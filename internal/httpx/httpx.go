// Package httpx holds helpers shared by all service handlers.
package httpx

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/morks/stackit_mock/internal/lifecycle"
	"github.com/morks/stackit_mock/internal/store"
)

// Deps are injected into every service's Register function.
type Deps struct {
	Store   *store.Store
	Engine  *lifecycle.Engine
	BaseURL string // configured external base URL; empty = derive from request
}

func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

// WriteError emits the STACKIT-style error body.
func WriteError(w http.ResponseWriter, status int, msg string) {
	WriteJSON(w, status, map[string]any{
		"code": status, "message": msg, "status": http.StatusText(status),
	})
}

func NotFound(w http.ResponseWriter, what string) {
	WriteError(w, http.StatusNotFound, what+" not found")
}

// Decode reads a JSON object body; on failure writes 400 and returns false.
func Decode(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	m := map[string]any{}
	if r.Body == nil || r.ContentLength == 0 {
		return m, true
	}
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return nil, false
	}
	return m, true
}

func Str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

// UUID returns a random RFC 4122 v4 UUID.
func UUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// BaseURL returns the externally visible base URL, honoring X-Forwarded-*.
func (d *Deps) BaseFor(r *http.Request) string {
	if d.BaseURL != "" {
		return strings.TrimRight(d.BaseURL, "/")
	}
	scheme := "http"
	if p := r.Header.Get("X-Forwarded-Proto"); p != "" {
		scheme = strings.TrimSpace(strings.Split(p, ",")[0])
	} else if r.TLS != nil {
		scheme = "https"
	}
	host := r.Host
	if h := r.Header.Get("X-Forwarded-Host"); h != "" {
		host = strings.TrimSpace(strings.Split(h, ",")[0])
	}
	return scheme + "://" + host
}
