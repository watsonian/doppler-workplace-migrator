package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// newAPIRequest builds a request the origin guard accepts. httptest.NewRequest
// defaults Host to example.com, which the guard rejects — correctly, since a real
// request reaches this server on loopback.
func newAPIRequest(method, target string, body io.Reader) *http.Request {
	req := httptest.NewRequest(method, target, body)
	req.Host = "127.0.0.1:8080"
	return req
}

func TestAllAPIRoutesRegistered(t *testing.T) {
	s := NewServer()
	handler := newRouterWithServer(s, nil)

	tests := []struct {
		name   string
		method string
		path   string
		// We just verify the route exists (doesn't return 404) and returns JSON.
		// Implemented endpoints may return 400 (validation), stubs return 501.
		notExpected int
	}{
		{"configure", "POST", "/api/v1/configure", http.StatusNotFound},
		{"preflight", "POST", "/api/v1/preflight", http.StatusNotFound},
		{"gather", "POST", "/api/v1/gather", http.StatusNotFound},
		{"plan", "GET", "/api/v1/plan", http.StatusNotFound},
		{"migrate", "POST", "/api/v1/migrate", http.StatusNotFound},
		{"migrate_pause", "POST", "/api/v1/migrate/pause", http.StatusNotFound},
		{"migrate_resume", "POST", "/api/v1/migrate/resume", http.StatusNotFound},
		{"status", "GET", "/api/v1/status", http.StatusNotFound},
		{"checkpoint", "GET", "/api/v1/checkpoint", http.StatusNotFound},
		{"report_service_tokens", "GET", "/api/v1/report/service-tokens", http.StatusNotFound},
		{"report_manual_steps", "GET", "/api/v1/report/manual-steps", http.StatusNotFound},
		{"report_summary", "GET", "/api/v1/report/summary", http.StatusNotFound},
		{"report_final_json", "GET", "/api/v1/report/final.json", http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := newAPIRequest(tt.method, tt.path, nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code == tt.notExpected {
				t.Errorf("route %s %s returned %d (not registered)", tt.method, tt.path, rec.Code)
			}

			// Verify response is JSON
			var body map[string]interface{}
			if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
				t.Fatalf("expected JSON response, got decode error: %v", err)
			}
		})
	}
}

