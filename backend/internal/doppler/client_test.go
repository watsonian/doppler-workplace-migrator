package doppler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func newTestServer(handler http.HandlerFunc) (*httptest.Server, *Client) {
	server := httptest.NewServer(handler)
	client := NewClient(server.URL, "dp.ct.testtoken",
		WithHTTPClient(server.Client()))
	return server, client
}

func TestClientGet(t *testing.T) {
	server, client := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer dp.ct.testtoken" {
			t.Errorf("missing auth header")
		}
		if r.Header.Get("Accept") != "application/json" {
			t.Errorf("missing accept header")
		}
		w.Header().Set("X-Ratelimit-Remaining", "99")
		w.Header().Set("X-Ratelimit-Limit", "100")
		json.NewEncoder(w).Encode(map[string]string{"name": "test"})
	})
	defer server.Close()

	result, headers, err := client.Get(context.Background(), "/workplace", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if headers == nil {
		t.Fatal("expected headers")
	}

	var parsed map[string]string
	json.Unmarshal(result, &parsed)
	if parsed["name"] != "test" {
		t.Errorf("expected name 'test', got %q", parsed["name"])
	}
}

func TestClientPost(t *testing.T) {
	server, client := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected JSON content type")
		}

		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if body["name"] != "new-project" {
			t.Errorf("expected name 'new-project', got %q", body["name"])
		}

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"slug": "new-project"})
	})
	defer server.Close()

	result, _, err := client.Post(context.Background(), "/projects", map[string]string{"name": "new-project"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var parsed map[string]string
	json.Unmarshal(result, &parsed)
	if parsed["slug"] != "new-project" {
		t.Errorf("expected slug 'new-project', got %q", parsed["slug"])
	}
}

func TestClientPut(t *testing.T) {
	server, client := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
	})
	defer server.Close()

	_, _, err := client.Put(context.Background(), "/resource", map[string]string{"key": "value"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestClientPatch(t *testing.T) {
	server, client := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Errorf("expected PATCH, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "patched"})
	})
	defer server.Close()

	_, _, err := client.Patch(context.Background(), "/resource", map[string]string{"key": "value"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestClientDelete(t *testing.T) {
	server, client := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("expected DELETE, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})
	})
	defer server.Close()

	_, _, err := client.Delete(context.Background(), "/resource", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestClientGetWithParams(t *testing.T) {
	server, client := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("project") != "my-proj" {
			t.Errorf("expected project=my-proj, got %q", r.URL.Query().Get("project"))
		}
		json.NewEncoder(w).Encode(map[string]string{"ok": "true"})
	})
	defer server.Close()

	params := url.Values{"project": {"my-proj"}}
	_, _, err := client.Get(context.Background(), "/environments", params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestClient4xxReturnsError(t *testing.T) {
	server, client := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"messages": []string{"Project not found"},
		})
	})
	defer server.Close()

	_, _, err := client.Get(context.Background(), "/projects/missing", nil)
	if err == nil {
		t.Fatal("expected error for 404")
	}

	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected APIError, got %T", err)
	}
	if apiErr.StatusCode != 404 {
		t.Errorf("expected status 404, got %d", apiErr.StatusCode)
	}
	if apiErr.Message != "Project not found" {
		t.Errorf("expected message 'Project not found', got %q", apiErr.Message)
	}
}

func TestClient4xxNoRetry(t *testing.T) {
	var calls int32
	server, client := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"message": "bad request"})
	})
	defer server.Close()

	_, _, err := client.Get(context.Background(), "/bad", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("4xx should not retry, got %d calls", atomic.LoadInt32(&calls))
	}
}

func TestClient5xxRetries(t *testing.T) {
	var calls int32
	server, client := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"error": "server error"}`))
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"ok": "true"})
	})
	defer server.Close()

	_, _, err := client.Get(context.Background(), "/flaky", nil)
	if err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}
	if atomic.LoadInt32(&calls) != 3 {
		t.Errorf("expected 3 calls (2 failures + 1 success), got %d", atomic.LoadInt32(&calls))
	}
}

func TestClient5xxExhaustsRetries(t *testing.T) {
	var calls int32
	server, client := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error": "always failing"}`))
	})
	defer server.Close()

	_, _, err := client.Get(context.Background(), "/always-fail", nil)
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	// 1 initial + 3 retries = 4 total
	if atomic.LoadInt32(&calls) != 4 {
		t.Errorf("expected 4 calls total, got %d", atomic.LoadInt32(&calls))
	}
}

