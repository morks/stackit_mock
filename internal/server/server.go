// Package server wires the mux, middleware and service handlers together.
package server

import (
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/morks/stackit_mock/internal/admin"
	"github.com/morks/stackit_mock/internal/faults"
	"github.com/morks/stackit_mock/internal/httpx"
)

// Registrar registers a service's routes.
type Registrar = func(mux *http.ServeMux, d *httpx.Deps)

// Registrars is filled by services (see services.go).
var Registrars []Registrar

type Options struct {
	StrictAuth bool
	Quiet      bool
}

func New(d *httpx.Deps, opt Options) (http.Handler, *faults.Set) {
	mux := http.NewServeMux()
	fs := &faults.Set{}
	admin.Register(mux, d, fs)
	for _, reg := range Registrars {
		reg(mux, d)
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteError(w, http.StatusNotFound, "no mock route for "+r.Method+" "+r.URL.Path)
	})
	return chain(mux, fs, opt), fs
}

type statusWriter struct {
	http.ResponseWriter
	code int
}

func (s *statusWriter) WriteHeader(c int) { s.code = c; s.ResponseWriter.WriteHeader(c) }

func chain(next http.Handler, fs *faults.Set, opt Options) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, code: 200}
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("panic: %v", rec)
				httpx.WriteError(sw, 500, "internal mock error")
			}
			if !opt.Quiet {
				log.Printf("%s %s -> %d (%s)", r.Method, r.URL.RequestURI(), sw.code, time.Since(start).Round(time.Millisecond))
			}
		}()
		isAdmin := strings.HasPrefix(r.URL.Path, admin.Prefix) || r.URL.Path == "/healthz"
		if !isAdmin {
			if rule, ok := fs.Match(r.Method, r.URL.Path); ok {
				msg := rule.Message
				if msg == "" {
					msg = "injected fault"
				}
				httpx.WriteError(sw, rule.Status, msg)
				return
			}
			if opt.StrictAuth && !strings.HasPrefix(r.URL.Path, "/token") &&
				!strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
				httpx.WriteError(sw, 401, "missing bearer token")
				return
			}
		}
		next.ServeHTTP(sw, r)
	})
}