func TestFormerStubsNowImplemented(t *testing.T) {
	s := NewServer()
	handler := newRouterWithServer(s, nil)

	// These endpoints were previously stubs returning 501.
	// They should now return real responses (400 without data, but never 501).
	endpoints := []struct {
		method string
		path   string
	}{
		{"GET", "/api/v1/report/service-tokens"},
		{"GET", "/api/v1/report/summary"},
	}

	for _, ep := range endpoints {
		req := newAPIRequest(ep.method, ep.path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code == http.StatusNotImplemented {
			t.Errorf("%s %s: should not be a stub (501)", ep.method, ep.path)
		}
	}
}

func TestImplementedMigrationEndpointsNotStub(t *testing.T) {
	s := NewServer()
	handler := newRouterWithServer(s, nil)

	// These endpoints should NOT return 501 anymore
	endpoints := []struct {
		method string
		path   string
	}{
		{"POST", "/api/v1/migrate"},
		{"POST", "/api/v1/migrate/pause"},
		{"POST", "/api/v1/migrate/resume"},
		{"GET", "/api/v1/status"},
		{"GET", "/api/v1/checkpoint"},
	}

	for _, ep := range endpoints {
		req := newAPIRequest(ep.method, ep.path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code == http.StatusNotImplemented {
			t.Errorf("%s %s: should not be a stub (501)", ep.method, ep.path)
		}
	}
}

func TestSSEEndpoint(t *testing.T) {
	s := NewServer()
	handler := newRouterWithServer(s, nil)

	// Use a cancellable context so the SSE handler doesn't block forever
	ctx, cancel := context.WithCancel(context.Background())
	req := newAPIRequest("GET", "/api/v1/events", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(rec, req)
		close(done)
	}()

	// Publish an event so we know the handler has started processing
	s.SSE.Publish(SSEEvent{Type: "test", Data: map[string]string{"msg": "hello"}})

	// Cancel the context to stop the SSE handler
	cancel()
	<-done

	if rec.Code == http.StatusNotFound {
		t.Error("SSE endpoint not registered")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	// Verify the response contains SSE headers and the connected event
	body := rec.Body.String()
	if !strings.Contains(body, "event: connected") {
		t.Error("expected initial connected event in SSE stream")
	}
}

func TestWrongMethodReturns405(t *testing.T) {
	s := NewServer()
	handler := newRouterWithServer(s, nil)

	// Try GET on a POST-only endpoint
	req := newAPIRequest("GET", "/api/v1/preflight", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

func TestOriginGuardAllowsLoopbackAndRejectsForeignOrigins(t *testing.T) {
	s := NewServer()
	handler := newRouterWithServer(s, nil)

	tests := []struct {
		name       string
		host       string
		origin     string
		wantStatus int
	}{
		{"no origin (curl, same-origin GET)", "127.0.0.1:8080", "", http.StatusOK},
		{"embedded UI origin", "127.0.0.1:8080", "http://127.0.0.1:8080", http.StatusOK},
		{"vite dev server origin", "localhost:8080", "http://localhost:5173", http.StatusOK},
		{"ipv6 loopback", "[::1]:8080", "http://[::1]:8080", http.StatusOK},
		// The drive-by: a page on another site POSTing to the local server.
		{"foreign origin", "127.0.0.1:8080", "https://evil.example", http.StatusForbidden},
		// DNS rebinding: attacker's name resolves to loopback, so the request looks
		// same-origin to the browser but the Host header gives it away.
		{"rebound host", "evil.example:8080", "http://evil.example:8080", http.StatusForbidden},
		{"rebound host, no origin", "evil.example:8080", "", http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/v1/status", nil)
			req.Host = tt.host
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}

// The wildcard is gone: no legitimate client is cross-origin, and echoing it let a
// foreign page read the plan and reports.
func TestNoWildcardCORSHeader(t *testing.T) {
	s := NewServer()
	handler := newRouterWithServer(s, nil)

	req := newAPIRequest("GET", "/api/v1/status", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want it absent", got)
	}
}

// ALLOWED_HOSTS is the escape hatch for reaching the tool at another address.
func TestAllowedHostsEnv(t *testing.T) {
	t.Setenv("ALLOWED_HOSTS", "migrator.internal")

	s := NewServer()
	handler := newRouterWithServer(s, nil)

	req := httptest.NewRequest("GET", "/api/v1/status", nil)
	req.Host = "migrator.internal:8080"
	req.Header.Set("Origin", "http://migrator.internal:8080")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 for an allowlisted host", rec.Code)
	}
}

func TestRequestIDHeader(t *testing.T) {
	s := NewServer()
	handler := newRouterWithServer(s, nil)

	req := newAPIRequest("GET", "/api/v1/status", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Header().Get("X-Request-ID") == "" {
		t.Error("expected X-Request-ID header in response")
	}
}

func TestRequestIDPreserved(t *testing.T) {
	s := NewServer()
	handler := newRouterWithServer(s, nil)

	req := newAPIRequest("GET", "/api/v1/status", nil)
	req.Header.Set("X-Request-ID", "test-id-123")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Header().Get("X-Request-ID") != "test-id-123" {
		t.Errorf("expected X-Request-ID 'test-id-123', got '%s'", rec.Header().Get("X-Request-ID"))
	}
}

func TestJSONResponseContentType(t *testing.T) {
	s := NewServer()
	handler := newRouterWithServer(s, nil)

	req := newAPIRequest("GET", "/api/v1/status", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", ct)
	}
}

func TestStaticFileServing(t *testing.T) {
	staticFS := fstest.MapFS{
		"index.html":     {Data: []byte("<!doctype html><title>app</title>")},
		"assets/app.js":  {Data: []byte("console.log(1)")},
		".gitkeep":       {Data: []byte{}},
		".well-known/x":  {Data: []byte("secret-ish")},
		"assets/.hidden": {Data: []byte("also hidden")},
	}

	s := NewServer()
	handler := newRouterWithServer(s, staticFS)

	tests := []struct {
		name       string
		path       string
		wantStatus int
		wantBody   string // substring; empty means unchecked
	}{
		{"root serves index", "/", http.StatusOK, "<title>app</title>"},
		{"asset served directly", "/assets/app.js", http.StatusOK, "console.log(1)"},
		{"unknown route falls back to index for SPA routing", "/preflight", http.StatusOK, "<title>app</title>"},
		{"keep-file is not reachable", "/.gitkeep", http.StatusNotFound, ""},
		{"hidden directory is not reachable", "/.well-known/x", http.StatusNotFound, ""},
		{"nested hidden file is not reachable", "/assets/.hidden", http.StatusNotFound, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := newAPIRequest("GET", tt.path, nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("GET %s: expected status %d, got %d", tt.path, tt.wantStatus, rec.Code)
			}
			if tt.wantBody != "" && !strings.Contains(rec.Body.String(), tt.wantBody) {
				t.Errorf("GET %s: expected body to contain %q, got %q", tt.path, tt.wantBody, rec.Body.String())
			}
		})
	}
}
