package services_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/morks/stackit_mock/internal/httpx"
	"github.com/morks/stackit_mock/internal/lifecycle"
	"github.com/morks/stackit_mock/internal/server"
	"github.com/morks/stackit_mock/internal/services"
	"github.com/morks/stackit_mock/internal/store"
)

type env struct {
	t   *testing.T
	ts  *httptest.Server
	st  *store.Store
	hdr http.Header
}

func newEnv(t *testing.T, delay time.Duration) *env {
	st := store.New()
	d := &httpx.Deps{Store: st, Engine: lifecycle.New(st, delay)}
	server.Registrars = services.All()
	h, _ := server.New(d, server.Options{Quiet: true})
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return &env{t: t, ts: ts, st: st}
}

func (e *env) do(method, path string, body any, want int) map[string]any {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.ts.URL+path, rd)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	e.hdr = resp.Header
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		e.t.Fatalf("%s %s: got %d want %d: %s", method, path, resp.StatusCode, want, raw)
	}
	out := map[string]any{}
	if len(raw) > 0 {
		if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
			e.t.Fatalf("content-type %q", ct)
		}
		_ = json.Unmarshal(raw, &out)
	}
	return out
}

func requireKeys(t *testing.T, m map[string]any, keys ...string) {
	t.Helper()
	for _, k := range keys {
		if _, ok := m[k]; !ok {
			t.Errorf("missing required key %q in %v", k, m)
		}
	}
}

func TestIaaSFlow(t *testing.T) {
	e := newEnv(t, 0)
	p := "/iaas/v2/projects/p1/regions/eu01"

	net := e.do("POST", p+"/networks", map[string]any{"name": "n1", "ipv4": map[string]any{"prefixLength": 24}}, 201)
	requireKeys(t, net, "id", "name", "status")
	net = e.do("GET", p+"/networks/"+net["id"].(string), nil, 200)
	if net["status"] != "CREATED" {
		t.Fatalf("status %v", net["status"])
	}
	if ps := net["ipv4"].(map[string]any)["prefixes"].([]any); len(ps) != 1 {
		t.Fatalf("prefixes %v", ps)
	}
	nid := net["id"].(string)
	e.do("POST", p+"/networks", map[string]any{"name": "n1"}, 409)

	e.do("POST", p+"/servers", map[string]any{"name": "s", "machineType": "c2i.1", "networking": map[string]any{"networkId": "nope"}}, 400)
	srv := e.do("POST", p+"/servers", map[string]any{"name": "s", "machineType": "c2i.1",
		"networking": map[string]any{"networkId": nid},
		"bootVolume": map[string]any{"source": map[string]any{"id": "a0000000-0000-4000-8000-000000000001", "type": "image"}, "deleteOnTermination": true}}, 201)
	requireKeys(t, srv, "id", "status", "machineType", "nics")
	srv = e.do("GET", p+"/servers/"+srv["id"].(string), nil, 200)
	if srv["status"] != "ACTIVE" {
		t.Fatalf("server %v", srv["status"])
	}
	sid := srv["id"].(string)
	e.do("DELETE", p+"/networks/"+nid, nil, 409) // NIC still present

	vol := e.do("POST", p+"/volumes", map[string]any{"name": "v", "availabilityZone": "eu01-1", "size": 10}, 201)
	vid := vol["id"].(string)
	e.do("PUT", p+"/servers/"+sid+"/volume-attachments/"+vid, map[string]any{}, 200)
	reqID := e.hdr.Get("X-Request-Id")
	if reqID == "" {
		t.Fatal("no X-Request-Id")
	}
	r := e.do("GET", p+"/requests/"+reqID, nil, 200)
	if r["requestAction"] != "CREATE" || r["status"] != "CREATED" {
		t.Fatalf("request %v", r)
	}
	e.do("GET", p+"/servers/"+sid+"/volume-attachments/"+vid, nil, 200)
	e.do("DELETE", p+"/volumes/"+vid, nil, 409)
	e.do("DELETE", p+"/servers/"+sid+"/volume-attachments/"+vid, nil, 202)
	e.do("GET", p+"/servers/"+sid+"/volume-attachments/"+vid, nil, 404)
	e.do("DELETE", p+"/volumes/"+vid, nil, 202)
	e.do("GET", p+"/volumes/"+vid, nil, 404)

	ip := e.do("POST", p+"/public-ips", map[string]any{}, 201)
	requireKeys(t, ip, "id", "ip")

	sg := e.do("POST", p+"/security-groups", map[string]any{"name": "sg"}, 201)
	rule := e.do("POST", p+"/security-groups/"+sg["id"].(string)+"/rules", map[string]any{"direction": "ingress", "protocol": "tcp"}, 201)
	if pr, ok := rule["protocol"].(map[string]any); !ok || pr["name"] != "tcp" {
		t.Fatalf("protocol must be an object: %v", rule["protocol"])
	}
	e.do("POST", p+"/security-groups/nope/rules", map[string]any{"direction": "ingress"}, 400)

	e.do("POST", "/iaas/v2/keypairs", map[string]any{"name": "k", "publicKey": "ssh-ed25519 AAAA"}, 201)
	e.do("GET", "/iaas/v2/keypairs/k", nil, 200)
	e.do("DELETE", "/iaas/v2/keypairs/k", nil, 202)
	e.do("GET", "/iaas/v2/keypairs/k", nil, 404)

	img := e.do("POST", p+"/images", map[string]any{"name": "i", "diskFormat": "qcow2"}, 201)
	requireKeys(t, img, "id", "uploadUrl")
	req, _ := http.NewRequest("PUT", img["uploadUrl"].(string), bytes.NewReader([]byte("data")))
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != 200 {
		t.Fatalf("upload: %v %v", err, resp)
	}
	if got := e.do("GET", p+"/images/"+img["id"].(string), nil, 200); got["status"] != "AVAILABLE" {
		t.Fatalf("image %v", got["status"])
	}

	e.do("DELETE", p+"/servers/"+sid, nil, 202)
	e.do("GET", p+"/servers/"+sid, nil, 404)
	e.do("DELETE", p+"/networks/"+nid, nil, 202)
	e.do("GET", p+"/networks/"+nid, nil, 404)
	e.do("DELETE", p+"/networks/"+nid, nil, 404)
}

