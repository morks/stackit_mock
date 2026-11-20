package resourcemanager

import (
	"net/http"
	"strings"
	"time"

	"github.com/morks/stackit_mock/internal/crud"
	"github.com/morks/stackit_mock/internal/httpx"
	"github.com/morks/stackit_mock/internal/store"
)

// findFolder resolves a folder by containerId or folderId.
func findFolder(d *httpx.Deps, id string) (store.Resource, bool) {
	if id == "" {
		return store.Resource{}, false
	}
	if r, ok := d.Store.Get(folderTyp, id, id); ok {
		return r, true
	}
	for _, r := range d.Store.List(store.Filter{Type: folderTyp}) {
		if r.Data["folderId"] == id {
			return r, true
		}
	}
	return store.Resource{}, false
}

func folderView(r store.Resource) map[string]any {
	m := map[string]any{}
	for k, v := range r.Data {
		if !strings.HasPrefix(k, "_") {
			m[k] = v
		}
	}
	return m
}

func registerFolders(mux *http.ServeMux, d *httpx.Deps) {
	const base = "/resourcemanager/v2/folders"
	notFound := func(w http.ResponseWriter, r *http.Request) {
		httpx.ErrStackit(w, r, 404, "folder not found")
	}
	nameTaken := func(name, parent, exceptID string) bool {
		for _, f := range d.Store.List(store.Filter{Type: folderTyp}) {
			if f.Name == name && f.Data["_parent"] == parent && f.ID != exceptID {
				return true
			}
		}
		return false
	}

	mux.HandleFunc("POST "+base, func(w http.ResponseWriter, r *http.Request) {
		b, ok := httpx.Decode(w, r)
		if !ok {
			return
		}
		name, parent := httpx.Str(b, "name"), httpx.Str(b, "containerParentId")
		if !nameRe.MatchString(name) {
			httpx.ErrStackit(w, r, 400, "invalid folder name")
			return
		}
		if parent == "" {
			httpx.ErrStackit(w, r, 400, "containerParentId is required")
			return
		}
		parentRef, parent := resolveParent(d, parent)
		if nameTaken(name, parent, "") {
			httpx.ErrStackit(w, r, 409, "folder with name "+name+" already exists")
			return
		}
		cid := strings.Trim(slugRe.ReplaceAllString(strings.ToLower(name), "-"), "-") + "-" + crud.Rand(6)
		ts := time.Now().UTC().Format(time.RFC3339)
		data := map[string]any{"containerId": cid, "folderId": httpx.UUID(), "name": name,
			"creationTime": ts, "updateTime": ts, "_parent": parent, "parent": parentRef}
		if l := crud.Map(b["labels"]); l != nil {
			data["labels"] = l
		}
		out, err := d.Store.Create(store.Resource{Type: folderTyp, Service: "resourcemanager", ID: cid, Project: cid,
			Name: name, Status: "ACTIVE", Data: data})
		if err != nil {
			httpx.ErrStackit(w, r, 409, "folder already exists")
			return
		}
		httpx.WriteJSON(w, 201, folderView(out))
	})
	mux.HandleFunc("GET "+base, func(w http.ResponseWriter, r *http.Request) {
		parent := r.URL.Query().Get("containerParentId")
		items := []any{}
		for _, f := range d.Store.List(store.Filter{Type: folderTyp}) {
			if parent != "" && f.Data["_parent"] != parent {
				continue
			}
			items = append(items, folderView(f))
		}
		httpx.WriteJSON(w, 200, map[string]any{"items": items})
	})
	mux.HandleFunc("GET "+base+"/{id}", func(w http.ResponseWriter, r *http.Request) {
		f, ok := findFolder(d, r.PathValue("id"))
		if !ok {
			notFound(w, r)
			return
		}
		v := folderView(f)
		// ancestor chain, nearest first
		parents := []any{}
		cur := f
		for i := 0; i < 10; i++ {
			pr := crud.Map(cur.Data["parent"])
			if pr == nil {
				break
			}
			// ParentListInner requires id, containerId, name and type.
			entry := map[string]any{"containerId": pr["containerId"], "id": pr["id"], "type": pr["type"],
				"name": httpx.Str(pr, "containerId")}
			next, ok := findFolder(d, httpx.Str(pr, "containerId"))
			if ok {
				entry["name"] = next.Name
			}
			parents = append(parents, entry)
			if !ok {
				break
			}
			cur = next
		}
		v["parents"] = parents
		httpx.WriteJSON(w, 200, v)
	})
	mux.HandleFunc("PATCH "+base+"/{id}", func(w http.ResponseWriter, r *http.Request) {
		b, ok := httpx.Decode(w, r)
		if !ok {
			return
		}
		f, ok := findFolder(d, r.PathValue("id"))
		if !ok {
			notFound(w, r)
			return
		}
		n := httpx.Str(b, "name")
		if n != "" {
			if !nameRe.MatchString(n) {
				httpx.ErrStackit(w, r, 400, "invalid folder name")
				return
			}
			if nameTaken(n, httpx.Str(f.Data, "_parent"), f.ID) {
				httpx.ErrStackit(w, r, 409, "folder with name "+n+" already exists")
				return
			}
		}
		out, _ := d.Store.Update(folderTyp, f.Project, f.ID, func(x *store.Resource) {
			data := crud.DeepMerge(x.Data, nil)
			if n != "" {
				x.Name, data["name"] = n, n
			}
			if l := crud.Map(b["labels"]); l != nil {
				data["labels"] = crud.DeepMerge(crud.Map(data["labels"]), l)
			}
			data["updateTime"] = time.Now().UTC().Format(time.RFC3339)
			x.Data = data
		})
		httpx.WriteJSON(w, 200, folderView(out))
	})
	mux.HandleFunc("DELETE "+base+"/{id}", func(w http.ResponseWriter, r *http.Request) {
		f, ok := findFolder(d, r.PathValue("id"))
		if !ok {
			notFound(w, r)
			return
		}
		for _, t := range []string{typ, folderTyp} {
			for _, c := range d.Store.List(store.Filter{Type: t}) {
				if c.Data["_parent"] == f.ID {
					httpx.ErrStackit(w, r, 409, "folder is not empty: it still contains projects or folders")
					return
				}
			}
		}
		d.Store.Delete(folderTyp, f.Project, f.ID)
		w.WriteHeader(http.StatusNoContent)
	})
}
