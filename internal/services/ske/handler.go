// Package ske mocks the SKE API (v2) under /ske and Service Enablement under /serviceenablement.
package ske

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/morks/stackit_mock/internal/crud"
	"github.com/morks/stackit_mock/internal/httpx"
	"github.com/morks/stackit_mock/internal/store"
)

const (
	pre = "/ske/v2"
	C   = pre + "/projects/{projectId}/regions/{region}/clusters"
)

func scope(r *http.Request) crud.Scope {
	return crud.Scope{Project: r.PathValue("projectId"), Region: r.PathValue("region")}
}

func Register(mux *http.ServeMux, d *httpx.Deps) {
	clusters := &crud.Coll{
		D: d, Err: httpx.ErrGeneric, Type: "ske/cluster", Service: "ske", Label: "cluster",
		IDParam: "clusterName", KeyKey: "name", Scope: scope,
		Initial: "STATE_CREATING", Final: "STATE_HEALTHY", Updating: "STATE_RECONCILING", Deleting: "STATE_DELETING",
		Prepare: func(r *http.Request, s crud.Scope, b map[string]any) error {
			if crud.Map(b["kubernetes"]) == nil || httpx.Str(crud.Map(b["kubernetes"]), "version") == "" {
				return errors.New("kubernetes.version is required")
			}
			nps, _ := b["nodepools"].([]any)
			if len(nps) == 0 {
				return errors.New("at least one nodepool is required")
			}
			for _, np := range nps {
				m := crud.Map(np)
				if m == nil || httpx.Str(m, "name") == "" || crud.Map(m["machine"]) == nil {
					return errors.New("nodepool needs name and machine")
				}
				for k, v := range map[string]any{"maxSurge": 1, "maxUnavailable": 0, "allowSystemComponents": true,
					"cri": map[string]any{"name": "containerd"}} {
					if _, ok := m[k]; !ok {
						m[k] = v
					}
				}
			}
			if _, ok := b["maintenance"]; !ok {
				b["maintenance"] = map[string]any{
					"autoUpdate": map[string]any{"kubernetesVersion": true, "machineImageVersion": true},
					"timeWindow": map[string]any{"start": "0001-01-01T01:00:00Z", "end": "0001-01-01T02:00:00Z"}}
			}
			// the provider dereferences access.idp without nil checks
			acc := crud.Map(b["access"])
			if acc == nil {
				acc = map[string]any{}
				b["access"] = acc
			}
			if crud.Map(acc["idp"]) == nil {
				acc["idp"] = map[string]any{"enabled": false, "type": "stackit"}
			}
			b["_created"] = time.Now().UTC().Format(time.RFC3339)
			return nil
		},
		Decorate: func(m map[string]any, res store.Resource) {
			n := fmt.Sprint(res.Data["name"])
			m["status"] = map[string]any{
				"aggregated": res.Status, "creationTime": res.Data["_created"],
				"credentialsRotation":  map[string]any{"phase": "NEVER"},
				"egressAddressRanges":  []any{"193.148.168.0/24"},
				"podAddressRanges":     []any{"100.64.0.0/13"},
				"serviceAddressRanges": []any{"100.72.0.0/13"},
				"serviceAccountIssuer": "https://" + n + ".ske.mock.local",
			}
		},
	}
	mux.HandleFunc("PUT "+C+"/{clusterName}", clusters.Upsert)
	mux.HandleFunc("GET "+C+"/{clusterName}", clusters.Get)
	mux.HandleFunc("GET "+C, clusters.List)
	mux.HandleFunc("DELETE "+C+"/{clusterName}", func(w http.ResponseWriter, r *http.Request) {
		clusters.Delete(w, r)
	})
	for _, act := range []string{"hibernate", "wakeup", "reconcile", "maintenance"} {
		mux.HandleFunc("POST "+C+"/{clusterName}/"+act, func(w http.ResponseWriter, r *http.Request) {
			s := scope(r)
			if _, ok := d.Engine.Transition("ske/cluster", s.Project, r.PathValue("clusterName"), "STATE_RECONCILING", "STATE_HEALTHY"); !ok {
				httpx.ErrGeneric(w, r, 404, "cluster not found")
				return
			}
			w.WriteHeader(http.StatusAccepted)
		})
	}
	kubeconfig := func(w http.ResponseWriter, r *http.Request) {
		s := scope(r)
		name := r.PathValue("clusterName")
		if _, ok := d.Store.Get("ske/cluster", s.Project, name); !ok {
			httpx.ErrGeneric(w, r, 404, "cluster not found")
			return
		}
		exp := time.Now().Add(time.Hour).UTC()
		httpx.WriteJSON(w, 200, map[string]any{
			"kubeconfig":          kubeconfigYAML(name),
			"expirationTimestamp": exp.Format(time.RFC3339),
		})
	}
	mux.HandleFunc("POST "+C+"/{clusterName}/kubeconfig", kubeconfig)
	mux.HandleFunc("GET "+C+"/{clusterName}/kubeconfig/login", kubeconfig)
	mux.HandleFunc("GET "+C+"/{clusterName}/kubeconfig/idp", kubeconfig)

	mux.HandleFunc("GET "+pre+"/regions/{region}/provider-options", func(w http.ResponseWriter, r *http.Request) {
		exp := time.Now().AddDate(1, 0, 0).UTC().Format(time.RFC3339)
		httpx.WriteJSON(w, 200, map[string]any{
			"kubernetesVersions": []any{
				map[string]any{"version": "1.34.1", "state": "supported", "expirationDate": exp, "featureGates": map[string]any{}},
				map[string]any{"version": "1.33.5", "state": "supported", "expirationDate": exp, "featureGates": map[string]any{}},
				map[string]any{"version": "1.31.13", "state": "deprecated", "expirationDate": exp, "featureGates": map[string]any{}},
			},
			"machineImages": []any{
				map[string]any{"name": "flatcar", "versions": []any{map[string]any{"version": "4230.2.1", "state": "supported",
					"expirationDate": exp, "cri": []any{map[string]any{"name": "containerd"}}}}},
				map[string]any{"name": "ubuntu", "versions": []any{map[string]any{"version": "24.04", "state": "supported",
					"expirationDate": exp, "cri": []any{map[string]any{"name": "containerd"}}}}},
			},
			"machineTypes": []any{
				map[string]any{"name": "g2i.2", "cpu": 2, "memory": 8, "gpu": 0, "architecture": "amd64"},
				map[string]any{"name": "g2i.4", "cpu": 4, "memory": 16, "gpu": 0, "architecture": "amd64"},
			},
			"availabilityZones": []any{map[string]any{"name": "eu01-1"}, map[string]any{"name": "eu01-2"}, map[string]any{"name": "eu01-3"}},
			"volumeTypes":       []any{map[string]any{"name": "storage_premium_perf1"}},
		})
	})

	registerEnablement(mux, d)
}

