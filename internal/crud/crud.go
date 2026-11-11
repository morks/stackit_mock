// Package crud implements a generic REST collection on top of the store and
// lifecycle engine. Each STACKIT resource type is a configured Coll.
package crud

import (
	"net/http"
	"strings"
	"time"

	"github.com/morks/stackit_mock/internal/httpx"
	"github.com/morks/stackit_mock/internal/store"
)

type Scope struct{ Project, Region, Parent string }

type Coll struct {
	D       *httpx.Deps
	Err     httpx.ErrFunc
	Type    string // e.g. "iaas/network"
	Service string
	Label   string // used in 404 messages

	Scope   func(r *http.Request) Scope
	IDParam string // path wildcard holding the store key (Get/Patch/Delete)

	IDKey   string // JSON key receiving the generated id ("" = none)
	IDNum   bool   // generated id is a JSON number (int64)
	KeyKey  string // JSON key whose value is the store key (default IDKey)
	NameKey string // JSON key holding the display name (default "name")
	Unique  bool   // 409 if name already exists in project/parent
	Stamp   bool   // maintain createdAt/updatedAt

	StatusKey string // JSON key where the lifecycle status is exposed
	Initial   string // status after create
	Final     string // status after delay
	Updating  string // intermediate status on update ("" = none)
	Deleting  string // status while deleting ("" = immediate delete)

	ListKey    string // default "items"
	Wrap       string // wrap single objects: {Wrap: obj}
	CreateCode int    // default 201

	Prepare    func(r *http.Request, s Scope, body map[string]any) error // fills defaults; error -> 400
	Decorate   func(m map[string]any, res store.Resource)                // adjusts the public view
	CreateResp func(raw map[string]any) any                              // response of POST (raw incl. "_" keys)
	ListWrap   func(items []any) any
	BeforeDel  func(r *http.Request, res store.Resource) (int, string) // non-zero code blocks deletion
	Children   []string                                                // child types removed with the parent
}

func (c *Coll) listKey() string {
	if c.ListKey == "" {
		return "items"
	}
	return c.ListKey
}

func (c *Coll) nameKey() string {
	if c.NameKey == "" {
		return "name"
	}
	return c.NameKey
}

func (c *Coll) keyKey() string {
	if c.KeyKey != "" {
		return c.KeyKey
	}
	return c.IDKey
}

func (c *Coll) scope(r *http.Request) Scope {
	if c.Scope == nil {
		return Scope{}
	}
	return c.Scope(r)
}

func (c *Coll) notFound(w http.ResponseWriter, r *http.Request) {
	c.Err(w, r, http.StatusNotFound, c.Label+" not found")
}

// View is the public JSON representation of a resource.
func (c *Coll) View(res store.Resource) map[string]any {
	m := make(map[string]any, len(res.Data)+2)
	for k, v := range res.Data {
		if !strings.HasPrefix(k, "_") {
			m[k] = v
		}
	}
	if c.StatusKey != "" {
		m[c.StatusKey] = res.Status
	}
	if c.Decorate != nil {
		c.Decorate(m, res)
	}
	return m
}

func (c *Coll) wrap(v any) any {
	if c.Wrap != "" {
		return map[string]any{c.Wrap: v}
	}
	return v
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }

// Build creates the Resource for body (used by Create and Upsert).
func (c *Coll) build(r *http.Request, s Scope, body map[string]any) (store.Resource, error) {
	if c.IDKey != "" {
		if _, ok := body[c.IDKey]; !ok || c.IDKey == "id" {
			if c.IDNum {
				body[c.IDKey] = NumID()
			} else {
				body[c.IDKey] = httpx.UUID()
			}
		}
	}
	if c.Stamp {
		body["createdAt"], body["updatedAt"] = now(), now()
	}
	if c.Prepare != nil {
		if err := c.Prepare(r, s, body); err != nil {
			return store.Resource{}, err
		}
	}
	key := KeyString(body[c.keyKey()])
	name, _ := body[c.nameKey()].(string)
	return store.Resource{Type: c.Type, Service: c.Service, ID: key, Project: s.Project,
		Region: s.Region, Parent: s.Parent, Name: name, Data: body}, nil
}

func (c *Coll) Create(w http.ResponseWriter, r *http.Request) {
	body, ok := httpx.Decode(w, r)
	if !ok {
		return
	}
	s := c.scope(r)
	res, err := c.build(r, s, body)
	if err != nil {
		c.Err(w, r, http.StatusBadRequest, err.Error())
		return
	}
	if res.ID == "" {
		c.Err(w, r, http.StatusBadRequest, "missing "+c.keyKey())
		return
	}
	if c.Unique && res.Name != "" && c.nameTaken(res, "") {
		c.Err(w, r, http.StatusConflict, c.Label+" with name "+res.Name+" already exists")
		return
	}
	out, err := c.D.Engine.Create(res, c.Initial, c.Final)
	if err != nil {
		c.Err(w, r, http.StatusConflict, c.Label+" already exists")
		return
	}
	code := c.CreateCode
	if code == 0 {
		code = http.StatusCreated
	}
	if c.CreateResp != nil {
		raw := map[string]any{}
		for k, v := range out.Data {
			raw[k] = v
		}
		if c.StatusKey != "" {
			raw[c.StatusKey] = out.Status
		}
		httpx.WriteJSON(w, code, c.CreateResp(raw))
		return
	}
	httpx.WriteJSON(w, code, c.wrap(c.View(out)))
}

