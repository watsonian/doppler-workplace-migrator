package preflight

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/doppler/onprem-migrator/internal/doppler"
)

// mockDopplerServer creates a test server with configurable endpoint responses.
type mockEndpoint struct {
	status int
	body   interface{}
}

func newMockDopplerServer(t *testing.T, endpoints map[string]mockEndpoint) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for path, ep := range endpoints {
		ep := ep // capture
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(ep.status)
			if ep.body != nil {
				json.NewEncoder(w).Encode(ep.body)
			}
		})
	}
	return httptest.NewServer(mux)
}

// validSourceEndpoints returns mock endpoints for a healthy source instance.
func validSourceEndpoints() map[string]mockEndpoint {
	return map[string]mockEndpoint{
		"/v3/me": {http.StatusOK, map[string]interface{}{
			"name":      "Test Token",
			"slug":      "test-token",
			"type":      "cli",
			"principal": map[string]string{"type": "workplace_user", "slug": "user-1"},
			"workplace": map[string]string{"slug": "wp-src", "name": "Source WP"},
		}},
		"/v3/workplace": {http.StatusOK, map[string]interface{}{
			"workplace": map[string]string{
				"id":   "wp-src",
				"name": "Source Workplace",
			},
		}},
		"/v3/projects": {http.StatusOK, map[string]interface{}{
			"projects": []map[string]string{
				{"name": "proj-1", "slug": "proj-1"},
			},
		}},
	}
}

// validTargetEndpoints returns mock endpoints for a healthy target instance.
func validTargetEndpoints() map[string]mockEndpoint {
	return map[string]mockEndpoint{
		"/v3/me": {http.StatusOK, map[string]interface{}{
			"name":      "Target Token",
			"slug":      "target-token",
			"type":      "personal",
			"workplace": map[string]string{"slug": "wp-tgt", "name": "Target WP"},
		}},
		"/v3/workplace": {http.StatusOK, map[string]interface{}{
			"workplace": map[string]string{
				"id":   "wp-tgt",
				"name": "Target Workplace",
			},
		}},
		"/v3/projects": {http.StatusOK, map[string]interface{}{
			"projects": []map[string]string{},
		}},
		// The write check reads workplace roles: the migration's first phase creates
		// them, so a token without workplace access should fail here, not mid-run.
		"/v3/workplace/roles": {http.StatusOK, map[string]interface{}{
			"roles": []map[string]interface{}{},
		}},
	}
}

func TestAllChecksPassed(t *testing.T) {
	sourceServer := newMockDopplerServer(t, validSourceEndpoints())
	defer sourceServer.Close()
	targetServer := newMockDopplerServer(t, validTargetEndpoints())
	defer targetServer.Close()

	source := doppler.NewClient(sourceServer.URL, "dp.ct.test", doppler.WithHTTPClient(sourceServer.Client()))
	target := doppler.NewClient(targetServer.URL, "dp.pt.test", doppler.WithHTTPClient(targetServer.Client()))

	var updates []Check
	checker := NewChecker(source, target, false, func(c Check) {
		updates = append(updates, c)
	})

	results := checker.RunAll(context.Background())

	if len(results) != 11 {
		t.Fatalf("expected 11 checks, got %d", len(results))
	}

	for _, r := range results {
		// A user-backed source token cannot read restricted secrets, so that check
		// warns by design; everything else must pass.
		if r.ID == "source_restricted_secrets" || r.ID == "users_provisioned" {
			if r.Status != StatusWarning && r.Status != StatusPassed {
				t.Errorf("check %q: expected warning or pass, got %s (%s)", r.ID, r.Status, r.Message)
			}
			continue
		}
		if r.Status != StatusPassed {
			t.Errorf("check %q: expected passed, got %s (%s)", r.ID, r.Status, r.Message)
		}
	}

	if !AllPassed(results) {
		t.Error("AllPassed should return true")
	}

	// Verify we got update callbacks (2 per check: running + final)
	if len(updates) != 22 {
		t.Errorf("expected 22 update callbacks (2 per check), got %d", len(updates))
	}
}

func TestSourceTokenInvalid(t *testing.T) {
	sourceServer := newMockDopplerServer(t, map[string]mockEndpoint{
		"/v3/me": {http.StatusUnauthorized, map[string]string{"message": "Invalid token"}},
	})
	defer sourceServer.Close()

	source := doppler.NewClient(sourceServer.URL, "bad-token", doppler.WithHTTPClient(sourceServer.Client()))
	checker := NewChecker(source, nil, true, nil)

	results := checker.RunAll(context.Background())

	// First check should fail
	if results[0].Status != StatusFailed {
		t.Errorf("expected source_token check to fail, got %s", results[0].Status)
	}
	if results[0].ID != "source_token" {
		t.Errorf("expected first check to be source_token, got %s", results[0].ID)
	}

	// Remaining source checks should also fail since the token is bad
	if HasFailures(results) != true {
		t.Error("HasFailures should return true")
	}
}