func kubeconfigYAML(name string) string {
	return fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- name: %[1]s
  cluster:
    server: https://%[1]s.ske.mock.local:6443
    insecure-skip-tls-verify: true
users:
- name: %[1]s-admin
  user:
    token: mock-token
contexts:
- name: %[1]s
  context:
    cluster: %[1]s
    user: %[1]s-admin
current-context: %[1]s
`, name)
}

// registerEnablement mocks Service Enablement (prerequisite of SKE).
func registerEnablement(mux *http.ServeMux, d *httpx.Deps) {
	const p = "/serviceenablement/v2/projects/{projectId}/regions/{region}/services/{serviceId}"
	typ := "serviceenablement/service"
	view := func(res store.Resource) map[string]any {
		return map[string]any{"serviceId": res.Data["serviceId"], "state": res.Status}
	}
	mux.HandleFunc("POST "+p, func(w http.ResponseWriter, r *http.Request) {
		s, id := scope(r), r.PathValue("serviceId")
		if cur, ok := d.Store.Get(typ, s.Project, id); ok {
			httpx.WriteJSON(w, 202, view(cur))
			return
		}
		out, _ := d.Engine.Create(store.Resource{Type: typ, Service: "serviceenablement", ID: id, Project: s.Project,
			Region: s.Region, Name: id, Data: map[string]any{"serviceId": id}}, "ENABLING", "ENABLED")
		httpx.WriteJSON(w, 202, view(out))
	})
	mux.HandleFunc("GET "+p, func(w http.ResponseWriter, r *http.Request) {
		s, id := scope(r), r.PathValue("serviceId")
		if cur, ok := d.Store.Get(typ, s.Project, id); ok {
			httpx.WriteJSON(w, 200, view(cur))
			return
		}
		httpx.WriteJSON(w, 200, map[string]any{"serviceId": id, "state": "DISABLED"})
	})
	mux.HandleFunc("DELETE "+p, func(w http.ResponseWriter, r *http.Request) {
		s := scope(r)
		d.Store.Delete(typ, s.Project, r.PathValue("serviceId"))
		w.WriteHeader(http.StatusAccepted)
	})
}