func TestClient429WaitsAndRetries(t *testing.T) {
	var calls int32
	server, client := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error": "rate limited"}`))
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"ok": "true"})
	})
	defer server.Close()

	start := time.Now()
	_, _, err := client.Get(context.Background(), "/limited", nil)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("expected success after rate limit wait, got: %v", err)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Errorf("expected 2 calls, got %d", atomic.LoadInt32(&calls))
	}
	// Should wait at least 1 second (retry-after) + 500ms buffer
	if elapsed < 1*time.Second {
		t.Errorf("expected wait of at least 1s, got %v", elapsed)
	}
}

func TestClientContextCancellation(t *testing.T) {
	server, client := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		// Simulate slow response
		time.Sleep(5 * time.Second)
		json.NewEncoder(w).Encode(map[string]string{"ok": "true"})
	})
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, _, err := client.Get(ctx, "/slow", nil)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestClientRateLimitHeadersParsed(t *testing.T) {
	server, client := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Ratelimit-Remaining", "42")
		w.Header().Set("X-Ratelimit-Limit", "100")
		w.Header().Set("X-Ratelimit-Reset", "1700000000")
		json.NewEncoder(w).Encode(map[string]string{"ok": "true"})
	})
	defer server.Close()

	_, _, err := client.Get(context.Background(), "/test", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	status := client.RateLimiterStatus()
	s := status[RateLimitRead]
	if s.Remaining != 42 {
		t.Errorf("expected remaining 42, got %d", s.Remaining)
	}
	if s.Limit != 100 {
		t.Errorf("expected limit 100, got %d", s.Limit)
	}
}

func TestClientBaseURLNormalization(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"https://api.doppler.com", "https://api.doppler.com/v3"},
		{"https://api.doppler.com/", "https://api.doppler.com/v3"},
		{"https://api.doppler.com/v3", "https://api.doppler.com/v3"},
		{"https://api.doppler.com/v3/", "https://api.doppler.com/v3"},
		{"https://custom.host.com", "https://custom.host.com/v3"},
	}

	for _, tt := range tests {
		c := NewClient(tt.input, "token")
		if c.BaseURL() != tt.expected {
			t.Errorf("input %q: expected %q, got %q", tt.input, tt.expected, c.BaseURL())
		}
	}
}

// Pagination tests

func TestListAllZeroItems(t *testing.T) {
	server, client := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"projects": []interface{}{},
		})
	})
	defer server.Close()

	items, err := client.ListAll(context.Background(), "/projects", nil, "projects", RateLimitRead)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected 0 items, got %d", len(items))
	}
}

func TestListAllSinglePage(t *testing.T) {
	server, client := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		projects := make([]map[string]string, 3)
		for i := range projects {
			projects[i] = map[string]string{"name": fmt.Sprintf("proj-%d", i)}
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"projects": projects,
		})
	})
	defer server.Close()

	items, err := client.ListAll(context.Background(), "/projects", nil, "projects", RateLimitRead)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 3 {
		t.Errorf("expected 3 items, got %d", len(items))
	}
}

