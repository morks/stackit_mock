// Package serviceaccount mocks the Service Account API (v2) under /serviceaccount.
package serviceaccount

import (
	"net/http"
	"strings"
	"time"

	"github.com/morks/stackit_mock/internal/crud"
	"github.com/morks/stackit_mock/internal/httpx"
	"github.com/morks/stackit_mock/internal/store"
)

const base = "/serviceaccount/v2/projects/{projectId}/service-accounts"

func Register(mux *http.ServeMux, d *httpx.Deps) {
	accounts := &crud.Coll{
		D: d, Err: httpx.ErrStackit, Type: "serviceaccount/account", Service: "serviceaccount",
		Label: "service account", IDParam: "serviceAccountEmail", IDKey: "id", KeyKey: "email",
		Scope:    func(r *http.Request) crud.Scope { return crud.Scope{Project: r.PathValue("projectId")} },
		Children: []string{"serviceaccount/key"},
		Prepare: func(r *http.Request, s crud.Scope, b map[string]any) error {
			name := httpx.Str(b, "name")
			if name == "" {
				return errMsg("name is required")
			}
			b["email"] = strings.ToLower(name) + "-" + crud.Rand(6) + "@sa.stackit.cloud"
			b["projectId"], b["internal"] = s.Project, false
			return nil
		},
	}
	// no GET by email in the real API; Get is still handy for the mock.
	mux.HandleFunc("POST "+base, accounts.Create)
	mux.HandleFunc("GET "+base, accounts.List)
	mux.HandleFunc("DELETE "+base+"/{serviceAccountEmail}", accounts.Delete)

	keys := &crud.Coll{
		D: d, Err: httpx.ErrStackit, Type: "serviceaccount/key", Service: "serviceaccount",
		Label: "service account key", IDParam: "keyId", IDKey: "id", NameKey: "id",
		Scope: func(r *http.Request) crud.Scope {
			return crud.Scope{Project: r.PathValue("projectId"), Parent: r.PathValue("serviceAccountEmail")}
		},
		Prepare: func(r *http.Request, s crud.Scope, b map[string]any) error {
			if _, ok := d.Store.Get("serviceaccount/account", s.Project, s.Parent); !ok {
				return errMsg("service account not found")
			}
			pub := httpx.Str(b, "publicKey")
			origin := "USER_PROVIDED"
			if pub == "" {
				pub, origin = "-----BEGIN PUBLIC KEY-----\nbW9jaw==\n-----END PUBLIC KEY-----\n", "GENERATED"
			}
			b["active"], b["createdAt"] = true, time.Now().UTC().Format(time.RFC3339)
			b["publicKey"], b["keyOrigin"], b["keyType"] = pub, origin, "USER_MANAGED"
			if httpx.Str(b, "algorithm") == "" {
				b["keyAlgorithm"] = "RSA_2048"
			} else {
				b["keyAlgorithm"] = b["algorithm"]
			}
			delete(b, "algorithm")
			creds := map[string]any{"aud": "https://stackit-service-account-prod.apps.01.cf.eu01.stackit.cloud",
				"iss": "stackit/" + s.Parent, "kid": httpx.UUID(), "sub": httpx.UUID()}
			b["_credentials"] = creds
			return nil
		},
		CreateResp: func(raw map[string]any) any {
			creds := crud.Map(raw["_credentials"])
			creds["privateKey"] = "-----BEGIN PRIVATE KEY-----\nbW9jaw==\n-----END PRIVATE KEY-----\n"
			creds["tokenEndpoint"] = ""
			raw["credentials"] = creds
			return raw
		},
		Decorate: func(m map[string]any, res store.Resource) {
			m["credentials"] = crud.Map(res.Data["_credentials"])
		},
	}
	kb := base + "/{serviceAccountEmail}/keys"
	mux.HandleFunc("POST "+kb, keys.Create)
	mux.HandleFunc("GET "+kb, keys.List)
	mux.HandleFunc("GET "+kb+"/{keyId}", keys.Get)
	mux.HandleFunc("PATCH "+kb+"/{keyId}", keys.Patch)
	mux.HandleFunc("DELETE "+kb+"/{keyId}", keys.Delete)
}

type errMsg string

func (e errMsg) Error() string { return string(e) }