func TestSourceTokenAPISuccessPassesRegardlessOfType(t *testing.T) {
	// Token prefix validation happens at configuration time, not during preflight.
	// The preflight check only verifies the API call succeeds.
	sourceServer := newMockDopplerServer(t, map[string]mockEndpoint{
		"/v3/me": {http.StatusOK, map[string]interface{}{
			"name":      "Service Token",
			"slug":      "svc-token",
			"type":      "service",
			"workplace": map[string]string{"slug": "wp1", "name": "Test WP"},
		}},
		"/v3/workplace": {http.StatusOK, map[string]interface{}{
			"workplace": map[string]string{"name": "Test"},
		}},
		"/v3/projects": {http.StatusOK, map[string]interface{}{
			"projects": []map[string]string{{"name": "p1", "slug": "p1"}},
		}},
	})
	defer sourceServer.Close()

	source := doppler.NewClient(sourceServer.URL, "dp.st.test", doppler.WithHTTPClient(sourceServer.Client()))
	checker := NewChecker(source, nil, true, nil)

	results := checker.RunAll(context.Background())

	if results[0].Status != StatusPassed {
		t.Errorf("expected source_token to pass when API succeeds, got %s (%s)", results[0].Status, results[0].Message)
	}
}

func TestDevModeSkipsTargetChecks(t *testing.T) {
	sourceServer := newMockDopplerServer(t, validSourceEndpoints())
	defer sourceServer.Close()

	source := doppler.NewClient(sourceServer.URL, "dp.ct.test", doppler.WithHTTPClient(sourceServer.Client()))

	checker := NewChecker(source, nil, true, nil)

	results := checker.RunAll(context.Background())

	if len(results) != 11 {
		t.Fatalf("expected 11 checks, got %d", len(results))
	}

	// Source checks pass
	for i := 0; i < 3; i++ {
		if results[i].Status != StatusPassed {
			t.Errorf("source check %q: expected passed, got %s", results[i].ID, results[i].Status)
		}
	}

	// Target checks skipped
	for i := 3; i < 7; i++ {
		if results[i].Status != StatusSkipped {
			t.Errorf("target check %q: expected skipped, got %s", results[i].ID, results[i].Status)
		}
	}

	if !AllPassed(results) {
		t.Error("AllPassed should return true when target checks are skipped")
	}
}

func TestTargetUnreachable(t *testing.T) {
	sourceServer := newMockDopplerServer(t, validSourceEndpoints())
	defer sourceServer.Close()

	source := doppler.NewClient(sourceServer.URL, "dp.ct.test", doppler.WithHTTPClient(sourceServer.Client()))
	// Point target at a closed server
	target := doppler.NewClient("http://127.0.0.1:1", "dp.ct.test")

	checker := NewChecker(source, target, false, nil)

	results := checker.RunAll(context.Background())

	// target_reachable should fail
	var reachableCheck *Check
	for i, r := range results {
		if r.ID == "target_reachable" {
			reachableCheck = &results[i]
			break
		}
	}
	if reachableCheck == nil {
		t.Fatal("target_reachable check not found")
	}
	if reachableCheck.Status != StatusFailed {
		t.Errorf("expected target_reachable to fail, got %s", reachableCheck.Status)
	}
}

func TestOneCheckFailDoesntPreventOthers(t *testing.T) {
	// Source /me fails, but /workplace and /projects should still run
	sourceServer := newMockDopplerServer(t, map[string]mockEndpoint{
		"/v3/me": {http.StatusUnauthorized, map[string]string{"message": "bad token"}},
		"/v3/workplace": {http.StatusOK, map[string]interface{}{
			"workplace": map[string]string{"name": "Test"},
		}},
		"/v3/projects": {http.StatusOK, map[string]interface{}{
			"projects": []map[string]string{{"name": "p1", "slug": "p1"}},
		}},
	})
	defer sourceServer.Close()

	source := doppler.NewClient(sourceServer.URL, "dp.ct.test", doppler.WithHTTPClient(sourceServer.Client()))
	checker := NewChecker(source, nil, true, nil)

	results := checker.RunAll(context.Background())

	// First check fails
	if results[0].Status != StatusFailed {
		t.Errorf("expected first check to fail")
	}

	// Second check should still pass
	if results[1].Status != StatusPassed {
		t.Errorf("expected source_workplace to pass even after source_token fails, got %s", results[1].Status)
	}

	// Third check should still pass
	if results[2].Status != StatusPassed {
		t.Errorf("expected source_projects to pass, got %s", results[2].Status)
	}
}

