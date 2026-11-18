package httpx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"time"

	"finopshq/internal/apperr"
)

type ctxKey int

const requestIDKey ctxKey = iota

func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

func WithRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" || len(id) > 64 {
			b := make([]byte, 8)
			_, _ = rand.Read(b)
			id = hex.EncodeToString(b)
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func Logger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		slog.InfoContext(r.Context(), "http request",
			"method", r.Method, "path", r.URL.Path, "status", rec.status,
			"duration_ms", time.Since(start).Milliseconds(), "request_id", RequestID(r.Context()))
	})
}

func Recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				slog.ErrorContext(r.Context(), "panic", "value", v, "stack", string(debug.Stack()), "request_id", RequestID(r.Context()))
				WriteJSON(w, http.StatusInternalServerError, map[string]any{
					"error": apperr.New(http.StatusInternalServerError, "internal", "something went wrong"),
				})
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		next.ServeHTTP(w, r)
	})
}

// CSRFGuard rejects state-changing requests that lack the custom header (which a plain HTML
// form cannot set) or that come from a foreign Origin. Combined with SameSite=Lax cookies.
func CSRFGuard(allowedOrigin string, exempt func(*http.Request) bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions:
				next.ServeHTTP(w, r)
				return
			}
			if exempt != nil && exempt(r) {
				next.ServeHTTP(w, r)
				return
			}
			if r.Header.Get("X-Requested-With") != "finops" {
				WriteError(w, r, apperr.Forbidden("missing X-Requested-With header"))
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" && origin != allowedOrigin && !sameHost(origin, r.Host) {
				WriteError(w, r, apperr.Forbidden("cross-origin request rejected"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func sameHost(origin, host string) bool {
	for _, scheme := range []string{"http://", "https://"} {
		if origin == scheme+host {
			return true
		}
	}
	return false
}

func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
