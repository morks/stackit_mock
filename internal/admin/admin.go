// Package admin exposes the /_admin API used by the TUI and for test setup.
package admin

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/morks/stackit_mock/internal/faults"
	"github.com/morks/stackit_mock/internal/httpx"
	"github.com/morks/stackit_mock/internal/store"
)

const Prefix = "/_admin"

func Register(mux *http.ServeMux, d *httpx.Deps, f *faults.Set) {
	mux.HandleFunc("GET "+Prefix+"/health", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, 200, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, 200, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET "+Prefix+"/resources", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		httpx.WriteJSON(w, 200, d.Store.List(store.Filter{
			Type: q.Get("type"), Service: q.Get("service"), Project: q.Get("project"), Parent: q.Get("parent"),
		}))
	})
	mux.HandleFunc("GET "+Prefix+"/events", func(w http.ResponseWriter, r *http.Request) {
		since, _ := strconv.ParseUint(r.URL.Query().Get("since"), 10, 64)
		ev, last := d.Store.Events(since)
		httpx.WriteJSON(w, 200, map[string]any{"last": last, "events": ev})
	})
	mux.HandleFunc("POST "+Prefix+"/reset", func(w http.ResponseWriter, r *http.Request) {
		d.Store.Reset()
		f.Clear()
		httpx.WriteJSON(w, 200, map[string]string{"status": "reset"})
	})
	mux.HandleFunc("GET "+Prefix+"/faults", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, 200, f.List())
	})
	mux.HandleFunc("POST "+Prefix+"/faults", func(w http.ResponseWriter, r *http.Request) {
		var rule faults.Rule
		if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
			httpx.WriteError(w, 400, "invalid JSON body: "+err.Error())
			return
		}
		httpx.WriteJSON(w, 201, f.Add(rule))
	})
	mux.HandleFunc("DELETE "+Prefix+"/faults", func(w http.ResponseWriter, r *http.Request) {
		if id, err := strconv.Atoi(r.URL.Query().Get("id")); err == nil {
			f.Remove(id)
		} else {
			f.Clear()
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
