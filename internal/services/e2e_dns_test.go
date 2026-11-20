package services_test

import (
	"testing"
	"time"

	"github.com/morks/stackit_mock/internal/store"
)

func TestDNSFlow(t *testing.T) {
	e := newEnv(t, 0)
	z := "/dns/v1/projects/p1/zones"

	e.do("POST", z, map[string]any{"name": "x", "dnsName": "bad name"}, 400)
	e.do("POST", z, map[string]any{"dnsName": "example.com."}, 400)

	created := e.do("POST", z, map[string]any{"name": "ex", "dnsName": "example.com."}, 202)
	zone := created["zone"].(map[string]any)
	requireKeys(t, zone, "id", "name", "dnsName", "state", "type", "acl", "visibility", "primaryNameServer",
		"defaultTTL", "expireTime", "negativeCache", "refreshTime", "retryTime", "serialNumber",
		"creationStarted", "creationFinished", "updateStarted", "updateFinished")
	requireKeys(t, created, "message")
	zid := zone["id"].(string)
	if zone["type"] != "primary" {
		t.Fatalf("type %v", zone["type"])
	}
	e.do("POST", z, map[string]any{"name": "ex2", "dnsName": "example.com."}, 409)

	got := e.do("GET", z+"/"+zid, nil, 200)["zone"].(map[string]any)
	if got["state"] != "CREATE_SUCCEEDED" {
		t.Fatalf("state %v", got["state"])
	}
	if l := e.do("GET", z+"?dnsNameEq=example.com.", nil, 200); len(l["zones"].([]any)) != 1 || l["totalItems"].(float64) != 1 {
		t.Fatalf("list %v", l)
	}
	if l := e.do("GET", z+"?dnsNameEq=other.", nil, 200); len(l["zones"].([]any)) != 0 {
		t.Fatalf("filter %v", l)
	}
	e.do("PATCH", z+"/"+zid, map[string]any{"description": "d"}, 200)
	if got := e.do("GET", z+"/"+zid, nil, 200)["zone"].(map[string]any); got["description"] != "d" || got["state"] != "UPDATE_SUCCEEDED" {
		t.Fatalf("patched zone %v", got)
	}

	rr := z + "/" + zid + "/rrsets"
	e.do("POST", rr, map[string]any{"name": "www.example.com.", "type": "BOGUS", "records": []any{map[string]any{"content": "1.2.3.4"}}}, 400)
	e.do("POST", z+"/nope/rrsets", map[string]any{"name": "a", "type": "A", "records": []any{map[string]any{"content": "1.2.3.4"}}}, 404)
	body := map[string]any{"name": "www.example.com.", "type": "A", "records": []any{map[string]any{"content": "1.2.3.4"}}}
	rs := e.do("POST", rr, body, 202)["rrset"].(map[string]any)
	requireKeys(t, rs, "id", "name", "type", "state", "ttl", "records", "active")
	if rs["name"] != "www.example.com." || rs["ttl"].(float64) != 3600 {
		t.Fatalf("rrset %v", rs)
	}
	rid := rs["id"].(string)
	e.do("POST", rr, body, 409)
	if rec := rs["records"].([]any)[0].(map[string]any); rec["id"] == "" || rec["content"] != "1.2.3.4" {
		t.Fatalf("record %v", rec)
	}
	if g := e.do("GET", rr+"/"+rid, nil, 200)["rrset"].(map[string]any); g["state"] != "CREATE_SUCCEEDED" {
		t.Fatalf("rrset state %v", g["state"])
	}
	e.do("PATCH", rr+"/"+rid, map[string]any{"ttl": 60, "records": []any{map[string]any{"content": "5.6.7.8"}}}, 200)
	g := e.do("GET", rr+"/"+rid, nil, 200)["rrset"].(map[string]any)
	if g["ttl"].(float64) != 60 || g["records"].([]any)[0].(map[string]any)["content"] != "5.6.7.8" {
		t.Fatalf("patched rrset %v", g)
	}
	if l := e.do("GET", rr, nil, 200); len(l["rrSets"].([]any)) != 1 {
		t.Fatalf("rrsets %v", l)
	}
	if got := e.do("GET", z+"/"+zid, nil, 200)["zone"].(map[string]any); got["recordCount"].(float64) != 1 {
		t.Fatalf("recordCount %v", got["recordCount"])
	}

	// rrset delete is a tombstone
	e.do("DELETE", rr+"/"+rid, nil, 202)
	if g := e.do("GET", rr+"/"+rid, nil, 200)["rrset"].(map[string]any); g["state"] != "DELETE_SUCCEEDED" {
		t.Fatalf("tombstone state %v", g["state"])
	}
	if l := e.do("GET", rr, nil, 200); len(l["rrSets"].([]any)) != 0 {
		t.Fatalf("tombstone listed %v", l)
	}
	e.do("POST", rr, body, 202) // name/type free again

	// zone delete cascades
	e.do("DELETE", z+"/"+zid, nil, 202)
	if g := e.do("GET", z+"/"+zid, nil, 200)["zone"].(map[string]any); g["state"] != "DELETE_SUCCEEDED" {
		t.Fatalf("zone tombstone %v", g["state"])
	}
	if l := e.do("GET", z, nil, 200); len(l["zones"].([]any)) != 0 {
		t.Fatalf("zone listed %v", l)
	}
	e.do("POST", rr, body, 404)
	e.do("PATCH", z+"/"+zid, map[string]any{"description": "x"}, 404)
	for _, r := range e.st.List(store.Filter{Type: "dns/recordset"}) {
		if r.Status != "DELETE_SUCCEEDED" {
			t.Fatalf("child not tombstoned: %+v", r)
		}
	}

	// re-create after delete
	again := e.do("POST", z, map[string]any{"name": "ex", "dnsName": "example.com."}, 202)["zone"].(map[string]any)
	if again["id"] == zid {
		t.Fatal("expected new id")
	}
}

func TestDNSTombstoneDelayed(t *testing.T) {
	e := newEnv(t, 50*time.Millisecond)
	z := "/dns/v1/projects/p1/zones"
	zid := e.do("POST", z, map[string]any{"name": "ex", "dnsName": "slow.example."}, 202)["zone"].(map[string]any)["id"].(string)
	if s := e.do("GET", z+"/"+zid, nil, 200)["zone"].(map[string]any)["state"]; s != "CREATING" {
		t.Fatalf("state %v", s)
	}
	waitState := func(want string) {
		t.Helper()
		for i := 0; i < 100; i++ {
			if e.do("GET", z+"/"+zid, nil, 200)["zone"].(map[string]any)["state"] == want {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("never reached %s", want)
	}
	waitState("CREATE_SUCCEEDED")
	e.do("DELETE", z+"/"+zid, nil, 202)
	if s := e.do("GET", z+"/"+zid, nil, 200)["zone"].(map[string]any)["state"]; s != "DELETING" {
		t.Fatalf("state %v", s)
	}
	waitState("DELETE_SUCCEEDED")
}
