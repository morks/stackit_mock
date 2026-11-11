package crud

import (
	"fmt"
	"math/rand/v2"
	"strconv"
)

// NumID returns a random positive int64-sized id (Postgres Flex users/databases).
func NumID() int64 { return 1_000_000_000 + rand.Int64N(8_000_000_000) } // #nosec G404 -- mock ids, not secrets

// KeyString renders an id value (string or JSON number) as the store key.
func KeyString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return strconv.FormatInt(int64(t), 10)
	default:
		return fmt.Sprint(t)
	}
}

// DeepMerge returns dst overlaid with src (nested objects merged, others replaced, null deletes).
func DeepMerge(dst, src map[string]any) map[string]any {
	out := make(map[string]any, len(dst))
	for k, v := range dst {
		out[k] = v
	}
	for k, v := range src {
		if v == nil {
			delete(out, k)
			continue
		}
		sm, sok := v.(map[string]any)
		dm, dok := out[k].(map[string]any)
		if sok && dok {
			out[k] = DeepMerge(dm, sm)
		} else {
			out[k] = v
		}
	}
	return out
}

// Map returns v as an object (nil-safe).
func Map(v any) map[string]any { m, _ := v.(map[string]any); return m }

// Rand returns a random lowercase hex string of n chars.
func Rand(n int) string {
	const h = "0123456789abcdef"
	b := make([]byte, n)
	for i := range b {
		b[i] = h[rand.IntN(16)] // #nosec G404 -- mock ids, not secrets
	}
	return string(b)
}
