package iaas

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/netip"
	"slices"
	"time"

	"github.com/morks/stackit_mock/internal/crud"
	"github.com/morks/stackit_mock/internal/httpx"
	"github.com/morks/stackit_mock/internal/store"
)

const (
	rmProjectType = "resourcemanager/project"
	routeType     = "iaas/network-area-route"
	regionType    = "iaas/network-area-region"
)

var (
	affinityPolicies = []string{"hard-affinity", "soft-affinity", "hard-anti-affinity", "soft-anti-affinity"}
	nexthopTypes     = []string{"ipv4", "ipv6", "internet", "blackhole"}
	destTypes        = []string{"cidrv4", "cidrv6"}
)

// areaProjects returns the ids of Resource Manager projects labelled networkArea=<areaID>.
func (x *h) areaProjects(areaID string) []string {
	out := []string{}
	for _, p := range x.d.Store.List(store.Filter{Type: rmProjectType}) {
		if l := crud.Map(p.Data["labels"]); l != nil && l["networkArea"] == areaID {
			id, _ := p.Data["projectId"].(string)
			if id == "" {
				id = p.ID
			}
			out = append(out, id)
		}
	}
	return out
}

func (x *h) listAreaProjects(w http.ResponseWriter, r *http.Request) {
	area := r.PathValue("areaId")
	if !x.exists("iaas/network-area", r.PathValue("organizationId"), area) {
		httpx.ErrIaaS(w, r, 404, "network area not found")
		return
	}
	items := make([]any, 0)
	for _, id := range x.areaProjects(area) {
		items = append(items, id)
	}
	httpx.WriteJSON(w, 200, map[string]any{"items": items})
}

func (x *h) getProjectDetails(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("projectId")
	p, ok := x.d.Store.Get(rmProjectType, id, id)
	if !ok {
		for _, c := range x.d.Store.List(store.Filter{Type: rmProjectType}) {
			if c.Data["containerId"] == id {
				p, ok = c, true
				break
			}
		}
	}
	if !ok {
		httpx.ErrIaaS(w, r, 404, "project not found")
		return
	}
	area := "PUBLIC"
	if l := crud.Map(p.Data["labels"]); l != nil {
		if a, _ := l["networkArea"].(string); a != "" {
			area = a
		}
	}
	pid, _ := p.Data["projectId"].(string)
	httpx.WriteJSON(w, 200, map[string]any{"id": pid, "areaId": area, "status": "CREATED",
		"createdAt": p.Data["creationTime"], "updatedAt": p.Data["updateTime"]})
}

func (x *h) defineLandingZone() {
	d := x.d
	x.regions.BeforeDel = func(r *http.Request, res store.Resource) (int, string) {
		for _, rt := range d.Store.List(store.Filter{Type: routeType, Project: res.Project, Parent: res.Region}) {
			d.Store.Delete(rt.Type, rt.Project, rt.ID)
		}
		return 0, ""
	}

	x.routes = &crud.Coll{D: d, Err: httpx.ErrIaaS, Type: routeType, Service: "iaas", Label: "route",
		IDParam: "routeId", IDKey: "id", Stamp: true,
		Scope: func(r *http.Request) crud.Scope {
			return crud.Scope{Project: r.PathValue("areaId"), Region: r.PathValue("region"), Parent: r.PathValue("region")}
		}}

	x.affinity = base(d, "iaas/affinity-group", "affinity group", "affinityGroupId")
	x.affinity.Unique = true
	x.affinity.Stamp = false
	x.affinity.Prepare = func(r *http.Request, s crud.Scope, b map[string]any) error {
		if httpx.Str(b, "name") == "" {
			return errBad("name is required")
		}
		if !slices.Contains(affinityPolicies, httpx.Str(b, "policy")) {
			return errBad("policy must be one of " + "hard-affinity, soft-affinity, hard-anti-affinity, soft-anti-affinity")
		}
		return nil
	}
	x.affinity.Decorate = func(m map[string]any, res store.Resource) {
		members := []any{}
		for _, sv := range d.Store.List(store.Filter{Type: "iaas/server", Project: res.Project}) {
			if sv.Data["affinityGroup"] == res.ID {
				members = append(members, sv.ID)
			}
		}
		m["members"] = members
	}
	x.affinity.BeforeDel = func(r *http.Request, res store.Resource) (int, string) {
		for _, sv := range d.Store.List(store.Filter{Type: "iaas/server", Project: res.Project}) {
			if sv.Data["affinityGroup"] == res.ID {
				return 409, "affinity group still has members"
			}
		}
		return 0, ""
	}
}

func base(d *httpx.Deps, typ, label, idParam string) *crud.Coll {
	return &crud.Coll{D: d, Err: httpx.ErrIaaS, Type: typ, Service: "iaas", Label: label,
		IDParam: idParam, IDKey: "id", Stamp: true, Scope: projScope}
}