// Upsert implements PUT-creates-or-updates (SKE clusters etc.).
func (c *Coll) Upsert(w http.ResponseWriter, r *http.Request) {
	body, ok := httpx.Decode(w, r)
	if !ok {
		return
	}
	s := c.scope(r)
	key := r.PathValue(c.IDParam)
	if cur, exists := c.D.Store.Get(c.Type, s.Project, key); exists {
		c.D.Store.Update(c.Type, s.Project, key, func(x *store.Resource) {
			for k, v := range body {
				x.Data[k] = v
			}
			if c.Stamp {
				x.Data["updatedAt"] = now()
			}
		})
		if c.Updating != "" && cur.Status == c.Final {
			c.D.Engine.Transition(c.Type, s.Project, key, c.Updating, c.Final)
		}
		out, _ := c.D.Store.Get(c.Type, s.Project, key)
		httpx.WriteJSON(w, http.StatusOK, c.wrap(c.View(out)))
		return
	}
	body[c.keyKey()] = key
	res, err := c.build(r, s, body)
	if err != nil {
		c.Err(w, r, http.StatusBadRequest, err.Error())
		return
	}
	res.ID = key
	out, err := c.D.Engine.Create(res, c.Initial, c.Final)
	if err != nil {
		c.Err(w, r, http.StatusConflict, c.Label+" already exists")
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, c.wrap(c.View(out)))
}

func (c *Coll) Get(w http.ResponseWriter, r *http.Request) {
	res, ok := c.D.Store.Get(c.Type, c.scope(r).Project, r.PathValue(c.IDParam))
	if !ok {
		c.notFound(w, r)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, c.wrap(c.View(res)))
}

func (c *Coll) List(w http.ResponseWriter, r *http.Request) {
	s := c.scope(r)
	items := []any{}
	for _, res := range c.D.Store.List(store.Filter{Type: c.Type, Project: s.Project, Parent: s.Parent}) {
		items = append(items, c.View(res))
	}
	if c.ListWrap != nil {
		httpx.WriteJSON(w, http.StatusOK, c.ListWrap(items))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{c.listKey(): items})
}

// Patch deep-merges the body into the stored object.
func (c *Coll) Patch(w http.ResponseWriter, r *http.Request) {
	body, ok := httpx.Decode(w, r)
	if !ok {
		return
	}
	s := c.scope(r)
	key := r.PathValue(c.IDParam)
	cur, exists := c.D.Store.Get(c.Type, s.Project, key)
	if !exists {
		c.notFound(w, r)
		return
	}
	if n, _ := body[c.nameKey()].(string); c.Unique && n != "" && n != cur.Name && c.nameTaken(cur, n) {
		c.Err(w, r, http.StatusConflict, c.Label+" with name "+n+" already exists")
		return
	}
	out, _ := c.D.Store.Update(c.Type, s.Project, key, func(x *store.Resource) {
		merged := DeepMerge(x.Data, body)
		if c.Stamp {
			merged["updatedAt"] = now()
		}
		x.Data = merged
		if n, ok := merged[c.nameKey()].(string); ok {
			x.Name = n
		}
	})
	if c.Updating != "" && cur.Status == c.Final {
		out, _ = c.D.Engine.Transition(c.Type, s.Project, key, c.Updating, c.Final)
	}
	httpx.WriteJSON(w, http.StatusOK, c.wrap(c.View(out)))
}

// Delete is idempotent in effect: a second call yields 404 like the real APIs.
func (c *Coll) Delete(w http.ResponseWriter, r *http.Request) {
	s := c.scope(r)
	key := r.PathValue(c.IDParam)
	res, ok := c.D.Store.Get(c.Type, s.Project, key)
	if !ok {
		c.notFound(w, r)
		return
	}
	if c.BeforeDel != nil {
		if code, msg := c.BeforeDel(r, res); code != 0 {
			c.Err(w, r, code, msg)
			return
		}
	}
	for _, ct := range c.Children {
		for _, ch := range c.D.Store.List(store.Filter{Type: ct, Project: s.Project, Parent: key}) {
			c.D.Store.Delete(ch.Type, ch.Project, ch.ID)
		}
	}
	if c.Deleting == "" {
		c.D.Store.Delete(c.Type, s.Project, key)
	} else {
		c.D.Engine.Delete(c.Type, s.Project, key, c.Deleting)
	}
	w.WriteHeader(http.StatusAccepted)
}

func (c *Coll) nameTaken(res store.Resource, newName string) bool {
	n := res.Name
	if newName != "" {
		n = newName
	}
	for _, o := range c.D.Store.List(store.Filter{Type: c.Type, Project: res.Project, Parent: res.Parent}) {
		if o.Name == n && o.ID != res.ID {
			return true
		}
	}
	return false
}

// Routes registers the standard routes below base (e.g. "/iaas/v2/.../networks").
// Pattern wildcard for the id must be {IDParam}.
func (c *Coll) Routes(mux *http.ServeMux, base string) {
	mux.HandleFunc("POST "+base, c.Create)
	mux.HandleFunc("GET "+base, c.List)
	mux.HandleFunc("GET "+base+"/{"+c.IDParam+"}", c.Get)
	mux.HandleFunc("PATCH "+base+"/{"+c.IDParam+"}", c.Patch)
	mux.HandleFunc("DELETE "+base+"/{"+c.IDParam+"}", c.Delete)
}
