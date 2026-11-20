package services_test

import (
	"testing"
)

const orgID = "11111111-2222-4333-8444-555555555555"

func items(t *testing.T, m map[string]any) []any {
	t.Helper()
	l, ok := m["items"].([]any)
	if !ok {
		t.Fatalf("items is not a list: %v", m)
	}
	return l
}

func TestFolders(t *testing.T) {
	e := newEnv(t, 0)
	rm := "/resourcemanager/v2"

	f := e.do("POST", rm+"/folders", map[string]any{"containerParentId": orgID, "name": "landing-zone",
		"labels": map[string]any{"env": "dev"}}, 201)
	requireKeys(t, f, "containerId", "folderId", "name", "parent", "creationTime", "updateTime")
	if p := f["parent"].(map[string]any); p["type"] != "ORGANIZATION" {
		t.Errorf("folder parent: %v", p)
	}
	cid, fid := f["containerId"].(string), f["folderId"].(string)

	e.do("POST", rm+"/folders", map[string]any{"containerParentId": orgID, "name": "landing-zone"}, 409)

	got := e.do("GET", rm+"/folders/"+cid, nil, 200)
	if got["folderId"] != fid {
		t.Errorf("get: %v", got)
	}
	// SDK ParentListInner requires id, containerId, name and type
	ps, _ := got["parents"].([]any)
	if len(ps) == 0 {
		t.Fatalf("parents missing: %v", got)
	}
	requireKeys(t, ps[0].(map[string]any), "id", "containerId", "name", "type")
	e.do("GET", rm+"/folders/"+fid, nil, 200)
	if n := len(items(t, e.do("GET", rm+"/folders", nil, 200))); n != 1 {
		t.Errorf("list len %d", n)
	}
	up := e.do("PATCH", rm+"/folders/"+cid, map[string]any{"name": "landing-zone2", "labels": map[string]any{"a": "b"}}, 200)
	if up["name"] != "landing-zone2" || up["labels"].(map[string]any)["env"] != "dev" {
		t.Errorf("patch: %v", up)
	}

	// project below the folder
	pr := e.do("POST", rm+"/projects", map[string]any{"containerParentId": cid, "name": "proj one"}, 201)
	par := pr["parent"].(map[string]any)
	if par["type"] != "FOLDER" || par["id"] != fid || par["containerId"] != cid {
		t.Errorf("project parent: %v", par)
	}
	e.do("DELETE", rm+"/folders/"+cid, nil, 409)
	e.do("DELETE", rm+"/projects/"+pr["projectId"].(string), nil, 202)
	e.do("DELETE", rm+"/folders/"+cid, nil, 204)
	e.do("GET", rm+"/folders/"+cid, nil, 404)
	e.do("DELETE", rm+"/folders/"+cid, nil, 404)

	// unknown parent keeps the old heuristic
	pr = e.do("POST", rm+"/projects", map[string]any{"containerParentId": orgID, "name": "proj two"}, 201)
	if pr["parent"].(map[string]any)["type"] != "ORGANIZATION" {
		t.Errorf("org parent: %v", pr["parent"])
	}
}

