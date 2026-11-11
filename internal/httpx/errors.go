package httpx

import (
	"net/http"
	"time"
)

// ErrFunc writes a service-specific error body.
type ErrFunc func(w http.ResponseWriter, r *http.Request, status int, msg string)

// ErrStackit: Resource Manager / Service Account / Service Enablement format.
func ErrStackit(w http.ResponseWriter, r *http.Request, status int, msg string) {
	WriteJSON(w, status, map[string]any{
		"error": http.StatusText(status), "message": msg, "path": r.URL.Path,
		"status": status, "timeStamp": time.Now().UTC().Format(time.RFC3339),
	})
}

// ErrIaaS: {"code":..,"msg":..}.
func ErrIaaS(w http.ResponseWriter, _ *http.Request, status int, msg string) {
	WriteJSON(w, status, map[string]any{"code": status, "msg": msg})
}

// ErrGeneric: SKE.
func ErrGeneric(w http.ResponseWriter, _ *http.Request, status int, msg string) {
	WriteJSON(w, status, map[string]any{"code": status, "message": msg})
}

// ErrFlex: PostgreSQL / MongoDB Flex.
func ErrFlex(w http.ResponseWriter, _ *http.Request, status int, msg string) {
	WriteJSON(w, status, map[string]any{
		"code": status, "message": msg, "traceId": UUID(), "type": http.StatusText(status),
	})
}