func TestAsyncStatusVisible(t *testing.T) {
	e := newEnv(t, 80*time.Millisecond)
	p := "/iaas/v2/projects/p1/regions/eu01"
	net := e.do("POST", p+"/networks", map[string]any{"name": "n"}, 201)
	if net["status"] != "CREATING" {
		t.Fatalf("want CREATING got %v", net["status"])
	}
	time.Sleep(200 * time.Millisecond)
	if got := e.do("GET", p+"/networks/"+net["id"].(string), nil, 200); got["status"] != "CREATED" {
		t.Fatalf("want CREATED got %v", got["status"])
	}
}

func TestProjectAndServiceAccount(t *testing.T) {
	e := newEnv(t, 0)
	pr := e.do("POST", "/resourcemanager/v2/projects", map[string]any{"name": "demo", "containerParentId": "11111111-1111-1111-1111-111111111111"}, 201)
	requireKeys(t, pr, "containerId", "projectId", "name", "lifecycleState", "creationTime", "updateTime", "parent")
	pid := pr["projectId"].(string)
	e.do("GET", "/resourcemanager/v2/projects/"+pr["containerId"].(string), nil, 200)
	e.do("POST", "/resourcemanager/v2/projects", map[string]any{"name": "!", "containerParentId": "x"}, 400)

	sa := e.do("POST", "/serviceaccount/v2/projects/"+pid+"/service-accounts", map[string]any{"name": "ci"}, 201)
	requireKeys(t, sa, "id", "email", "projectId", "internal")
	mail := sa["email"].(string)
	key := e.do("POST", "/serviceaccount/v2/projects/"+pid+"/service-accounts/"+mail+"/keys", map[string]any{}, 201)
	requireKeys(t, key, "id", "active", "createdAt", "publicKey", "keyAlgorithm", "keyOrigin", "keyType", "credentials")
	requireKeys(t, key["credentials"].(map[string]any), "aud", "iss", "kid", "sub")
	list := e.do("GET", "/serviceaccount/v2/projects/"+pid+"/service-accounts", nil, 200)
	if len(list["items"].([]any)) != 1 {
		t.Fatal("list")
	}
	e.do("DELETE", "/resourcemanager/v2/projects/"+pid, nil, 202)
	e.do("GET", "/resourcemanager/v2/projects/"+pid, nil, 404)
	if n := len(e.st.List(store.Filter{})); n != 0 {
		t.Fatalf("project delete left %d resources", n)
	}
}

