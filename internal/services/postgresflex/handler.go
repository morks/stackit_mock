// Package postgresflex mocks PostgreSQL Flex (v3) under /postgresflex.
package postgresflex

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/morks/stackit_mock/internal/crud"
	"github.com/morks/stackit_mock/internal/httpx"
)

const P = "/postgresflex/v3/projects/{projectId}/regions/{region}"

func pagination(n int) map[string]any {
	return map[string]any{"page": 1, "size": max(n, 1), "sort": "id.asc", "totalPages": 1, "totalRows": n}
}

type flavor = map[string]any

var flavors = func() []flavor {
	var out []flavor
	for _, cpu := range []int{2, 4, 8, 16} {
		for _, nt := range []string{"Single", "Replica"} {
			out = append(out, flavor{
				"id": fmt.Sprintf("%d.%d-%s", cpu, cpu*4, nt), "cpu": cpu, "memory": cpu * 4,
				"description": fmt.Sprintf("%d vCPU, %d GB, %s", cpu, cpu*4, nt), "nodeType": nt,
				"minGB": 10, "maxGB": 500,
				"storageClasses": []any{map[string]any{"class": "premium-perf6-stackit", "maxIoPerSec": 90000, "maxThroughInMb": 600}},
			})
		}
	}
	return out
}()

func Register(mux *http.ServeMux, d *httpx.Deps) {
	scope := func(r *http.Request) crud.Scope {
		return crud.Scope{Project: r.PathValue("projectId"), Region: r.PathValue("region")}
	}
	childScope := func(r *http.Request) crud.Scope {
		s := scope(r)
		s.Parent = r.PathValue("instanceId")
		return s
	}
	instances := &crud.Coll{
		D: d, Err: httpx.ErrFlex, Type: "postgresflex/instance", Service: "postgresflex", Label: "instance",
		IDParam: "instanceId", IDKey: "id", Scope: scope, Unique: true,
		StatusKey: "state", Initial: "PENDING", Final: "READY", Updating: "PROGRESSING", Deleting: "TERMINATING",
		Children: []string{"postgresflex/user", "postgresflex/database"},
		Prepare: func(r *http.Request, s crud.Scope, b map[string]any) error {
			if httpx.Str(b, "name") == "" || httpx.Str(b, "version") == "" {
				return errors.New("name and version are required")
			}
			known := false
			for _, f := range flavors {
				known = known || f["id"] == httpx.Str(b, "flavorId")
			}
			if !known {
				return errors.New("unknown flavorId " + httpx.Str(b, "flavorId"))
			}
			nw := crud.Map(b["network"])
			if nw == nil {
				nw = map[string]any{}
				b["network"] = nw
			}
			if _, ok := nw["acl"]; !ok {
				nw["acl"] = []any{}
			}
			if _, ok := nw["accessScope"]; !ok {
				nw["accessScope"] = "PUBLIC"
			}
			id := fmt.Sprint(b["id"])
			nw["instanceAddress"] = "pg-" + id[:8] + ".postgresflex.mock.local"
			nw["routerAddress"] = "router-" + id[:8] + ".postgresflex.mock.local"
			b["connectionInfo"] = map[string]any{"write": map[string]any{"host": nw["instanceAddress"], "port": 5432}}
			b["isDeletable"] = true
			if b["storage"] == nil {
				b["storage"] = map[string]any{"class": "premium-perf6-stackit", "size": 20}
			}
			return nil
		},
		CreateResp: func(raw map[string]any) any { return map[string]any{"id": raw["id"]} },
		ListWrap:   nil,
	}
	instances.ListWrap = func(items []any) any {
		out := []any{}
		for _, it := range items {
			m := it.(map[string]any)
			out = append(out, map[string]any{"id": m["id"], "name": m["name"], "state": m["state"], "isDeletable": true})
		}
		return map[string]any{"instances": out, "pagination": pagination(len(out))}
	}
	instances.Routes(mux, P+"/instances")
	mux.HandleFunc("PUT "+P+"/instances/{instanceId}", instances.Patch)

	needInstance := func(r *http.Request, s crud.Scope) error {
		if _, ok := d.Store.Get("postgresflex/instance", s.Project, s.Parent); !ok {
			return errors.New("instance not found")
		}
		return nil
	}
	users := &crud.Coll{
		D: d, Err: httpx.ErrFlex, Type: "postgresflex/user", Service: "postgresflex", Label: "user",
		IDParam: "userId", IDKey: "id", IDNum: true, Scope: childScope, ListKey: "users",
		StatusKey: "state", Initial: "PENDING", Final: "PROCESSED",
		Prepare: func(r *http.Request, s crud.Scope, b map[string]any) error {
			if err := needInstance(r, s); err != nil {
				return err
			}
			if httpx.Str(b, "name") == "" {
				return errors.New("name is required")
			}
			if _, ok := b["roles"]; !ok {
				b["roles"] = []any{"login"}
			}
			b["_password"] = "pw-" + crud.Rand(24)
			return nil
		},
		CreateResp: func(raw map[string]any) any {
			return map[string]any{"id": raw["id"], "name": raw["name"], "password": raw["_password"], "state": raw["state"]}
		},
	}
	users.ListWrap = func(items []any) any {
		return map[string]any{"users": items, "pagination": pagination(len(items))}
	}
	users.Routes(mux, P+"/instances/{instanceId}/users")

	dbs := &crud.Coll{
		D: d, Err: httpx.ErrFlex, Type: "postgresflex/database", Service: "postgresflex", Label: "database",
		IDParam: "databaseId", IDKey: "id", IDNum: true, Scope: childScope,
		Prepare: func(r *http.Request, s crud.Scope, b map[string]any) error {
			if err := needInstance(r, s); err != nil {
				return err
			}
			if httpx.Str(b, "name") == "" {
				return errors.New("name is required")
			}
			if httpx.Str(b, "owner") == "" {
				b["owner"] = "postgres"
			}
			return nil
		},
		CreateResp: func(raw map[string]any) any { return map[string]any{"id": raw["id"]} },
	}
	dbs.ListWrap = func(items []any) any {
		return map[string]any{"databases": items, "pagination": pagination(len(items))}
	}
	dbs.Routes(mux, P+"/instances/{instanceId}/databases")

	mux.HandleFunc("GET "+P+"/flavors", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, 200, map[string]any{"flavors": flavors, "pagination": pagination(len(flavors))})
	})
	mux.HandleFunc("GET "+P+"/versions", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, 200, map[string]any{"versions": []any{
			map[string]any{"version": "17", "beta": false, "deprecated": false, "recommend": true},
			map[string]any{"version": "16", "beta": false, "deprecated": false, "recommend": false},
			map[string]any{"version": "15", "beta": false, "deprecated": true, "recommend": false},
		}})
	})
}
