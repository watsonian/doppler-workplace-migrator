package migration

import (
	"context"
	"testing"

	"github.com/doppler/workplace-migrator/internal/doppler"
)

func TestGatherToPlanSummaryCounts(t *testing.T) {
	mc := fullMockClient()
	g := NewGatherer(mc, nil)

	data, err := g.GatherAll(context.Background())
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	plan := GeneratePlan(data)

	// Verify plan summary matches gathered data
	if plan.Summary.ProjectCount != len(data.Projects) {
		t.Errorf("project_count: expected %d, got %d", len(data.Projects), plan.Summary.ProjectCount)
	}

	expectedEnvs := 0
	expectedConfigs := 0
	expectedSecrets := 0
	expectedWebhooks := 0
	expectedTrustedIPs := 0
	for _, p := range data.Projects {
		expectedEnvs += len(p.Environments)
		expectedConfigs += len(p.Configs)
		for _, c := range p.Configs {
			expectedSecrets += len(c.Secrets)
			expectedTrustedIPs += len(c.TrustedIPs)
		}
		expectedWebhooks += len(p.Webhooks)
	}

	if plan.Summary.EnvironmentCount != expectedEnvs {
		t.Errorf("environment_count: expected %d, got %d", expectedEnvs, plan.Summary.EnvironmentCount)
	}
	if plan.Summary.ConfigCount != expectedConfigs {
		t.Errorf("config_count: expected %d, got %d", expectedConfigs, plan.Summary.ConfigCount)
	}
	if plan.Summary.SecretCount != expectedSecrets {
		t.Errorf("secret_count: expected %d, got %d", expectedSecrets, plan.Summary.SecretCount)
	}
	if plan.Summary.WebhookCount != expectedWebhooks {
		t.Errorf("webhook_count: expected %d, got %d", expectedWebhooks, plan.Summary.WebhookCount)
	}
	if plan.Summary.TrustedIPCount != expectedTrustedIPs {
		t.Errorf("trusted_ip_count: expected %d, got %d", expectedTrustedIPs, plan.Summary.TrustedIPCount)
	}

	expectedRoles := len(data.WorkplaceRoles) + len(data.ProjectRoles)
	if plan.Summary.RoleCount != expectedRoles {
		t.Errorf("role_count: expected %d, got %d", expectedRoles, plan.Summary.RoleCount)
	}
	if plan.Summary.GroupCount != len(data.Groups) {
		t.Errorf("group_count: expected %d, got %d", len(data.Groups), plan.Summary.GroupCount)
	}
	if plan.Summary.ServiceAccountCount != len(data.ServiceAccounts) {
		t.Errorf("service_account_count: expected %d, got %d", len(data.ServiceAccounts), plan.Summary.ServiceAccountCount)
	}
}

func TestGatherToPlanManualSteps(t *testing.T) {
	mc := fullMockClient()
	g := NewGatherer(mc, nil)

	data, err := g.GatherAll(context.Background())
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	plan := GeneratePlan(data)

	// Verify manual step categories
	categoryCounts := make(map[string]int)
	for _, ms := range plan.ManualSteps {
		categoryCounts[ms.Category] = len(ms.Items)
	}

	// Source has 1 integration
	if categoryCounts["integration"] != 1 {
		t.Errorf("integration items: expected 1, got %d", categoryCounts["integration"])
	}

	// Source has 1 service token (ci-token for backend/dev), which the token page
	// handles rather than the manual steps.
	if categoryCounts["service_token"] != 0 {
		t.Errorf("service_token items: expected none in the manual steps, got %d", categoryCounts["service_token"])
	}

	// Source integration has 1 sync
	if categoryCounts["sync"] != 1 {
		t.Errorf("sync items: expected 1, got %d", categoryCounts["sync"])
	}

	// ManualStepCount should be total items across all categories
	expectedManualCount := categoryCounts["integration"] + categoryCounts["sync"]
	if plan.Summary.ManualStepCount != expectedManualCount {
		t.Errorf("manual_step_count: expected %d, got %d", expectedManualCount, plan.Summary.ManualStepCount)
	}
}