func TestSKE(t *testing.T) {
	e := newEnv(t, 0)
	b := "/ske/v2/projects/p1/regions/eu01"
	st := e.do("POST", "/serviceenablement/v2/projects/p1/regions/eu01/services/cloud.stackit.ske", nil, 202)
	_ = st
	if got := e.do("GET", "/serviceenablement/v2/projects/p1/regions/eu01/services/cloud.stackit.ske", nil, 200); got["state"] != "ENABLED" {
		t.Fatalf("%v", got)
	}
	po := e.do("GET", "/ske/v2/regions/eu01/provider-options", nil, 200)
	requireKeys(t, po, "kubernetesVersions", "machineImages")

	e.do("PUT", b+"/clusters/c1", map[string]any{"kubernetes": map[string]any{"version": "1.33.5"}}, 400)
	cl := e.do("PUT", b+"/clusters/c1", map[string]any{
		"kubernetes": map[string]any{"version": "1.33.5"},
		"nodepools": []any{map[string]any{"name": "np", "machine": map[string]any{"type": "g2i.2", "image": map[string]any{"name": "flatcar", "version": "4230.2.1"}},
			"minimum": 1, "maximum": 2, "volume": map[string]any{"size": 50}, "availabilityZones": []any{"eu01-1"}}}}, 202)
	cl = e.do("GET", b+"/clusters/c1", nil, 200)
	if cl["name"] != "c1" || cl["status"].(map[string]any)["aggregated"] != "STATE_HEALTHY" {
		t.Fatalf("%v", cl)
	}
	if idp := cl["access"].(map[string]any)["idp"].(map[string]any); idp["type"] != "stackit" {
		t.Fatalf("access.idp: %v", idp)
	}
	kc := e.do("POST", b+"/clusters/c1/kubeconfig", map[string]any{"expirationSeconds": "3600"}, 200)
	requireKeys(t, kc, "kubeconfig", "expirationTimestamp")
	e.do("DELETE", b+"/clusters/c1", nil, 202)
	if l := e.do("GET", b+"/clusters", nil, 200); len(l["items"].([]any)) != 0 {
		t.Fatal("cluster still listed")
	}
}

func TestPostgresFlex(t *testing.T) {
	e := newEnv(t, 0)
	b := "/postgresflex/v3/projects/p1/regions/eu01"
	fl := e.do("GET", b+"/flavors", nil, 200)
	requireKeys(t, fl, "flavors", "pagination")
	requireKeys(t, fl["pagination"].(map[string]any), "page", "size", "sort", "totalPages", "totalRows")
	fid := fl["flavors"].([]any)[0].(map[string]any)["id"]

	e.do("POST", b+"/instances", map[string]any{"name": "pg", "version": "16", "flavorId": "bogus"}, 400)
	c := e.do("POST", b+"/instances", map[string]any{"name": "pg", "version": "16", "flavorId": fid,
		"backupSchedule": "0 0 * * *", "retentionDays": 7, "network": map[string]any{"acl": []any{"1.2.3.4/32"}},
		"storage": map[string]any{"class": "premium-perf6-stackit", "size": 20}}, 201)
	id := c["id"].(string)
	inst := e.do("GET", b+"/instances/"+id, nil, 200)
	requireKeys(t, inst, "id", "name", "state", "version", "flavorId", "backupSchedule", "retentionDays", "isDeletable", "network", "storage", "connectionInfo")
	if inst["state"] != "READY" {
		t.Fatalf("%v", inst["state"])
	}
	u := e.do("POST", b+"/instances/"+id+"/users", map[string]any{"name": "app"}, 201)
	requireKeys(t, u, "id", "name", "password", "state")
	if _, isNum := u["id"].(float64); !isNum {
		t.Fatalf("user id must be number: %v", u["id"])
	}
	uid := formatID(u["id"])
	gu := e.do("GET", b+"/instances/"+id+"/users/"+uid, nil, 200)
	requireKeys(t, gu, "id", "name", "roles", "state")
	if _, leaked := gu["password"]; leaked {
		t.Fatal("password leaked on GET")
	}
	db := e.do("POST", b+"/instances/"+id+"/databases", map[string]any{"name": "d", "owner": "app"}, 201)
	e.do("GET", b+"/instances/"+id+"/databases/"+formatID(db["id"]), nil, 200)
	e.do("DELETE", b+"/instances/"+id+"/users/"+uid, nil, 202)
	e.do("GET", b+"/instances/"+id+"/users/"+uid, nil, 404)
	e.do("DELETE", b+"/instances/"+id, nil, 202)
	e.do("GET", b+"/instances/"+id, nil, 404)
	if n := len(e.st.List(store.Filter{})); n != 0 {
		t.Fatalf("leftovers: %d", n)
	}
}

