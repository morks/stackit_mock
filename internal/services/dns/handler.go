// Package dns mocks the DNS API (v1) under /dns.
//
// Zones and record sets are "tombstoned" on delete: they move to DELETING and
// then DELETE_SUCCEEDED but stay GET-able, because the SDK delete waiter treats
// a 404 as an error. Tombstones are hidden from lists and name uniqueness.
package dns

import (
	"bytes"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/morks/stackit_mock/internal/crud"
	"github.com/morks/stackit_mock/internal/httpx"
	"github.com/morks/stackit_mock/internal/store"
)

const (
	pre = "/dns/v1/projects/{projectId}"
	Z   = pre + "/zones"
	RR  = Z + "/{zoneId}/rrsets"

	typeZone = "dns/zone"
	typeRR   = "dns/recordset"

	stCreating  = "CREATING"
	stCreated   = "CREATE_SUCCEEDED"
	stUpdating  = "UPDATING"
	stUpdated   = "UPDATE_SUCCEEDED"
	stDeleting  = "DELETING"
	stDeleted   = "DELETE_SUCCEEDED"
	defaultTTL  = 3600
	maxPageSize = 1000
)

var recordTypes = []string{"A", "AAAA", "CNAME", "MX", "NS", "TXT", "SRV", "PTR", "SOA", "CAA", "TLSA", "SSHFP", "NAPTR", "DS", "DNSKEY", "HTTPS", "SVCB"}

func wrapAs(key string) func(v any) any {
	return func(v any) any { return map[string]any{key: v, "message": "Success"} }
}

func ts(t time.Time) string {
	if t.IsZero() {
		t = time.Now()
	}
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

func validDNSName(n string) bool {
	n = strings.TrimSuffix(n, ".")
	if n == "" || len(n) > 253 {
		return false
	}
	for _, l := range strings.Split(n, ".") {
		if l == "" || len(l) > 63 {
			return false
		}
		for _, ch := range l {
			if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-' || ch == '_') {
				return false
			}
		}
	}
	return true
}

func num(m map[string]any, k string, def int) {
	if _, ok := m[k]; !ok {
		m[k] = def
	}
}

