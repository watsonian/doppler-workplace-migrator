package api

import (
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"crypto/rand"
	"encoding/hex"
)

// requestIDKey is used to store the request ID in the context.
type contextKey string

const requestIDContextKey contextKey = "request_id"

// generateRequestID creates a random hex request ID.
func generateRequestID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// requestIDMiddleware adds a unique request ID to each request.
func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			id = generateRequestID()
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r)
	})
}

// loggingMiddleware logs each request with method, path, status, and duration.
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		wrapped := &statusRecorder{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(wrapped, r)
		log.Printf("%s %s %d %s", r.Method, r.URL.Path, wrapped.statusCode, time.Since(start))
	})
}

// statusRecorder wraps ResponseWriter to capture the status code.
// It also implements http.Flusher so that SSE streaming works through
// the logging middleware.
type statusRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (sr *statusRecorder) WriteHeader(code int) {
	sr.statusCode = code
	sr.ResponseWriter.WriteHeader(code)
}

// Unwrap exposes the underlying ResponseWriter to http.ResponseController.
//
// Without it the controller cannot reach the connection through this wrapper,
// so anything it does fails with "feature not supported". The SSE handler uses
// it to lift the server's 60-second write timeout for a stream meant to stay
// open for a whole migration — and without Unwrap that silently never worked:
// every stream was cut after a minute, and whatever was published before the
// browser reconnected was lost.
func (sr *statusRecorder) Unwrap() http.ResponseWriter {
	return sr.ResponseWriter
}

// Flush delegates to the underlying ResponseWriter if it supports flushing.
// This is required for Server-Sent Events (SSE) to work, since the SSE
// handler checks for http.Flusher support.
func (sr *statusRecorder) Flush() {
	if f, ok := sr.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// allowedHosts returns the host names this server answers to. Loopback always
// counts; ALLOWED_HOSTS adds more, comma-separated, for setups that reach the tool
// at another address (a VM with the port opened deliberately, say).
func allowedHosts() map[string]bool {
	hosts := map[string]bool{
		"localhost": true,
		"127.0.0.1": true,
		"::1":       true,
		"[::1]":     true,
	}
	for _, h := range strings.Split(os.Getenv("ALLOWED_HOSTS"), ",") {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			hosts[h] = true
		}
	}
	return hosts
}

// hostOnly strips any port and surrounding brackets from a host[:port] value.
func hostOnly(hostport string) string {
	hostport = strings.TrimSpace(hostport)
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		hostport = h
	}
	return strings.ToLower(strings.Trim(hostport, "[]"))
}

// originGuardMiddleware rejects requests that a browser on another site initiated,
// and requests addressed to a host this server does not answer to.
//
// This tool holds a source token with workplace-wide read and a target token with
// write access, and has no authentication — by design, since it is run locally by
// one operator for the length of one migration. That makes a drive-by from a page
// the operator happens to have open the real exposure: every state-changing endpoint
// is a POST that accepts text/plain, which is a CORS "simple request" a browser
// sends without a preflight and regardless of what the response would allow. The
// attacker never needs to read a reply — repointing source_host and calling
// /preflight is enough to send a live token to a host they control.
//
// Checking Origin stops that: browsers attach it to every cross-origin request,
// including those simple POSTs. Checking Host closes the DNS-rebinding variant,
// where an attacker's name resolves to 127.0.0.1 so the request looks same-origin.
// Neither check burdens a legitimate caller: the embedded UI is same-origin, the
// Vite dev server proxies from a loopback origin, and curl sends no Origin at all.
func originGuardMiddleware(next http.Handler) http.Handler {
	hosts := allowedHosts()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if host := hostOnly(r.Host); host != "" && !hosts[host] {
			jsonError(w, http.StatusForbidden,
				"Request addressed to an unrecognized host. Set ALLOWED_HOSTS if you reach this tool at another address.")
			return
		}

		// Absent on same-origin GETs and on non-browser clients; only meaningful
		// when the browser supplies it.
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || !hosts[hostOnly(u.Host)] {
				jsonError(w, http.StatusForbidden,
					"Cross-origin requests are not accepted. Open this tool directly rather than from another site.")
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

// chain applies middleware in order (first applied = outermost).
func chain(handler http.Handler, middlewares ...func(http.Handler) http.Handler) http.Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		handler = middlewares[i](handler)
	}
	return handler
}