func TestContextCancellation(t *testing.T) {
	sourceServer := newMockDopplerServer(t, validSourceEndpoints())
	defer sourceServer.Close()

	source := doppler.NewClient(sourceServer.URL, "dp.ct.test", doppler.WithHTTPClient(sourceServer.Client()))

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	checker := NewChecker(source, nil, true, nil)
	results := checker.RunAll(ctx)

	// All checks should fail with cancellation
	for _, r := range results {
		if r.Status != StatusFailed {
			t.Errorf("check %q: expected failed due to cancellation, got %s", r.ID, r.Status)
		}
	}
}

func TestEmptySourceWorkspace(t *testing.T) {
	sourceServer := newMockDopplerServer(t, map[string]mockEndpoint{
		"/v3/me": {http.StatusOK, map[string]interface{}{
			"name": "Token", "slug": "tok", "type": "cli", "workplace": map[string]string{"slug": "wp1", "name": "WP"},
		}},
		"/v3/workplace": {http.StatusOK, map[string]interface{}{
			"workplace": map[string]string{"name": "Empty WP"},
		}},
		"/v3/projects": {http.StatusOK, map[string]interface{}{
			"projects": []interface{}{},
		}},
	})
	defer sourceServer.Close()

	source := doppler.NewClient(sourceServer.URL, "dp.ct.test", doppler.WithHTTPClient(sourceServer.Client()))
	checker := NewChecker(source, nil, true, nil)

	results := checker.RunAll(context.Background())

	// source_projects should be a warning for empty workspace
	if results[2].Status != StatusWarning {
		t.Errorf("expected warning for empty projects, got %s", results[2].Status)
	}
}