func Register(mux *http.ServeMux, d *httpx.Deps) {
	scope := func(r *http.Request) crud.Scope { return crud.Scope{Project: r.PathValue("projectId")} }
	rrScope := func(r *http.Request) crud.Scope {
		s := scope(r)
		s.Parent = r.PathValue("zoneId")
		return s
	}

	zones := &crud.Coll{
		D: d, Err: httpx.ErrGeneric, Type: typeZone, Service: "dns", Label: "zone",
		Scope: scope, IDParam: "zoneId", IDKey: "id", NameKey: "dnsName", Unique: true,
		StatusKey: "state", Initial: stCreating, Final: stCreated, Updating: stUpdating, Updated: stUpdated,
		Deleting: stDeleting, Tombstone: stDeleted, DeleteBody: true,
		CreateCode: http.StatusAccepted, WrapFn: wrapAs("zone"),
		Children: []string{typeRR},
		Prepare: func(_ *http.Request, _ crud.Scope, b map[string]any) error {
			name, _ := b["name"].(string)
			dn, _ := b["dnsName"].(string)
			if name == "" || !validDNSName(dn) {
				return fmt.Errorf("name and a valid dnsName are required")
			}
			typ, _ := b["type"].(string)
			switch typ {
			case "":
				b["type"] = "primary"
			case "primary", "secondary":
			default:
				return fmt.Errorf("invalid zone type %q", typ)
			}
			if b["type"] == "secondary" {
				if p, _ := b["primaries"].([]any); len(p) == 0 {
					return fmt.Errorf("primaries are required for secondary zones")
				}
			}
			for _, k := range []string{"state", "visibility", "serialNumber", "primaryNameServer"} {
				delete(b, k)
			}
			b["visibility"] = "public"
			b["primaryNameServer"] = "ns1.stackit.mock."
			if _, ok := b["acl"]; !ok {
				b["acl"] = "0.0.0.0/0,::/0"
			}
			num(b, "defaultTTL", defaultTTL)
			num(b, "expireTime", 1209600)
			num(b, "refreshTime", 3600)
			num(b, "retryTime", 600)
			num(b, "negativeCache", 60)
			b["serialNumber"] = 2022111800
			return nil
		},
		Decorate: func(m map[string]any, res store.Resource) {
			timestamps(m, res)
			m["active"] = res.Status == stCreated || res.Status == stUpdated
			n := 0
			for _, o := range d.Store.List(store.Filter{Type: typeRR, Project: res.Project, Parent: res.ID}) {
				if o.Status != stDeleted {
					n++
				}
			}
			m["recordCount"] = n
		},
	}

	rrsets := &crud.Coll{
		D: d, Err: httpx.ErrGeneric, Type: typeRR, Service: "dns", Label: "record set",
		Scope: rrScope, IDParam: "rrSetId", IDKey: "id",
		StatusKey: "state", Initial: stCreating, Final: stCreated, Updating: stUpdating, Updated: stUpdated,
		Deleting: stDeleting, Tombstone: stDeleted, DeleteBody: true,
		CreateCode: http.StatusAccepted, WrapFn: wrapAs("rrset"),
		Prepare: func(_ *http.Request, s crud.Scope, b map[string]any) error {
			name, _ := b["name"].(string)
			typ, _ := b["type"].(string)
			if name == "" {
				return fmt.Errorf("name is required")
			}
			if !slices.Contains(recordTypes, typ) {
				return fmt.Errorf("invalid record type %q", typ)
			}
			recs, _ := b["records"].([]any)
			if len(recs) == 0 {
				return fmt.Errorf("records are required")
			}
			for _, x := range recs {
				if c, _ := crud.Map(x)["content"].(string); c == "" {
					return fmt.Errorf("record content is required")
				}
			}
			if _, ok := b["ttl"]; !ok {
				ttl := defaultTTL
				if z, ok := d.Store.Get(typeZone, s.Project, s.Parent); ok {
					if f, ok := z.Data["defaultTTL"].(float64); ok {
						ttl = int(f)
					} else if i, ok := z.Data["defaultTTL"].(int); ok {
						ttl = i
					}
				}
				b["ttl"] = ttl
			}
			return nil
		},
		Decorate: func(m map[string]any, res store.Resource) {
			timestamps(m, res)
			m["active"] = res.Status == stCreated || res.Status == stUpdated
			recs, _ := m["records"].([]any)
			out := make([]any, 0, len(recs))
			for _, x := range recs {
				r := map[string]any{}
				for k, v := range crud.Map(x) {
					r[k] = v
				}
				if _, ok := r["id"]; !ok {
					h := fnv.New64a()
					_, _ = h.Write([]byte(res.ID + fmt.Sprint(r["content"])))
					r["id"] = fmt.Sprintf("%016x", h.Sum64())
				}
				out = append(out, r)
			}
			m["records"] = out
		},
	}

	mux.HandleFunc("POST "+Z, zones.Create)
	mux.HandleFunc("GET "+Z, list(zones, scope, "zones"))
	mux.HandleFunc("GET "+Z+"/{zoneId}", zones.Get)
	mux.HandleFunc("PATCH "+Z+"/{zoneId}", zones.Patch)
	mux.HandleFunc("DELETE "+Z+"/{zoneId}", zones.Delete)

	mux.HandleFunc("POST "+RR, func(w http.ResponseWriter, r *http.Request) {
		s := rrScope(r)
		z, ok := d.Store.Get(typeZone, s.Project, s.Parent)
		if !ok || zones.Hidden(z) {
			httpx.ErrGeneric(w, r, http.StatusNotFound, "zone not found")
			return
		}
		raw, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(raw))
		b, ok := httpx.Decode(w, r)
		if !ok {
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		name, _ := b["name"].(string)
		typ, _ := b["type"].(string)
		for _, o := range d.Store.List(store.Filter{Type: typeRR, Project: s.Project, Parent: s.Parent}) {
			if !rrsets.Hidden(o) && o.Name == name && o.Data["type"] == typ {
				httpx.ErrGeneric(w, r, http.StatusConflict, "record set "+name+" "+typ+" already exists")
				return
			}
		}
		rrsets.Create(w, r)
	})
	mux.HandleFunc("GET "+RR, list(rrsets, rrScope, "rrSets"))
	mux.HandleFunc("GET "+RR+"/{rrSetId}", rrsets.Get)
	mux.HandleFunc("PATCH "+RR+"/{rrSetId}", rrsets.Patch)
	mux.HandleFunc("DELETE "+RR+"/{rrSetId}", rrsets.Delete)
}

func timestamps(m map[string]any, res store.Resource) {
	m["creationStarted"], m["creationFinished"] = ts(res.Created), ts(res.Created)
	m["updateStarted"], m["updateFinished"] = ts(res.Updated), ts(res.Updated)
}

// list serves GET collections with the SDK's *Eq filters and page/pageSize paging.
func list(c *crud.Coll, scope func(*http.Request) crud.Scope, key string) http.HandlerFunc {
	filters := map[string]string{"nameEq": "name", "dnsNameEq": "dnsName", "typeEq": "type", "stateEq": "state"}
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		s := scope(r)
		var items []map[string]any
	next:
		for _, res := range c.D.Store.List(store.Filter{Type: c.Type, Project: s.Project, Parent: s.Parent}) {
			if c.Hidden(res) {
				continue
			}
			v := c.View(res)
			for p, field := range filters {
				if want := q.Get(p); want != "" && fmt.Sprint(v[field]) != want {
					continue next
				}
			}
			items = append(items, v)
		}
		sort.Slice(items, func(i, j int) bool {
			return fmt.Sprint(items[i]["name"], items[i]["type"], items[i]["id"]) < fmt.Sprint(items[j]["name"], items[j]["type"], items[j]["id"])
		})
		page, _ := strconv.Atoi(q.Get("page"))
		size, _ := strconv.Atoi(q.Get("pageSize"))
		if page < 1 {
			page = 1
		}
		if size < 1 || size > maxPageSize {
			size = 100
		}
		total := len(items)
		lo := min((page-1)*size, total)
		hi := min(lo+size, total)
		out := make([]any, 0, hi-lo)
		for _, it := range items[lo:hi] {
			out = append(out, it)
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			key: out, "totalItems": total, "totalPages": (total + size - 1) / size, "itemsPerPage": size,
			"message": "Success",
		})
	}
}