func TestGatherCheckUsersIntegration(t *testing.T) {
	mc := fullMockClient()
	g := NewGatherer(mc, nil)

	data, err := g.GatherAll(context.Background())
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	// Target with only alice — bob should be missing
	targetMock := newMockClient()
	targetMock.users = []doppler.WorkplaceUser{
		{ID: "t1", User: struct {
			Email    string `json:"email"`
			Name     string `json:"name"`
			Username string `json:"username"`
		}{Email: "alice@test.com", Username: "alice"}},
	}

	result, err := CheckUsers(context.Background(), data, targetMock)
	if err != nil {
		t.Fatalf("check users: %v", err)
	}

	// alice is in the engineering group → required
	// bob is in engineering + devops groups → required
	if len(result.Required) != 2 {
		t.Errorf("expected 2 required users, got %d: %v", len(result.Required), result.Required)
	}

	// alice exists on target
	if len(result.Existing) != 1 {
		t.Errorf("expected 1 existing, got %d: %v", len(result.Existing), result.Existing)
	}

	// bob missing from target
	if len(result.Missing) != 1 {
		t.Errorf("expected 1 missing, got %d: %v", len(result.Missing), result.Missing)
	}

	// Verify the missing user is bob (resolved to email via source user ID lookup)
	foundBob := false
	for _, m := range result.Missing {
		if m == "bob@test.com" {
			foundBob = true
		}
	}
	if !foundBob {
		t.Errorf("expected bob@test.com in missing, got %v", result.Missing)
	}
}

func TestGatherToPlanEmptyWorkspace(t *testing.T) {
	mc := newMockClient()
	mc.workplace = &doppler.Workplace{ID: "wp1", Name: "Empty Corp"}

	g := NewGatherer(mc, nil)
	data, err := g.GatherAll(context.Background())
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	plan := GeneratePlan(data)

	// All counts should be zero
	if plan.Summary.ProjectCount != 0 {
		t.Errorf("expected 0 projects, got %d", plan.Summary.ProjectCount)
	}
	if plan.Summary.EnvironmentCount != 0 {
		t.Errorf("expected 0 environments, got %d", plan.Summary.EnvironmentCount)
	}
	if plan.Summary.ConfigCount != 0 {
		t.Errorf("expected 0 configs, got %d", plan.Summary.ConfigCount)
	}
	if plan.Summary.SecretCount != 0 {
		t.Errorf("expected 0 secrets, got %d", plan.Summary.SecretCount)
	}
	if plan.Summary.RoleCount != 0 {
		t.Errorf("expected 0 roles, got %d", plan.Summary.RoleCount)
	}
	if plan.Summary.GroupCount != 0 {
		t.Errorf("expected 0 groups, got %d", plan.Summary.GroupCount)
	}
	if plan.Summary.WebhookCount != 0 {
		t.Errorf("expected 0 webhooks, got %d", plan.Summary.WebhookCount)
	}
	if plan.Summary.ServiceAccountCount != 0 {
		t.Errorf("expected 0 service accounts, got %d", plan.Summary.ServiceAccountCount)
	}
	if plan.Summary.TrustedIPCount != 0 {
		t.Errorf("expected 0 trusted IPs, got %d", plan.Summary.TrustedIPCount)
	}

	// Only sync manual step (no integrations or service tokens)
	if plan.Summary.ManualStepCount != 0 {
		t.Errorf("expected 0 manual step items, got %d", plan.Summary.ManualStepCount)
	}

	// Workplace plan should still have settings=true
	if !plan.Workplace.Settings {
		t.Error("expected settings=true even for empty workspace")
	}

	// No projects in plan
	if len(plan.Projects) != 0 {
		t.Errorf("expected 0 project plans, got %d", len(plan.Projects))
	}
}