func TestNetworkAreaRangesAndRoutes(t *testing.T) {
	e := newEnv(t, 0)
	o := "/iaas/v2/organizations/" + orgID
	area := e.do("POST", o+"/network-areas", map[string]any{"name": "a1"}, 201)["id"].(string)
	rb := o + "/network-areas/" + area + "/regions/eu01"

	e.do("GET", rb+"/routes", nil, 404) // region missing
	e.do("PUT", o+"/network-areas/nope/regions/eu01", map[string]any{}, 404)
	e.do("PUT", rb, map[string]any{"ipv4": map[string]any{"networkRanges": []any{map[string]any{"prefix": "10.0.0.0/16"}}}}, 202)

	reg := e.do("GET", rb, nil, 200)
	rs := reg["ipv4"].(map[string]any)["networkRanges"].([]any)
	if len(rs) != 1 || rs[0].(map[string]any)["id"] == nil {
		t.Fatalf("embedded ranges: %v", rs)
	}
	firstID := rs[0].(map[string]any)["id"]

	created := e.do("POST", rb+"/network-ranges", map[string]any{"ipv4": []any{map[string]any{"prefix": "10.1.0.0/16"}}}, 201)
	cr := items(t, created)
	if len(cr) != 1 {
		t.Fatalf("created: %v", created)
	}
	rid := cr[0].(map[string]any)["id"].(string)
	e.do("POST", rb+"/network-ranges", map[string]any{"ipv4": []any{map[string]any{"prefix": "10.1.0.0/16"}}}, 409)
	e.do("POST", rb+"/network-ranges", map[string]any{"ipv4": []any{map[string]any{"prefix": "bogus"}}}, 400)

	reg = e.do("GET", rb, nil, 200)
	if n := len(reg["ipv4"].(map[string]any)["networkRanges"].([]any)); n != 2 {
		t.Errorf("region ranges after add: %d", n)
	}
	if n := len(items(t, e.do("GET", rb+"/network-ranges", nil, 200))); n != 2 {
		t.Errorf("list ranges: %d", n)
	}
	if g := e.do("GET", rb+"/network-ranges/"+rid, nil, 200); g["prefix"] != "10.1.0.0/16" {
		t.Errorf("get range: %v", g)
	}

	// PUT keeps the id of an unchanged prefix
	reg = e.do("PUT", rb, map[string]any{"ipv4": map[string]any{"networkRanges": []any{map[string]any{"prefix": "10.0.0.0/16"}}}}, 200)
	if got := reg["ipv4"].(map[string]any)["networkRanges"].([]any)[0].(map[string]any)["id"]; got != firstID {
		t.Errorf("range id changed: %v vs %v", got, firstID)
	}
	e.do("POST", rb+"/network-ranges", map[string]any{"ipv4": []any{map[string]any{"prefix": "10.1.0.0/16"}}}, 201)
	rid = items(t, e.do("GET", rb+"/network-ranges", nil, 200))[1].(map[string]any)["id"].(string)

	e.do("DELETE", rb+"/network-ranges/"+rid, nil, 202)
	e.do("GET", rb+"/network-ranges/"+rid, nil, 404)
	reg = e.do("GET", rb, nil, 200)
	if n := len(reg["ipv4"].(map[string]any)["networkRanges"].([]any)); n != 1 {
		t.Errorf("region ranges after delete: %d", n)
	}

	// routes
	rt := e.do("POST", rb+"/routes", map[string]any{"items": []any{map[string]any{
		"destination": map[string]any{"type": "cidrv4", "value": "0.0.0.0/0"},
		"nextHop":     map[string]any{"type": "ipv4", "value": "10.0.0.1"}}}}, 201)
	ri := items(t, rt)
	route := ri[0].(map[string]any)
	id := route["id"].(string)
	if route["nexthop"] == nil || route["nextHop"] != nil {
		t.Errorf("route: %v", route)
	}
	e.do("POST", rb+"/routes", map[string]any{"items": []any{map[string]any{
		"destination": map[string]any{"type": "cidrv4", "value": "0.0.0.0/0"},
		"nexthop":     map[string]any{"type": "wormhole"}}}}, 400)
	if n := len(items(t, e.do("GET", rb+"/routes", nil, 200))); n != 1 {
		t.Errorf("routes list %d", n)
	}
	e.do("GET", rb+"/routes/"+id, nil, 200)
	up := e.do("PATCH", rb+"/routes/"+id, map[string]any{"nexthop": map[string]any{"type": "blackhole"}}, 200)
	if up["nexthop"].(map[string]any)["type"] != "blackhole" {
		t.Errorf("patch: %v", up)
	}
	e.do("DELETE", rb+"/routes/"+id, nil, 202)
	e.do("GET", rb+"/routes/"+id, nil, 404)
	if n := len(items(t, e.do("GET", rb+"/routes", nil, 200))); n != 0 {
		t.Errorf("routes after delete %d", n)
	}
}

