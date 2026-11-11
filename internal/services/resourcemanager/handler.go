// Package resourcemanager mocks the Resource Manager API (v2) under /resourcemanager.
package resourcemanager

import (
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/morks/stackit_mock/internal/crud"
	"github.com/morks/stackit_mock/internal/httpx"
	"github.com/morks/stackit_mock/internal/store"
)

const typ = "resourcemanager/project"

var nameRe = regexp.MustCompile(`^[a-zA-ZäüöÄÜÖ0-9]( ?[a-zA-ZäüöÄÜÖß0-9_+&-]){1,40}$`)
var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func Register(mux *http.ServeMux, d *httpx.Deps) {
	// find resolves a project by UUID or containerId.
	find := func(id string) (store.Resource, bool) {
		if r, ok := d.Store.Get(typ, id, id); ok {
			return r, true
		}
		for _, r := range d.Store.List(store.Filter{Type: typ}) {
			if r.Data["containerId"] == id {
				return r, true
			}
		}
		return store.Resource{}, false
	}
	view := func(r store.Resource) map[string]any {
		m := map[string]any{}
		for k, v := range r.Data {
			if !strings.HasPrefix(k, "_") {
				m[k] = v
			}
		}
		m["lifecycleState"] = r.Status
		return m
	}

	mux.HandleFunc("POST /resourcemanager/v2/projects", func(w http.ResponseWriter, r *http.Request) {
		b, ok := httpx.Decode(w, r)
		if !ok {
			return
		}
		name, parent := httpx.Str(b, "name"), httpx.Str(b, "containerParentId")
		if !nameRe.MatchString(name) {
			httpx.ErrStackit(w, r, 400, "invalid project name")
			return
		}
		if parent == "" {
			httpx.ErrStackit(w, r, 400, "containerParentId is required")
			return
		}
		for _, p := range d.Store.List(store.Filter{Type: typ}) {
			if p.Name == name && p.Data["_parent"] == parent {
				httpx.ErrStackit(w, r, 409, "project with name "+name+" already exists")
				return
			}
		}
		id := httpx.UUID()
		ts := time.Now().UTC().Format(time.RFC3339)
		ptype := "FOLDER"
		if looksUUID(parent) {
			ptype = "ORGANIZATION"
		}
		data := map[string]any{
			"projectId": id, "containerId": strings.Trim(slugRe.ReplaceAllString(strings.ToLower(name), "-"), "-") + "-" + crud.Rand(6),
			"name": name, "creationTime": ts, "updateTime": ts, "_parent": parent,
			"parent": map[string]any{"containerId": parent, "id": parent, "type": ptype},
		}
		if l := crud.Map(b["labels"]); l != nil {
			data["labels"] = l
		}
		out, err := d.Engine.Create(store.Resource{Type: typ, Service: "resourcemanager", ID: id, Project: id,
			Name: name, Data: data}, "CREATING", "ACTIVE")
		if err != nil {
			httpx.ErrStackit(w, r, 409, "project already exists")
			return
		}
		httpx.WriteJSON(w, 201, view(out))
	})
	mux.HandleFunc("GET /resourcemanager/v2/projects", func(w http.ResponseWriter, r *http.Request) {
		items := []any{}
		for _, p := range d.Store.List(store.Filter{Type: typ}) {
			items = append(items, view(p))
		}
		httpx.WriteJSON(w, 200, map[string]any{"items": items})
	})
	mux.HandleFunc("GET /resourcemanager/v2/projects/{id}", func(w http.ResponseWriter, r *http.Request) {
		p, ok := find(r.PathValue("id"))
		if !ok {
			httpx.ErrStackit(w, r, 404, "project not found")
			return
		}
		httpx.WriteJSON(w, 200, view(p))
	})
	mux.HandleFunc("PATCH /resourcemanager/v2/projects/{id}", func(w http.ResponseWriter, r *http.Request) {
		b, ok := httpx.Decode(w, r)
		if !ok {
			return
		}
		p, ok := find(r.PathValue("id"))
		if !ok {
			httpx.ErrStackit(w, r, 404, "project not found")
			return
		}
		out, _ := d.Store.Update(typ, p.Project, p.ID, func(x *store.Resource) {
			if n := httpx.Str(b, "name"); n != "" {
				x.Name, x.Data["name"] = n, n
			}
			if l := crud.Map(b["labels"]); l != nil {
				x.Data["labels"] = crud.DeepMerge(crud.Map(x.Data["labels"]), l)
			}
			x.Data["updateTime"] = time.Now().UTC().Format(time.RFC3339)
		})
		httpx.WriteJSON(w, 200, view(out))
	})
	mux.HandleFunc("DELETE /resourcemanager/v2/projects/{id}", func(w http.ResponseWriter, r *http.Request) {
		p, ok := find(r.PathValue("id"))
		if !ok {
			httpx.ErrStackit(w, r, 404, "project not found")
			return
		}
		// Removing a project removes everything inside it.
		for _, c := range d.Store.List(store.Filter{Project: p.ID}) {
			if c.Type != typ {
				d.Store.Delete(c.Type, c.Project, c.ID)
			}
		}
		d.Engine.Delete(typ, p.Project, p.ID, "DELETING")
		w.WriteHeader(http.StatusAccepted)
	})
}

func looksUUID(s string) bool {
	return len(s) == 36 && s[8] == '-' && s[13] == '-' && s[18] == '-' && s[23] == '-'
}