func formatID(v any) string { return jsonNum(v.(float64)) }

func jsonNum(f float64) string {
	b, _ := json.Marshal(int64(f))
	return string(b)
}

func TestMongoFlex(t *testing.T) {
	e := newEnv(t, 0)
	b := "/mongodbflex/v2/projects/p1/regions/eu01"
	c := e.do("POST", b+"/instances", map[string]any{"name": "m", "version": "7.0", "flavorId": "2.8", "replicas": 3,
		"backupSchedule": "0 0 * * *", "acl": map[string]any{"items": []any{"1.2.3.4/32"}},
		"storage": map[string]any{"class": "premium-perf2-mongodb", "size": 10}, "options": map[string]any{"type": "Replica"}}, 201)
	id := c["id"].(string)
	inst := e.do("GET", b+"/instances/"+id, nil, 200)["item"].(map[string]any)
	requireKeys(t, inst, "id", "status", "flavor", "options")
	if inst["status"] != "READY" {
		t.Fatalf("%v", inst["status"])
	}
	for _, k := range []string{"snapshotRetentionDays", "dailySnapshotRetentionDays", "weeklySnapshotRetentionWeeks", "monthlySnapshotRetentionMonths", "pointInTimeWindowHours", "type"} {
		if _, ok := inst["options"].(map[string]any)[k].(string); !ok {
			t.Errorf("option %s must be a string", k)
		}
	}
	bs := e.do("PUT", b+"/instances/"+id+"/backups", map[string]any{"backupSchedule": "1 1 * * *", "snapshotRetentionDays": 5}, 200)
	requireKeys(t, bs, "backupSchedule", "snapshotRetentionDays", "dailySnapshotRetentionDays", "weeklySnapshotRetentionWeeks", "monthlySnapshotRetentionMonths", "pointInTimeWindowHours")
	if bs["snapshotRetentionDays"].(float64) != 5 {
		t.Fatalf("%v", bs)
	}
	u := e.do("POST", b+"/instances/"+id+"/users", map[string]any{"database": "db", "roles": []any{"readWrite"}}, 201)["item"].(map[string]any)
	requireKeys(t, u, "id", "password", "username")
	g := e.do("GET", b+"/instances/"+id+"/users/"+u["id"].(string), nil, 200)["item"].(map[string]any)
	if _, leaked := g["password"]; leaked {
		t.Fatal("password leaked")
	}
	e.do("DELETE", b+"/instances/"+id, nil, 202)
	e.do("GET", b+"/instances/"+id, nil, 404)
}

func TestFaultInjectionAndAuth(t *testing.T) {
	e := newEnv(t, 0)
	e.do("POST", "/_admin/faults", map[string]any{"method": "GET", "path_prefix": "/iaas", "status": 503, "remaining": 1}, 201)
	e.do("GET", "/iaas/v2/projects/p/regions/eu01/networks", nil, 503)
	e.do("GET", "/iaas/v2/projects/p/regions/eu01/networks", nil, 200)
	ev := e.do("GET", "/_admin/events?since=0", nil, 200)
	requireKeys(t, ev, "last", "events")
	e.do("POST", "/token", nil, 200)
}
