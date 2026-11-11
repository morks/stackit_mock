// Package auth mocks the service-account token endpoint (key flow).
package auth

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"time"

	"github.com/morks/stackit_mock/internal/httpx"
)

// Token returns an unsigned JWT-shaped token with an exp claim (clients only parse it).
func Token(sub string) (string, int) {
	enc := func(v any) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	const ttl = 3600
	now := time.Now().Unix()
	return enc(map[string]any{"alg": "RS512", "typ": "JWT"}) + "." +
		enc(map[string]any{"iss": "stackit-mock", "sub": sub, "iat": now, "exp": now + ttl}) + "." +
		base64.RawURLEncoding.EncodeToString([]byte("mock-signature")), ttl
}

func Register(mux *http.ServeMux, d *httpx.Deps) {
	h := func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm() // any assertion is accepted
		tok, ttl := Token("mock-service-account")
		httpx.WriteJSON(w, 200, map[string]any{
			"access_token": tok, "expires_in": ttl, "refresh_token": "", "scope": "", "token_type": "Bearer",
		})
	}
	mux.HandleFunc("POST /token", h)
	mux.HandleFunc("POST /serviceaccount/token", h)
}
