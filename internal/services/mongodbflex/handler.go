// Package mongodbflex mocks MongoDB Flex (v2) under /mongodbflex.
package mongodbflex

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/morks/stackit_mock/internal/crud"
	"github.com/morks/stackit_mock/internal/httpx"
	"github.com/morks/stackit_mock/internal/store"
)

const M = "/mongodbflex/v2/projects/{projectId}/regions/{region}"

var flavors = func() []any {
	var out []any
	for _, cpu := range []int{1, 2, 4, 8} {
		out = append(out, map[string]any{"id": fmt.Sprintf("%d.%d", cpu, cpu*4), "cpu": cpu, "memory": cpu * 4,
			"description": fmt.Sprintf("%d vCPU, %d GB", cpu, cpu*4), "categories": []any{}})
	}
	return out
}()

// option defaults must be numeric strings; the provider ParseInts them.
var optionDefaults = map[string]any{
	"snapshotRetentionDays": "3", "dailySnapshotRetentionDays": "0", "weeklySnapshotRetentionWeeks": "3",
	"monthlySnapshotRetentionMonths": "1", "pointInTimeWindowHours": "2",
}

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
		D: d, Err: httpx.ErrFlex, Type: "mongodbflex/instance", Service: "mongodbflex", Label: "instance",
		IDParam: "instanceId", IDKey: "id", Scope: scope, Unique: true, Wrap: "item",
		StatusKey: "status", Initial: "PENDING", Final: "READY", Updating: "PROCESSING", Deleting: "PROCESSING",
		Children: []string{"mongodbflex/user"},
		Prepare: func(r *http.Request, s crud.Scope, b map[string]any) error {
			if httpx.Str(b, "name") == "" || httpx.Str(b, "version") == "" {
				return errors.New("name and version are required")
			}
			var fl any
			for _, f := range flavors {
				if f.(map[string]any)["id"] == httpx.Str(b, "flavorId") {
					fl = f
				}
			}
			if fl == nil {
				return errors.New("unknown flavorId " + httpx.Str(b, "flavorId"))
			}
			b["flavor"] = fl
			delete(b, "flavorId")
			replicas := 1
			if f, ok := b["replicas"].(float64); ok {
				replicas = int(f)
			}
			b["replicas"] = replicas
			if b["acl"] == nil {
				b["acl"] = map[string]any{"items": []any{}}
			}
			if b["storage"] == nil {
				b["storage"] = map[string]any{"class": "premium-perf2-mongodb", "size": 10}
			}
			opts := map[string]any{}
			for k, v := range optionDefaults {
				opts[k] = v
			}
			for k, v := range crud.Map(b["options"]) {
				opts[k] = v
			}
			if _, ok := opts["type"]; !ok {
				opts["type"] = map[int]string{1: "Single", 3: "Replica", 9: "Sharded"}[replicas]
			}
			b["options"] = opts
			if httpx.Str(b, "backupSchedule") == "" {
				b["backupSchedule"] = "0 0 * * *"
			}
			return nil
		},
		CreateResp: func(raw map[string]any) any { return map[string]any{"id": raw["id"]} },
	}
	instances.ListWrap = func(items []any) any {
		out := []any{}
		for _, it := range items {
			m := it.(map[string]any)
			out = append(out, map[string]any{"id": m["id"], "name": m["name"], "status": m["status"]})
		}
		return map[string]any{"items": out}
	}
	mux.HandleFunc("POST "+M+"/instances", instances.Create)
	mux.HandleFunc("GET "+M+"/instances", instances.List)
	mux.HandleFunc("GET "+M+"/instances/{instanceId}", instances.Get)
	mux.HandleFunc("PATCH "+M+"/instances/{instanceId}", instances.Patch)
	mux.HandleFunc("PUT "+M+"/instances/{instanceId}", instances.Patch)
	mux.HandleFunc("DELETE "+M+"/instances/{instanceId}", instances.Delete)

	// backup schedule: the provider dereferences every numeric field of the response.
	mux.HandleFunc("PUT "+M+"/instances/{instanceId}/backups", func(w http.ResponseWriter, r *http.Request) {
		b, ok := httpx.Decode(w, r)
		if !ok {
			return
		}
		s := scope(r)
		id := r.PathValue("instanceId")
		resp := map[string]any{}
		_, found := d.Store.Update("mongodbflex/instance", s.Project, id, func(x *store.Resource) {
			opts := crud.Map(x.Data["options"])
			if opts == nil {
				opts = map[string]any{}
				x.Data["options"] = opts
			}
			if bs := httpx.Str(b, "backupSchedule"); bs != "" {
				x.Data["backupSchedule"] = bs
			}
			resp["backupSchedule"] = x.Data["backupSchedule"]
			for k := range optionDefaults {
				if v, ok := b[k].(float64); ok {
					opts[k] = fmt.Sprint(int64(v))
				}
				var n int64
				_, _ = fmt.Sscan(fmt.Sprint(opts[k]), &n)
				resp[k] = n
			}
		})
		if !found {
			httpx.ErrFlex(w, r, 404, "instance not found")
			return
		}
		httpx.WriteJSON(w, 200, resp)
	})

	users := &crud.Coll{
		D: d, Err: httpx.ErrFlex, Type: "mongodbflex/user", Service: "mongodbflex", Label: "user",
		IDParam: "userId", IDKey: "id", Scope: childScope, Wrap: "item", NameKey: "username",
		Prepare: func(r *http.Request, s crud.Scope, b map[string]any) error {
			if _, ok := d.Store.Get("mongodbflex/instance", s.Project, s.Parent); !ok {
				return errors.New("instance not found")
			}
			if httpx.Str(b, "database") == "" {
				return errors.New("database is required")
			}
			if _, ok := b["roles"]; !ok {
				return errors.New("roles are required")
			}
			if httpx.Str(b, "username") == "" {
				b["username"] = "user-" + crud.Rand(6)
			}
			host := "mongo-" + s.Parent[:8] + ".mongodbflex.mock.local"
			b["host"], b["port"] = host, 27017
			pw := "pw-" + crud.Rand(24)
			b["_password"] = pw
			b["_uri"] = fmt.Sprintf("mongodb://%s:%s@%s:27017/%s", b["username"], pw, host, b["database"])
			return nil
		},
		CreateResp: func(raw map[string]any) any {
			item := map[string]any{}
			for k, v := range raw {
				item[k] = v
			}
			item["password"], item["uri"] = raw["_password"], raw["_uri"]
			delete(item, "_password")
			delete(item, "_uri")
			return map[string]any{"item": item}
		},
	}
	ub := M + "/instances/{instanceId}/users"
	mux.HandleFunc("POST "+ub, users.Create)
	mux.HandleFunc("GET "+ub, users.List)
	mux.HandleFunc("GET "+ub+"/{userId}", users.Get)
	mux.HandleFunc("PUT "+ub+"/{userId}", users.Patch)
	mux.HandleFunc("PATCH "+ub+"/{userId}", users.Patch)
	mux.HandleFunc("DELETE "+ub+"/{userId}", users.Delete)

	mux.HandleFunc("GET "+M+"/flavors", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, 200, map[string]any{"flavors": flavors})
	})
	mux.HandleFunc("GET "+M+"/versions", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, 200, map[string]any{"versions": []string{"6.0", "7.0", "8.0"}})
	})
}