// regionGuard 404s unless network area and region exist.
func (x *h) regionGuard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		area := r.PathValue("areaId")
		if !x.exists("iaas/network-area", r.PathValue("organizationId"), area) {
			httpx.ErrIaaS(w, r, 404, "network area not found")
			return
		}
		if !x.exists(regionType, area, r.PathValue("region")) {
			httpx.ErrIaaS(w, r, 404, "network area region not found")
			return
		}
		next(w, r)
	}
}

func (x *h) registerLandingZone(mux *http.ServeMux, ab string) {
	rb := ab + "/regions/{region}"
	g := x.regionGuard

	// network ranges (stored inside the region's ipv4.networkRanges)
	mux.HandleFunc("POST "+rb+"/network-ranges", g(x.createRanges))
	mux.HandleFunc("GET "+rb+"/network-ranges", g(func(w http.ResponseWriter, r *http.Request) {
		reg, _ := x.d.Store.Get(regionType, r.PathValue("areaId"), r.PathValue("region"))
		httpx.WriteJSON(w, 200, map[string]any{"items": rangesOf(reg)})
	}))
	mux.HandleFunc("GET "+rb+"/network-ranges/{rangeId}", g(func(w http.ResponseWriter, r *http.Request) {
		reg, _ := x.d.Store.Get(regionType, r.PathValue("areaId"), r.PathValue("region"))
		for _, rg := range rangesOf(reg) {
			if crud.Map(rg)["id"] == r.PathValue("rangeId") {
				httpx.WriteJSON(w, 200, rg)
				return
			}
		}
		httpx.ErrIaaS(w, r, 404, "network range not found")
	}))
	mux.HandleFunc("DELETE "+rb+"/network-ranges/{rangeId}", g(x.deleteRange))

	// routes
	mux.HandleFunc("POST "+rb+"/routes", g(x.createRoutes))
	mux.HandleFunc("GET "+rb+"/routes", g(x.routes.List))
	mux.HandleFunc("GET "+rb+"/routes/{routeId}", g(x.routes.Get))
	mux.HandleFunc("PATCH "+rb+"/routes/{routeId}", g(x.patchRoute))
	mux.HandleFunc("DELETE "+rb+"/routes/{routeId}", g(x.routes.Delete))

	// affinity groups
	ag := R + "/affinity-groups"
	mux.HandleFunc("POST "+ag, x.affinity.Create)
	mux.HandleFunc("GET "+ag, x.affinity.List)
	mux.HandleFunc("GET "+ag+"/{affinityGroupId}", x.affinity.Get)
	mux.HandleFunc("DELETE "+ag+"/{affinityGroupId}", x.affinity.Delete)
}

func rangesOf(reg store.Resource) []any {
	out := []any{}
	if v4 := crud.Map(reg.Data["ipv4"]); v4 != nil {
		out = append(out, listOf(v4["networkRanges"])...)
	}
	return out
}

// setRanges replaces the region's ranges copy-on-write.
func (x *h) setRanges(area, region string, ranges []any) {
	x.d.Store.Update(regionType, area, region, func(res *store.Resource) {
		data := crud.DeepMerge(res.Data, nil)
		v4 := crud.DeepMerge(crud.Map(data["ipv4"]), nil)
		v4["networkRanges"] = ranges
		data["ipv4"] = v4
		res.Data = data
	})
}

func (x *h) createRanges(w http.ResponseWriter, r *http.Request) {
	b, ok := httpx.Decode(w, r)
	if !ok {
		return
	}
	area, region := r.PathValue("areaId"), r.PathValue("region")
	reg, _ := x.d.Store.Get(regionType, area, region)
	cur := rangesOf(reg)
	var added []any
	for _, it := range listOf(b["ipv4"]) {
		prefix := httpx.Str(crud.Map(it), "prefix")
		if _, err := netip.ParsePrefix(prefix); err != nil {
			httpx.ErrIaaS(w, r, 400, "invalid prefix "+prefix)
			return
		}
		for _, e := range append(slices.Clone(cur), added...) {
			if crud.Map(e)["prefix"] == prefix {
				httpx.ErrIaaS(w, r, 409, "network range "+prefix+" already exists")
				return
			}
		}
		added = append(added, map[string]any{"id": httpx.UUID(), "prefix": prefix})
	}
	if len(added) == 0 {
		httpx.ErrIaaS(w, r, 400, "ipv4 must contain at least one prefix")
		return
	}
	x.setRanges(area, region, append(slices.Clone(cur), added...))
	httpx.WriteJSON(w, 201, map[string]any{"items": added})
}

func (x *h) deleteRange(w http.ResponseWriter, r *http.Request) {
	area, region := r.PathValue("areaId"), r.PathValue("region")
	reg, _ := x.d.Store.Get(regionType, area, region)
	kept := []any{}
	found := false
	for _, rg := range rangesOf(reg) {
		if crud.Map(rg)["id"] == r.PathValue("rangeId") {
			found = true
			continue
		}
		kept = append(kept, rg)
	}
	if !found {
		httpx.ErrIaaS(w, r, 404, "network range not found")
		return
	}
	x.setRanges(area, region, kept)
	w.WriteHeader(http.StatusAccepted)
}