func TestNetworkAreaProjects(t *testing.T) {
	e := newEnv(t, 0)
	o := "/iaas/v2/organizations/" + orgID
	area := e.do("POST", o+"/network-areas", map[string]any{"name": "a1"}, 201)["id"].(string)
	ab := o + "/network-areas/" + area
	e.do("PUT", ab+"/regions/eu01", map[string]any{}, 202)
	e.do("POST", ab+"/regions/eu01/routes", map[string]any{"items": []any{map[string]any{
		"destination": map[string]any{"type": "cidrv4", "value": "10.0.0.0/8"},
		"nexthop":     map[string]any{"type": "internet"}}}}, 201)

	if l := items(t, e.do("GET", ab+"/projects", nil, 200)); len(l) != 0 {
		t.Errorf("projects: %v", l)
	}
	if c := e.do("GET", ab, nil, 200)["projectCount"]; c != float64(0) {
		t.Errorf("projectCount %v", c)
	}

	pr := e.do("POST", "/resourcemanager/v2/projects", map[string]any{"containerParentId": orgID, "name": "lz project",
		"labels": map[string]any{"networkArea": area}}, 201)
	pid := pr["projectId"].(string)
	e.do("POST", "/resourcemanager/v2/projects", map[string]any{"containerParentId": orgID, "name": "other project"}, 201)

	l := items(t, e.do("GET", ab+"/projects", nil, 200))
	if len(l) != 1 || l[0] != pid {
		t.Errorf("projects: %v", l)
	}
	if c := e.do("GET", ab, nil, 200)["projectCount"]; c != float64(1) {
		t.Errorf("projectCount %v", c)
	}
	if c := items(t, e.do("GET", o+"/network-areas", nil, 200))[0].(map[string]any)["projectCount"]; c != float64(1) {
		t.Errorf("list projectCount %v", c)
	}
	det := e.do("GET", "/iaas/v2/projects/"+pid, nil, 200)
	requireKeys(t, det, "id", "areaId", "status", "createdAt", "updatedAt")
	if det["areaId"] != area {
		t.Errorf("details: %v", det)
	}
	e.do("GET", "/iaas/v2/projects/unknown", nil, 404)

	e.do("DELETE", ab, nil, 409)
	e.do("DELETE", "/resourcemanager/v2/projects/"+pid, nil, 202)
	e.do("DELETE", ab, nil, 202)
	e.do("GET", ab, nil, 404)
	e.do("GET", ab+"/regions/eu01", nil, 404)
}

func TestAffinityGroups(t *testing.T) {
	e := newEnv(t, 0)
	p := "/iaas/v2/projects/p1/regions/eu01"

	e.do("POST", p+"/affinity-groups", map[string]any{"name": "ag", "policy": "bogus"}, 400)
	ag := e.do("POST", p+"/affinity-groups", map[string]any{"name": "ag", "policy": "hard-anti-affinity"}, 201)
	requireKeys(t, ag, "id", "name", "policy", "members")
	id := ag["id"].(string)
	e.do("POST", p+"/affinity-groups", map[string]any{"name": "ag", "policy": "soft-affinity"}, 409)
	if n := len(items(t, e.do("GET", p+"/affinity-groups", nil, 200))); n != 1 {
		t.Errorf("list %d", n)
	}

	net := e.do("POST", p+"/networks", map[string]any{"name": "n", "ipv4": map[string]any{"prefixLength": 24}}, 201)
	srv := func(name, group string) map[string]any {
		return map[string]any{"name": name, "machineType": "c2i.1", "affinityGroup": group,
			"networking": map[string]any{"networkId": net["id"]}}
	}
	e.do("POST", p+"/servers", srv("s0", "missing"), 400)
	s := e.do("POST", p+"/servers", srv("s1", id), 201)
	sid := s["id"].(string)

	members := e.do("GET", p+"/affinity-groups/"+id, nil, 200)["members"].([]any)
	if len(members) != 1 || members[0] != sid {
		t.Errorf("members: %v", members)
	}
	e.do("DELETE", p+"/affinity-groups/"+id, nil, 409)
	e.do("DELETE", p+"/servers/"+sid, nil, 202)
	if m := e.do("GET", p+"/affinity-groups/"+id, nil, 200)["members"].([]any); len(m) != 0 {
		t.Errorf("members after delete: %v", m)
	}
	e.do("DELETE", p+"/affinity-groups/"+id, nil, 202)
	e.do("GET", p+"/affinity-groups/"+id, nil, 404)
}
