package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/doppler/onprem-migrator/internal/doppler"
	"github.com/doppler/onprem-migrator/internal/migration"
)

// tokenPtr builds the pointer the configure request now uses for token fields,
// where nil means "keep the token already held".
func tokenPtr(s string) *string { return &s }

func TestConfigureValidInput(t *testing.T) {
	s := NewServer()
	handler := newRouterWithServer(s, nil)

	body := configureRequest{
		SourceHost:  "https://api.doppler.com",
		SourceToken: tokenPtr("dp.ct.source123"),
		TargetHost:  "https://doppler.internal.com",
		TargetToken: tokenPtr("dp.pt.target456"),
		DevMode:     false,
	}
	bodyBytes, _ := json.Marshal(body)

	req := newAPIRequest("POST", "/api/v1/configure", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp configureResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if !resp.Valid {
		t.Errorf("expected valid=true, got errors: %v", resp.Errors)
	}

	// Verify config was stored
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.config == nil {
		t.Fatal("expected config to be stored")
	}
	if s.config.SourceHost != "https://api.doppler.com" {
		t.Errorf("expected source host stored, got %q", s.config.SourceHost)
	}
	if s.config.SourceToken != "dp.ct.source123" {
		t.Errorf("expected source token stored, got %q", s.config.SourceToken)
	}
}

func TestConfigureDevModeMinimalInput(t *testing.T) {
	s := NewServer()
	handler := newRouterWithServer(s, nil)

	body := configureRequest{
		SourceHost:  "https://api.doppler.com",
		SourceToken: tokenPtr("dp.ct.source123"),
		DevMode:     true,
	}
	bodyBytes, _ := json.Marshal(body)

	req := newAPIRequest("POST", "/api/v1/configure", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp configureResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if !resp.Valid {
		t.Errorf("expected valid=true, got errors: %v", resp.Errors)
	}
	if !resp.DevMode {
		t.Error("expected dev_mode=true in response")
	}
}

func TestConfigureMissingSourceToken(t *testing.T) {
	s := NewServer()
	handler := newRouterWithServer(s, nil)

	body := configureRequest{
		SourceHost:  "https://api.doppler.com",
		SourceToken: tokenPtr(""),
		TargetHost:  "https://doppler.internal.com",
		TargetToken: tokenPtr("dp.ct.target"),
	}
	bodyBytes, _ := json.Marshal(body)

	req := newAPIRequest("POST", "/api/v1/configure", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}

	var resp configureResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Valid {
		t.Error("expected valid=false")
	}
	if len(resp.Errors) == 0 {
		t.Error("expected at least one error")
	}
}

func TestConfigureInvalidTokenPrefix(t *testing.T) {
	s := NewServer()
	handler := newRouterWithServer(s, nil)

	body := configureRequest{
		SourceHost:  "https://api.doppler.com",
		SourceToken: tokenPtr("dp.st.servicetoken"),
		TargetHost:  "https://doppler.internal.com",
		TargetToken: tokenPtr("dp.ct.target"),
	}
	bodyBytes, _ := json.Marshal(body)

	req := newAPIRequest("POST", "/api/v1/configure", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}

	var resp configureResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Valid {
		t.Error("expected valid=false for service token prefix")
	}
}

func TestConfigureInvalidTargetURL(t *testing.T) {
	s := NewServer()
	handler := newRouterWithServer(s, nil)

	body := configureRequest{
		SourceHost:  "https://api.doppler.com",
		SourceToken: tokenPtr("dp.ct.source"),
		TargetHost:  "not-a-url",
		TargetToken: tokenPtr("dp.ct.target"),
	}
	bodyBytes, _ := json.Marshal(body)

	req := newAPIRequest("POST", "/api/v1/configure", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestConfigureMissingTargetWhenNotDevMode(t *testing.T) {
	s := NewServer()
	handler := newRouterWithServer(s, nil)

	body := configureRequest{
		SourceHost:  "https://api.doppler.com",
		SourceToken: tokenPtr("dp.ct.source"),
		DevMode:     false,
	}
	bodyBytes, _ := json.Marshal(body)

	req := newAPIRequest("POST", "/api/v1/configure", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}

	var resp configureResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if len(resp.Errors) < 2 {
		t.Errorf("expected at least 2 errors (host+token), got %d: %v", len(resp.Errors), resp.Errors)
	}
}

func TestConfigureMissingSourceHost(t *testing.T) {
	s := NewServer()
	handler := newRouterWithServer(s, nil)

	body := configureRequest{
		SourceToken: tokenPtr("dp.ct.source"),
		TargetHost:  "https://doppler.internal.com",
		TargetToken: tokenPtr("dp.ct.target"),
	}
	bodyBytes, _ := json.Marshal(body)

	req := newAPIRequest("POST", "/api/v1/configure", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}

	var resp configureResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Valid {
		t.Error("expected valid=false for missing source host")
	}
}

func TestConfigureInvalidSourceHostURL(t *testing.T) {
	s := NewServer()
	handler := newRouterWithServer(s, nil)

	body := configureRequest{
		SourceHost:  "not-a-url",
		SourceToken: tokenPtr("dp.ct.source"),
		TargetHost:  "https://doppler.internal.com",
		TargetToken: tokenPtr("dp.ct.target"),
	}
	bodyBytes, _ := json.Marshal(body)

	req := newAPIRequest("POST", "/api/v1/configure", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}

	var resp configureResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Valid {
		t.Error("expected valid=false for invalid source host URL")
	}
}

func TestConfigureInvalidJSON(t *testing.T) {
	s := NewServer()
	handler := newRouterWithServer(s, nil)

	req := newAPIRequest("POST", "/api/v1/configure", bytes.NewReader([]byte("not json")))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestPreflightWithoutConfigReturns400(t *testing.T) {
	s := NewServer()
	handler := newRouterWithServer(s, nil)

	req := newAPIRequest("POST", "/api/v1/preflight", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestValidateConfigHelpers(t *testing.T) {
	t.Run("valid token prefixes", func(t *testing.T) {
		if !isValidTokenPrefix("dp.ct.abc123") {
			t.Error("dp.ct. should be valid")
		}
		if !isValidTokenPrefix("dp.pt.abc123") {
			t.Error("dp.pt. should be valid")
		}
		if !isValidTokenPrefix("dp.sa.abc123") {
			t.Error("dp.sa. should be valid")
		}
	})

	t.Run("invalid token prefixes", func(t *testing.T) {
		if isValidTokenPrefix("dp.st.abc") {
			t.Error("dp.st. should be invalid")
		}
		if isValidTokenPrefix("dp.scim.abc") {
			t.Error("dp.scim. should be invalid")
		}
		if isValidTokenPrefix("random") {
			t.Error("random should be invalid")
		}
		if isValidTokenPrefix("") {
			t.Error("empty should be invalid")
		}
	})

	t.Run("valid URLs", func(t *testing.T) {
		if !isValidURL("https://doppler.internal.com") {
			t.Error("https URL should be valid")
		}
		if !isValidURL("http://localhost:8080") {
			t.Error("http localhost should be valid")
		}
	})

	t.Run("invalid URLs", func(t *testing.T) {
		if isValidURL("not-a-url") {
			t.Error("not-a-url should be invalid")
		}
		if isValidURL("ftp://host.com") {
			t.Error("ftp should be invalid")
		}
		if isValidURL("") {
			t.Error("empty should be invalid")
		}
	})
}

// --- Gather handler tests ---

func TestGatherWithoutConfigReturns400(t *testing.T) {
	s := NewServer()
	handler := newRouterWithServer(s, nil)

	req := newAPIRequest("POST", "/api/v1/gather", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestGatherWithConfig(t *testing.T) {
	s := NewServer()
	handler := newRouterWithServer(s, nil)

	configBody := configureRequest{
		SourceHost:  "https://api.doppler.com",
		SourceToken: tokenPtr("dp.ct.source123"),
		TargetHost:  "https://doppler.internal.com",
		TargetToken: tokenPtr("dp.pt.target456"),
	}
	bodyBytes, _ := json.Marshal(configBody)
	req := newAPIRequest("POST", "/api/v1/configure", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("configure: expected 200, got %d", rec.Code)
	}

	// The gather endpoint will try to connect to the real Doppler API,
	// which will fail in tests. We just verify it doesn't return 400 (config missing)
	// or 501 (not implemented).
	req = newAPIRequest("POST", "/api/v1/gather", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code == http.StatusBadRequest {
		t.Error("gather should not return 400 when config is set")
	}
	if rec.Code == http.StatusNotImplemented {
		t.Error("gather should not return 501 (implemented)")
	}
}

// --- Plan handler tests ---

func TestPlanWithoutGatherReturns400(t *testing.T) {
	s := NewServer()
	handler := newRouterWithServer(s, nil)

	req := newAPIRequest("GET", "/api/v1/plan", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestPlanWithStoredPlan(t *testing.T) {
	s := NewServer()

	// Build test data and generate plan
	data := &migration.MigrationData{
		Workplace: &doppler.Workplace{ID: "wp1", Name: "Test WP"},
		WorkplaceUsers: []doppler.WorkplaceUser{
			{ID: "u1"},
			{ID: "u2"},
		},
		WorkplaceRoles: []doppler.Role{
			{Name: "custom-admin", IsCustomRole: true},
		},
		ProjectRoles: []doppler.Role{
			{Name: "deployer", IsCustomRole: true},
		},
		Groups: []migration.GroupWithMembers{
			{
				Group:   doppler.Group{Name: "Engineering", Slug: "engineering"},
				Members: []doppler.GroupMember{{Type: "workplace_user", Slug: "alice"}},
			},
		},
		ServiceAccounts: []doppler.ServiceAccount{
			{Name: "CI Bot", Slug: "ci-bot"},
		},
		Integrations: []doppler.Integration{
			{Name: "AWS SM", Type: "aws_secrets_manager"},
		},
		ServiceTokens: []doppler.ServiceToken{
			{Name: "ci-token", Project: "backend", Config: "dev"},
			{Name: "deploy-token", Project: "backend", Config: "prd"},
		},
		Projects: []migration.ProjectData{
			{
				Project: doppler.Project{Slug: "backend", Name: "Backend", Description: "API service"},
				Environments: []doppler.Environment{
					{Slug: "dev", Name: "Development"},
					{Slug: "prd", Name: "Production"},
				},
				Configs: []migration.ConfigData{
					{
						Config:  doppler.Config{Name: "dev", Root: true, Environment: "dev", Project: "backend"},
						Secrets: doppler.SecretsMap{"KEY": {Raw: "val"}},
					},
					{
						Config:     doppler.Config{Name: "prd", Root: true, Environment: "prd", Project: "backend", Locked: true},
						Secrets:    doppler.SecretsMap{"KEY": {Raw: "prd-val"}, "DB": {Raw: "conn"}},
						TrustedIPs: []doppler.TrustedIP{{IP: "10.0.0.1/32"}},
					},
				},
				Webhooks: []doppler.Webhook{{ID: "wh1", URL: "https://hooks.example.com", Enabled: true}},
				Members:  []doppler.ProjectMember{{Type: "workplace_user", Slug: "alice", Role: doppler.MemberRole{Identifier: "admin"}}},
			},
		},
	}

	s.migrationData = data
	s.plan = migration.GeneratePlan(data)

	handler := newRouterWithServer(s, nil)

	req := newAPIRequest("GET", "/api/v1/plan", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var plan migration.MigrationPlan
	if err := json.NewDecoder(rec.Body).Decode(&plan); err != nil {
		t.Fatalf("failed to decode plan: %v", err)
	}

	// Summary
	if plan.Summary.ProjectCount != 1 {
		t.Errorf("expected 1 project, got %d", plan.Summary.ProjectCount)
	}
	if plan.Summary.EnvironmentCount != 2 {
		t.Errorf("expected 2 environments, got %d", plan.Summary.EnvironmentCount)
	}
	if plan.Summary.ConfigCount != 2 {
		t.Errorf("expected 2 configs, got %d", plan.Summary.ConfigCount)
	}
	if plan.Summary.SecretCount != 3 {
		t.Errorf("expected 3 secrets, got %d", plan.Summary.SecretCount)
	}
	if plan.Summary.TrustedIPCount != 1 {
		t.Errorf("expected 1 trusted IP, got %d", plan.Summary.TrustedIPCount)
	}
	if plan.Summary.RoleCount != 2 {
		t.Errorf("expected 2 roles, got %d", plan.Summary.RoleCount)
	}
	if plan.Summary.GroupCount != 1 {
		t.Errorf("expected 1 group, got %d", plan.Summary.GroupCount)
	}

	// Workplace plan
	if !plan.Workplace.Settings {
		t.Error("expected workplace settings=true")
	}
	if len(plan.Workplace.WorkplaceRoles) != 1 || plan.Workplace.WorkplaceRoles[0] != "custom-admin" {
		t.Errorf("unexpected workplace roles: %v", plan.Workplace.WorkplaceRoles)
	}
	if len(plan.Workplace.Groups) != 1 || plan.Workplace.Groups[0].MemberCount != 1 {
		t.Errorf("unexpected groups: %v", plan.Workplace.Groups)
	}

	// Project plan
	if len(plan.Projects) != 1 {
		t.Fatalf("expected 1 project plan, got %d", len(plan.Projects))
	}
	pp := plan.Projects[0]
	if pp.Name != "Backend" {
		t.Errorf("expected project name 'Backend', got %q", pp.Name)
	}
	if len(pp.Environments) != 2 {
		t.Errorf("expected 2 environments, got %d", len(pp.Environments))
	}
	if len(pp.Configs) != 2 {
		t.Errorf("expected 2 configs, got %d", len(pp.Configs))
	}
	if pp.Configs[1].Locked != true {
		t.Error("expected prd config to be locked")
	}
	if pp.Configs[1].SecretCount != 2 {
		t.Errorf("expected 2 secrets in prd, got %d", pp.Configs[1].SecretCount)
	}
	if pp.Configs[1].TrustedIPs != 1 {
		t.Errorf("expected 1 trusted IP in prd, got %d", pp.Configs[1].TrustedIPs)
	}
	if len(pp.Webhooks) != 1 {
		t.Errorf("expected 1 webhook, got %d", len(pp.Webhooks))
	}
	if len(pp.Members) != 1 {
		t.Errorf("expected 1 member, got %d", len(pp.Members))
	}

	// Manual steps: just the integration. Service tokens are issued or skipped on
	// the token page now, so they are not listed here as well.
	if len(plan.ManualSteps) != 1 {
		t.Fatalf("expected 1 manual step, got %d: %+v", len(plan.ManualSteps), plan.ManualSteps)
	}
	if plan.ManualSteps[0].Category != "integration" {
		t.Errorf("expected the manual step category=integration, got %q", plan.ManualSteps[0].Category)
	}
}

// --- Manual steps handler tests ---

func TestManualStepsWithoutPlanReturns400(t *testing.T) {
	s := NewServer()
	handler := newRouterWithServer(s, nil)

	req := newAPIRequest("GET", "/api/v1/report/manual-steps", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestManualStepsWithPlan(t *testing.T) {
	s := NewServer()
	data := &migration.MigrationData{
		Workplace: &doppler.Workplace{ID: "wp1"},
		Integrations: []doppler.Integration{
			{Name: "AWS SM", Type: "aws_secrets_manager"},
		},
	}
	s.plan = migration.GeneratePlan(data)

	handler := newRouterWithServer(s, nil)

	req := newAPIRequest("GET", "/api/v1/report/manual-steps", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp struct {
		ManualSteps []migration.ManualStep    `json:"manual_steps"`
		UserCheck   migration.UserCheckResult `json:"user_check"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	hasInteg := false
	for _, ms := range resp.ManualSteps {
		if ms.Category == "integration" {
			hasInteg = true
		}
	}
	if !hasInteg {
		t.Error("expected integration manual step in response")
	}
}

// --- Service token report handler tests ---

func TestServiceTokensWithoutDataReturns400(t *testing.T) {
	s := NewServer()
	handler := newRouterWithServer(s, nil)

	req := newAPIRequest("GET", "/api/v1/report/service-tokens", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestServiceTokensWithData(t *testing.T) {
	s := NewServer()
	expires := "2025-12-31T00:00:00Z"
	s.migrationData = &migration.MigrationData{
		Workplace: &doppler.Workplace{ID: "wp1"},
		ServiceTokens: []doppler.ServiceToken{
			{
				Name:        "ci-token",
				Slug:        "ci-token",
				CreatedAt:   "2024-01-01T00:00:00Z",
				ExpiresAt:   &expires,
				Project:     "backend",
				Config:      "dev",
				Environment: "dev",
				Access:      "read",
			},
			{
				Name:        "deploy-token",
				Slug:        "deploy-token",
				CreatedAt:   "2024-02-01T00:00:00Z",
				ExpiresAt:   nil,
				Project:     "backend",
				Config:      "prd",
				Environment: "prd",
				Access:      "read/write",
			},
		},
	}

	handler := newRouterWithServer(s, nil)

	req := newAPIRequest("GET", "/api/v1/report/service-tokens", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp struct {
		ServiceTokens []doppler.ServiceToken `json:"service_tokens"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	if len(resp.ServiceTokens) != 2 {
		t.Fatalf("expected 2 service tokens, got %d", len(resp.ServiceTokens))
	}
	if resp.ServiceTokens[0].Name != "ci-token" {
		t.Errorf("expected first token name 'ci-token', got %q", resp.ServiceTokens[0].Name)
	}
	if resp.ServiceTokens[0].Project != "backend" {
		t.Errorf("expected project 'backend', got %q", resp.ServiceTokens[0].Project)
	}
	if resp.ServiceTokens[1].ExpiresAt != nil {
		t.Error("expected second token to have nil expires_at")
	}
}

// --- Summary report handler tests ---

func TestSummaryWithoutPlanReturns400(t *testing.T) {
	s := NewServer()
	handler := newRouterWithServer(s, nil)

	req := newAPIRequest("GET", "/api/v1/report/summary", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestSummaryWithPlan(t *testing.T) {
	s := NewServer()
	data := &migration.MigrationData{
		Workplace: &doppler.Workplace{ID: "wp1"},
		Projects: []migration.ProjectData{
			{
				Project: doppler.Project{Slug: "api", Name: "API"},
				Environments: []doppler.Environment{
					{Slug: "dev", Name: "Development"},
				},
				Configs: []migration.ConfigData{
					{
						Config:  doppler.Config{Name: "dev", Root: true, Environment: "dev", Project: "api"},
						Secrets: doppler.SecretsMap{"KEY": {Raw: "val"}},
					},
				},
			},
		},
	}
	s.plan = migration.GeneratePlan(data)

	handler := newRouterWithServer(s, nil)

	req := newAPIRequest("GET", "/api/v1/report/summary", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp struct {
		Summary         migration.PlanSummary `json:"summary"`
		Checkpoint      interface{}           `json:"checkpoint"`
		ManualStepCount int                   `json:"manual_step_count"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	if resp.Summary.ProjectCount != 1 {
		t.Errorf("expected 1 project, got %d", resp.Summary.ProjectCount)
	}
	if resp.Summary.SecretCount != 1 {
		t.Errorf("expected 1 secret, got %d", resp.Summary.SecretCount)
	}
	if resp.Checkpoint != nil {
		t.Error("expected checkpoint to be nil when no migration has run")
	}
}

func TestSummaryWithCheckpoint(t *testing.T) {
	s := NewServer()
	data := &migration.MigrationData{
		Workplace: &doppler.Workplace{ID: "wp1"},
	}
	s.plan = migration.GeneratePlan(data)

	// Create a checkpoint manager with some data
	cm := migration.NewCheckpointManager("test-checkpoint.json")
	cm.InitPhase("phase1")
	cm.CompletePhase("phase1")
	s.checkpoint = cm

	handler := newRouterWithServer(s, nil)

	req := newAPIRequest("GET", "/api/v1/report/summary", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp struct {
		Summary    migration.PlanSummary  `json:"summary"`
		Checkpoint map[string]interface{} `json:"checkpoint"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	if resp.Checkpoint == nil {
		t.Fatal("expected checkpoint to be present")
	}
	phases, ok := resp.Checkpoint["phases"].(map[string]interface{})
	if !ok {
		t.Fatal("expected phases in checkpoint")
	}
	if _, ok := phases["phase1"]; !ok {
		t.Error("expected phase1 in checkpoint phases")
	}
}

// --- Migrate handler tests ---

func TestMigrateWithoutConfigReturns400(t *testing.T) {
	s := NewServer()
	handler := newRouterWithServer(s, nil)

	req := newAPIRequest("POST", "/api/v1/migrate", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestMigrateWithoutDataReturns400(t *testing.T) {
	s := NewServer()
	s.config = &migration.MigrationConfig{
		SourceToken: "dp.ct.src",
		DevMode:     true,
	}
	handler := newRouterWithServer(s, nil)

	req := newAPIRequest("POST", "/api/v1/migrate", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

// --- Pause handler tests ---

func TestPauseWithoutMigrationReturns400(t *testing.T) {
	s := NewServer()
	handler := newRouterWithServer(s, nil)

	req := newAPIRequest("POST", "/api/v1/migrate/pause", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

// --- Resume handler tests ---

func TestResumeWithoutMigrationReturns400(t *testing.T) {
	s := NewServer()
	handler := newRouterWithServer(s, nil)

	req := newAPIRequest("POST", "/api/v1/migrate/resume", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

// --- Status handler tests ---

func TestStatusReturnsJSON(t *testing.T) {
	s := NewServer()
	handler := newRouterWithServer(s, nil)

	req := newAPIRequest("GET", "/api/v1/status", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp statusResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	if resp.Migrating {
		t.Error("expected migrating=false when no migration running")
	}
	if resp.Paused {
		t.Error("expected paused=false when no migration running")
	}
}

// --- Checkpoint handler tests ---

func TestCheckpointWithoutMigrationReturnsEmpty(t *testing.T) {
	s := NewServer()
	handler := newRouterWithServer(s, nil)

	req := newAPIRequest("GET", "/api/v1/checkpoint", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	if resp["completed"] != false {
		t.Error("expected completed=false in empty checkpoint")
	}
}

func TestCheckpointPathHonorsEnv(t *testing.T) {
	tests := []struct {
		name     string
		env      string
		setEnv   bool
		wantPath string
	}{
		{"defaults to ./data when unset", "", false, filepath.Join("data", "migration-checkpoint.json")},
		{"defaults to ./data when empty", "", true, filepath.Join("data", "migration-checkpoint.json")},
		{"defaults to ./data when blank", "   ", true, filepath.Join("data", "migration-checkpoint.json")},
		{"honors an absolute dir", "/data", true, filepath.Join("/data", "migration-checkpoint.json")},
		{"honors a relative dir", "tmp/ckpt", true, filepath.Join("tmp/ckpt", "migration-checkpoint.json")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setEnv {
				t.Setenv("CHECKPOINT_DIR", tt.env)
			} else {
				os.Unsetenv("CHECKPOINT_DIR")
			}

			got := checkpointPath()
			if got != tt.wantPath {
				t.Errorf("checkpointPath() = %q, want %q", got, tt.wantPath)
			}

			// NewServer must resolve it the same way.
			if s := NewServer(); s.checkpointPath != tt.wantPath {
				t.Errorf("NewServer().checkpointPath = %q, want %q", s.checkpointPath, tt.wantPath)
			}
		})
	}
}

// The checkpoint must land in CHECKPOINT_DIR, creating the directory if needed —
// otherwise the documented Docker volume at /data persists nothing.
func TestCheckpointWrittenToConfiguredDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "checkpoints")
	t.Setenv("CHECKPOINT_DIR", dir)

	s := NewServer()
	want := filepath.Join(dir, "migration-checkpoint.json")
	if s.checkpointPath != want {
		t.Fatalf("checkpointPath = %q, want %q", s.checkpointPath, want)
	}

	cm := migration.NewCheckpointManager(s.checkpointPath)
	cm.InitPhase("configs")
	cm.SetItemStatus("configs", "config:app/dev", migration.StatusCompleted, "")
	if err := cm.Save(); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	if _, err := os.Stat(want); err != nil {
		t.Fatalf("checkpoint not written to %s: %v", want, err)
	}

	// And it must be loadable again, which is what pause/resume depends on.
	reloaded := migration.NewCheckpointManager(want)
	if err := reloaded.Load(); err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if got := reloaded.GetItemStatus("configs", "config:app/dev"); got != migration.StatusCompleted {
		t.Errorf("reloaded item status = %q, want %q", got, migration.StatusCompleted)
	}
}

func TestTokenHint(t *testing.T) {
	tests := []struct {
		name  string
		token string
		want  string
	}{
		{"cli token", "dp.ct.abcdefghijklmnop1234", "dp.ct.…1234"},
		{"personal token", "dp.pt.abcdefghijklmnop5678", "dp.pt.…5678"},
		{"service token", "dp.st.abcdefghijklmnopwxyz", "dp.st.…wxyz"},
		{"service account token", "dp.sa.abcdefghijklmnopABCD", "dp.sa.…ABCD"},
		{"service account identity is not shadowed by dp.sa.", "dp.said.abcdefghijklmn9999", "dp.said.…9999"},
		{"scim token", "dp.scim.abcdefghijklmnop0001", "dp.scim.…0001"},
		{"audit token", "dp.audit.abcdefghijklmnop0002", "dp.audit.…0002"},
		{"unrecognized format still masks", "someothertoken9876", "…9876"},
		{"too short to reveal four", "dp.ct.abc", "dp.ct.…"},
		{"empty", "", ""},
		{"whitespace only", "   ", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tokenHint(tt.token); got != tt.want {
				t.Errorf("tokenHint(%q) = %q, want %q", tt.token, got, tt.want)
			}
		})
	}
}

// The configure endpoint must never hand back a token value: it holds a source token
// with workplace-wide read and a target token with write access.
func TestGetConfigureNeverReturnsTokenValues(t *testing.T) {
	const sourceToken = "dp.ct.sourcesecretvalue1111"
	const targetToken = "dp.pt.targetsecretvalue2222"

	s := NewServer()
	s.config = &migration.MigrationConfig{
		SourceHost:  "https://api.doppler.com",
		SourceToken: sourceToken,
		TargetHost:  "https://doppler.internal.com",
		TargetToken: targetToken,
	}
	handler := newRouterWithServer(s, nil)

	req := newAPIRequest("GET", "/api/v1/configure", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	raw := rec.Body.String()
	for _, secret := range []string{sourceToken, targetToken, "sourcesecretvalue1111", "targetsecretvalue2222"} {
		if strings.Contains(raw, secret) {
			t.Fatalf("response leaked token material %q: %s", secret, raw)
		}
	}

	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}

	var resp getConfigureResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.HasSourceToken || !resp.HasTargetToken {
		t.Error("expected has_*_token to be true")
	}
	if resp.SourceTokenHint != "dp.ct.…1111" {
		t.Errorf("source hint = %q, want dp.ct.…1111", resp.SourceTokenHint)
	}
	if resp.TargetTokenHint != "dp.pt.…2222" {
		t.Errorf("target hint = %q, want dp.pt.…2222", resp.TargetTokenHint)
	}
}

// Omitting a token on re-submit keeps the stored one, so the UI can save the form
// without ever having been given the value.
func TestConfigureOmittedTokenKeepsStored(t *testing.T) {
	s := NewServer()
	handler := newRouterWithServer(s, nil)

	post := func(body configureRequest) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		req := newAPIRequest("POST", "/api/v1/configure", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	if rec := post(configureRequest{
		SourceHost:  "https://api.doppler.com",
		SourceToken: tokenPtr("dp.ct.originalsource1111"),
		TargetHost:  "https://doppler.internal.com",
		TargetToken: tokenPtr("dp.pt.originaltarget2222"),
	}); rec.Code != http.StatusOK {
		t.Fatalf("initial configure: expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}

	// Re-submit with hosts only — tokens omitted entirely.
	if rec := post(configureRequest{
		SourceHost: "https://api.doppler.com",
		TargetHost: "https://doppler.internal.com",
	}); rec.Code != http.StatusOK {
		t.Fatalf("re-configure: expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.config.SourceToken != "dp.ct.originalsource1111" {
		t.Errorf("source token = %q, want the stored one", s.config.SourceToken)
	}
	if s.config.TargetToken != "dp.pt.originaltarget2222" {
		t.Errorf("target token = %q, want the stored one", s.config.TargetToken)
	}
}

func TestConfigureOmittedTokenWithNothingStoredFails(t *testing.T) {
	s := NewServer()
	handler := newRouterWithServer(s, nil)

	b, _ := json.Marshal(configureRequest{
		SourceHost: "https://api.doppler.com",
		TargetHost: "https://doppler.internal.com",
	})
	req := newAPIRequest("POST", "/api/v1/configure", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Source token is required") {
		t.Errorf("expected a required-token error, got %s", rec.Body.String())
	}
}

// DEV_MODE in the environment pins the session to a dry run regardless of what the
// request asks for. docker-compose.yml deliberately does not set it.
func TestDevModeForcedByEnv(t *testing.T) {
	t.Setenv("DEV_MODE", "true")

	s := NewServer()
	if !s.devModeForced {
		t.Fatal("expected devModeForced to be true")
	}
	handler := newRouterWithServer(s, nil)

	// No target host or token, and dev_mode false — accepted because the env forces it.
	b, _ := json.Marshal(configureRequest{
		SourceHost:  "https://api.doppler.com",
		SourceToken: tokenPtr("dp.ct.source1234"),
		DevMode:     false,
	})
	req := newAPIRequest("POST", "/api/v1/configure", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var resp configureResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if !resp.DevMode {
		t.Error("expected dev_mode true in the response")
	}

	s.mu.RLock()
	stored := s.config.DevMode
	s.mu.RUnlock()
	if !stored {
		t.Error("expected the stored config to be in dev mode")
	}

	// And GET reports it as forced so the UI can lock the control.
	getReq := newAPIRequest("GET", "/api/v1/configure", nil)
	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, getReq)
	var got getConfigureResponse
	json.Unmarshal(getRec.Body.Bytes(), &got)
	if !got.DevModeForced {
		t.Error("expected dev_mode_forced true")
	}
}

func TestDevModeNotForcedWhenEnvUnset(t *testing.T) {
	os.Unsetenv("DEV_MODE")
	if NewServer().devModeForced {
		t.Error("expected devModeForced false when DEV_MODE is unset")
	}
	for _, v := range []string{"", "false", "0", "no"} {
		t.Setenv("DEV_MODE", v)
		if NewServer().devModeForced {
			t.Errorf("DEV_MODE=%q should not force dev mode", v)
		}
	}
}

// A project an earlier run migrated and that was deleted from the target since
// goes back through the migration engine, which builds it whole.
func TestAnEarlierProjectMissingFromTheTargetIsMigratedAgain(t *testing.T) {
	kept, remaining, missing := splitMissing(
		[]string{"api", "something-new", "web"},
		nil,
		[]doppler.Project{{Slug: "api"}, {Slug: "web"}},
	)
	if len(kept) != 2 || kept[0] != "api" || kept[1] != "web" {
		t.Fatalf("kept %v", kept)
	}
	if len(remaining) != 1 || remaining[0] != "something-new" || len(missing) != 1 {
		t.Fatalf("remaining %v, missing %v", remaining, missing)
	}
}