// putRegion wraps region PUT/PATCH: validates the area, gives embedded ranges ids
// (keeping ids of unchanged prefixes) and keeps existing ipv4 defaults on PUT.
func (x *h) putRegion(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		area := r.PathValue("areaId")
		if !x.exists("iaas/network-area", r.PathValue("organizationId"), area) {
			httpx.ErrIaaS(w, r, 404, "network area not found")
			return
		}
		b, ok := httpx.Decode(w, r)
		if !ok {
			return
		}
		if reg, exists := x.d.Store.Get(regionType, area, r.PathValue("region")); exists {
			if v4 := crud.Map(b["ipv4"]); v4 != nil {
				if nr, ok := v4["networkRanges"].([]any); ok {
					for _, rg := range nr {
						m := crud.Map(rg)
						if m == nil || m["id"] != nil {
							continue
						}
						for _, old := range rangesOf(reg) {
							if crud.Map(old)["prefix"] == m["prefix"] {
								m["id"] = crud.Map(old)["id"]
							}
						}
						if m["id"] == nil {
							m["id"] = httpx.UUID()
						}
					}
				}
				b["ipv4"] = crud.DeepMerge(crud.Map(reg.Data["ipv4"]), v4)
			}
		}
		nb := mustJSON(b)
		r.Body = io.NopCloser(bytes.NewReader(nb))
		r.ContentLength = int64(len(nb))
		next(w, r)
	}
}

func validateRoute(m map[string]any) string {
	if v, ok := m["nextHop"]; ok {
		if m["nexthop"] == nil {
			m["nexthop"] = v
		}
		delete(m, "nextHop")
	}
	dest, nh := crud.Map(m["destination"]), crud.Map(m["nexthop"])
	if dest == nil || !slices.Contains(destTypes, httpx.Str(dest, "type")) || httpx.Str(dest, "value") == "" {
		return "destination requires type (cidrv4|cidrv6) and value"
	}
	if _, err := netip.ParsePrefix(httpx.Str(dest, "value")); err != nil {
		return "invalid destination prefix " + httpx.Str(dest, "value")
	}
	if nh == nil || !slices.Contains(nexthopTypes, httpx.Str(nh, "type")) {
		return "nexthop requires type (ipv4|ipv6|internet|blackhole)"
	}
	if t := httpx.Str(nh, "type"); (t == "ipv4" || t == "ipv6") && httpx.Str(nh, "value") == "" {
		return "nexthop of type " + t + " requires a value"
	}
	return ""
}

func (x *h) createRoutes(w http.ResponseWriter, r *http.Request) {
	b, ok := httpx.Decode(w, r)
	if !ok {
		return
	}
	items := listOf(b["items"])
	if len(items) == 0 {
		httpx.ErrIaaS(w, r, 400, "items must contain at least one route")
		return
	}
	s := x.routes.Scope(r)
	bodies := make([]map[string]any, 0, len(items))
	for _, it := range items {
		m := crud.Map(it)
		if m == nil {
			httpx.ErrIaaS(w, r, 400, "invalid route")
			return
		}
		if msg := validateRoute(m); msg != "" {
			httpx.ErrIaaS(w, r, 400, msg)
			return
		}
		m["id"] = httpx.UUID()
		m["createdAt"], m["updatedAt"] = nowStamp(), nowStamp()
		bodies = append(bodies, m)
	}
	out := make([]any, 0, len(bodies))
	for _, m := range bodies {
		res, err := x.d.Store.Create(store.Resource{Type: routeType, Service: "iaas", ID: m["id"].(string), Project: s.Project,
			Region: s.Region, Parent: s.Parent, Name: m["id"].(string), Status: "", Data: m})
		if err != nil {
			httpx.ErrIaaS(w, r, 409, "route already exists")
			return
		}
		out = append(out, x.routes.View(res))
	}
	httpx.WriteJSON(w, 201, map[string]any{"items": out})
}

func (x *h) patchRoute(w http.ResponseWriter, r *http.Request) {
	b, ok := httpx.Decode(w, r)
	if !ok {
		return
	}
	s := x.routes.Scope(r)
	cur, found := x.d.Store.Get(routeType, s.Project, r.PathValue("routeId"))
	if !found {
		httpx.ErrIaaS(w, r, 404, "route not found")
		return
	}
	if v, ok := b["nextHop"]; ok {
		b["nexthop"] = v
		delete(b, "nextHop")
	}
	merged := crud.DeepMerge(cur.Data, b)
	merged["id"] = cur.ID
	if msg := validateRoute(merged); msg != "" {
		httpx.ErrIaaS(w, r, 400, msg)
		return
	}
	merged["updatedAt"] = nowStamp()
	out, _ := x.d.Store.Update(routeType, s.Project, cur.ID, func(res *store.Resource) { res.Data = merged })
	httpx.WriteJSON(w, 200, x.routes.View(out))
}

func nowStamp() string { return time.Now().UTC().Format(time.RFC3339) }

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