func TestAllPassedHelper(t *testing.T) {
	tests := []struct {
		name     string
		checks   []Check
		expected bool
	}{
		{"all passed", []Check{{Status: StatusPassed}, {Status: StatusPassed}}, true},
		{"one failed", []Check{{Status: StatusPassed}, {Status: StatusFailed}}, false},
		{"all skipped", []Check{{Status: StatusSkipped}, {Status: StatusSkipped}}, true},
		{"mixed passed and skipped", []Check{{Status: StatusPassed}, {Status: StatusSkipped}}, true},
		{"warning is ok", []Check{{Status: StatusPassed}, {Status: StatusWarning}}, true},
		{"empty", []Check{}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := AllPassed(tt.checks); got != tt.expected {
				t.Errorf("AllPassed = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestHasFailuresHelper(t *testing.T) {
	tests := []struct {
		name     string
		checks   []Check
		expected bool
	}{
		{"no failures", []Check{{Status: StatusPassed}, {Status: StatusSkipped}}, false},
		{"one failure", []Check{{Status: StatusPassed}, {Status: StatusFailed}}, true},
		{"empty", []Check{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HasFailures(tt.checks); got != tt.expected {
				t.Errorf("HasFailures = %v, want %v", got, tt.expected)
			}
		})
	}
}

// Users cannot be created by the migration — the API has no endpoint for it — so a
// target without them silently skips every access control that names a user. This
// check exists to say so before anything is read.
func TestCheckUsersProvisioned(t *testing.T) {
	user := func(email string) map[string]interface{} {
		return map[string]interface{}{"id": "u-" + email, "user": map[string]string{"email": email}}
	}

	tests := []struct {
		name        string
		sourceUsers []map[string]interface{}
		targetUsers []map[string]interface{}
		wantStatus  CheckStatus
		wantInMsg   string
	}{
		{
			name:        "no users on target at all",
			sourceUsers: []map[string]interface{}{user("a@acme.com"), user("b@acme.com")},
			targetUsers: []map[string]interface{}{},
			wantStatus:  StatusWarning,
			wantInMsg:   "set up SCIM",
		},
		{
			name:        "only a couple of the source users present",
			sourceUsers: []map[string]interface{}{user("a@acme.com"), user("b@acme.com"), user("c@acme.com")},
			targetUsers: []map[string]interface{}{user("a@acme.com")},
			wantStatus:  StatusWarning,
			wantInMsg:   "2 of 3 users are missing",
		},
		{
			name:        "everyone present",
			sourceUsers: []map[string]interface{}{user("a@acme.com"), user("b@acme.com")},
			targetUsers: []map[string]interface{}{user("a@acme.com"), user("b@acme.com")},
			wantStatus:  StatusPassed,
			wantInMsg:   "All 2 source user(s) exist",
		},
		{
			name:        "extra users on the target are fine",
			sourceUsers: []map[string]interface{}{user("a@acme.com")},
			targetUsers: []map[string]interface{}{user("a@acme.com"), user("extra@acme.com")},
			wantStatus:  StatusPassed,
			wantInMsg:   "All 1 source user(s) exist",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// No "page" key: these fixtures are static, and a static fixture cannot
			// model pagination — echoing a page number makes ListAll correctly ask
			// for page 2 and get the same payload back, forever.
			src := validSourceEndpoints()
			src["/v3/workplace/users"] = mockEndpoint{http.StatusOK, map[string]interface{}{"workplace_users": tt.sourceUsers}}
			tgt := validTargetEndpoints()
			tgt["/v3/workplace/users"] = mockEndpoint{http.StatusOK, map[string]interface{}{"workplace_users": tt.targetUsers}}

			sourceServer := newMockDopplerServer(t, src)
			defer sourceServer.Close()
			targetServer := newMockDopplerServer(t, tgt)
			defer targetServer.Close()

			checker := NewChecker(
				doppler.NewClient(sourceServer.URL, "dp.sa.test", doppler.WithHTTPClient(sourceServer.Client())),
				doppler.NewClient(targetServer.URL, "dp.sa.test", doppler.WithHTTPClient(targetServer.Client())),
				false, nil,
			)

			res := checker.checkUsersProvisioned(context.Background())

			if res.Status != tt.wantStatus {
				t.Errorf("status = %s, want %s (%s)", res.Status, tt.wantStatus, res.Message)
			}
			combined := res.Message + " " + res.Detail
			if !strings.Contains(combined, tt.wantInMsg) {
				t.Errorf("expected %q in the result, got message=%q detail=%q", tt.wantInMsg, res.Message, res.Detail)
			}
		})
	}
}

// A warning must not block the migration — the operator can proceed — but it has
// to be reported separately so the UI can offer to re-check first.
func TestWarningsDoNotBlockButAreReported(t *testing.T) {
	checks := []Check{
		{ID: "a", Status: StatusPassed},
		{ID: "b", Status: StatusWarning},
	}
	if !AllPassed(checks) {
		t.Error("a warning should not block the migration")
	}
	if !HasWarnings(checks) {
		t.Error("expected HasWarnings to report the warning")
	}
	if HasFailures(checks) {
		t.Error("a warning is not a failure")
	}
}

func TestSourcePermissionsWarnsAboutReadsTheTokenIsRefused(t *testing.T) {
	// A read-only token cannot list webhooks or trusted IPs: Doppler has only
	// "manage" permissions for them. The gather carries on without them, and
	// pre-flight says so first.
	endpoints := validSourceEndpoints()
	endpoints["/v3/configs"] = mockEndpoint{http.StatusOK, map[string]interface{}{
		"configs": []map[string]string{{"name": "dev", "project": "proj-1", "environment": "dev"}},
	}}
	endpoints["/v3/webhooks"] = mockEndpoint{http.StatusForbidden, map[string]interface{}{"messages": []string{"Your workplace does not have access to Webhooks."}}}
	endpoints["/v3/configs/config/trusted_ips"] = mockEndpoint{http.StatusForbidden, map[string]interface{}{"messages": []string{"You do not have access to Trusted IPs."}}}
	endpoints["/v3/projects/project/members"] = mockEndpoint{http.StatusOK, map[string]interface{}{"members": []any{}}}
	endpoints["/v3/configs/config/tokens"] = mockEndpoint{http.StatusOK, map[string]interface{}{"tokens": []any{}}}
	endpoints["/v3/workplace/change_request_policies"] = mockEndpoint{http.StatusOK, map[string]interface{}{"policies": []any{}}}
	src := newMockDopplerServer(t, endpoints)
	defer src.Close()

	ch := NewChecker(doppler.NewClient(src.URL, "dp.sa.test"), nil, true, nil)
	res := ch.checkSourcePermissions(context.Background())
	if res.Status != StatusWarning {
		t.Fatalf("status %s: %s", res.Status, res.Message)
	}
	if res.Message != "Source token cannot read webhooks and trusted IP ranges" {
		t.Errorf("message %q", res.Message)
	}
	if !AllPassed([]Check{{Status: res.Status}}) {
		t.Error("a warning should not block")
	}
}