func TestListAllMultiplePages(t *testing.T) {
	var requestCount int32
	server, client := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		n := atomic.AddInt32(&requestCount, 1)

		var projects []map[string]string
		switch page {
		case "1", "":
			// Return full page (100 items)
			for i := 0; i < 100; i++ {
				projects = append(projects, map[string]string{"name": fmt.Sprintf("proj-1-%d", i)})
			}
		case "2":
			// Return partial page (50 items)
			for i := 0; i < 50; i++ {
				projects = append(projects, map[string]string{"name": fmt.Sprintf("proj-2-%d", i)})
			}
		case "3":
			// Empty page — how a paginated endpoint signals the end. A short page
			// does not, since several endpoints cap page size below what is asked.
			projects = []map[string]string{}
		default:
			t.Errorf("unexpected page %s (request #%d)", page, n)
		}

		// A paginated endpoint echoes "page"; that is what tells ListAll to continue.
		json.NewEncoder(w).Encode(map[string]interface{}{
			"projects": projects,
			"page":     page,
		})
	})
	defer server.Close()

	items, err := client.ListAll(context.Background(), "/projects", nil, "projects", RateLimitRead)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 150 {
		t.Errorf("expected 150 items, got %d", len(items))
	}
	// Three requests: the third comes back empty, which is the stop signal.
	if atomic.LoadInt32(&requestCount) != 3 {
		t.Errorf("expected 3 requests, got %d", atomic.LoadInt32(&requestCount))
	}
}

// An endpoint that returns no "page" attribute is not paginated: it has already
// returned everything, and asking for page 2 would return the same payload again.
// Several endpoints behave this way and previously sent ListAll into an infinite
// append loop once they held a full page's worth of items.
func TestListAllStopsOnNonPaginatedEndpoint(t *testing.T) {
	var requests int32
	server, client := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		roles := make([]map[string]string, 0, 100)
		for i := 0; i < 100; i++ {
			roles = append(roles, map[string]string{"name": fmt.Sprintf("role-%d", i)})
		}
		// No "page" key — exactly what /workplace/roles and friends return.
		json.NewEncoder(w).Encode(map[string]interface{}{"roles": roles})
	})
	defer server.Close()

	items, err := client.ListAll(context.Background(), "/workplace/roles", nil, "roles", RateLimitRead)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Fatalf("made %d requests, want exactly 1 — this endpoint does not paginate", got)
	}
	if len(items) != 100 {
		t.Errorf("expected 100 items, got %d", len(items))
	}
}

// GET /workplace/users ignores per_page and hard-codes 20 per page. A stop rule
// based on the requested per_page truncated every workplace to its first 20 users.
func TestListAllHandlesServerCappedPageSize(t *testing.T) {
	const total = 45
	server, client := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page < 1 {
			page = 1
		}
		start := (page - 1) * 20
		users := []map[string]string{}
		for i := start; i < start+20 && i < total; i++ {
			users = append(users, map[string]string{"id": fmt.Sprintf("u-%d", i)})
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"workplace_users": users, "page": page})
	})
	defer server.Close()

	items, err := client.ListAll(context.Background(), "/workplace/users", nil, "workplace_users", RateLimitRead)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != total {
		t.Errorf("got %d users, want all %d — a 20-item page must not end the loop", len(items), total)
	}
}

func TestListAllMissingKey(t *testing.T) {
	server, client := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"other_key": []interface{}{},
		})
	})
	defer server.Close()

	_, err := client.ListAll(context.Background(), "/projects", nil, "projects", RateLimitRead)
	if err == nil {
		t.Fatal("expected error for missing key")
	}
}

func TestListAllContextCancellation(t *testing.T) {
	server, client := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"projects": []interface{}{},
		})
	})
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := client.ListAll(ctx, "/projects", nil, "projects", RateLimitRead)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestListAllPreservesExistingParams(t *testing.T) {
	server, client := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("project") != "my-proj" {
			t.Errorf("expected project=my-proj, got %q", r.URL.Query().Get("project"))
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"configs": []interface{}{},
		})
	})
	defer server.Close()

	params := url.Values{"project": {"my-proj"}}
	_, err := client.ListAll(context.Background(), "/configs", params, "configs", RateLimitRead)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBackoffDuration(t *testing.T) {
	tests := []struct {
		attempt  int
		expected time.Duration
	}{
		{1, 1 * time.Second},
		{2, 2 * time.Second},
		{3, 4 * time.Second},
		{4, 4 * time.Second}, // capped
	}
	for _, tt := range tests {
		d := backoffDuration(tt.attempt)
		if d != tt.expected {
			t.Errorf("attempt %d: expected %v, got %v", tt.attempt, tt.expected, d)
		}
	}
}
