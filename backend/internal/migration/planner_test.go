package migration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/doppler/onprem-migrator/internal/doppler"
)

func fullTestData() *MigrationData {
	expires := "2025-12-31T00:00:00Z"
	return &MigrationData{
		Workplace: &doppler.Workplace{ID: "wp1", Name: "Test Workplace"},
		WorkplaceUsers: []doppler.WorkplaceUser{
			{ID: "u1", User: struct {
				Email    string `json:"email"`
				Name     string `json:"name"`
				Username string `json:"username"`
			}{Email: "alice@test.com", Name: "Alice", Username: "alice"}},
			{ID: "u2", User: struct {
				Email    string `json:"email"`
				Name     string `json:"name"`
				Username string `json:"username"`
			}{Email: "bob@test.com", Name: "Bob", Username: "bob"}},
		},
		WorkplaceRoles: []doppler.Role{
			{Name: "custom-admin", IsCustomRole: true, Permissions: []string{"read", "write"}},
		},
		ProjectRoles: []doppler.Role{
			{Name: "deployer", IsCustomRole: true, Permissions: []string{"deploy"}},
		},
		Groups: []GroupWithMembers{
			{
				Group: doppler.Group{Name: "Engineering", Slug: "engineering"},
				Members: []doppler.GroupMember{
					{Type: "workplace_user", Slug: "u1"},
					{Type: "workplace_user", Slug: "u2"},
				},
			},
		},
		ServiceAccounts: []doppler.ServiceAccount{
			{Name: "CI Bot", Slug: "ci-bot"},
		},
		Integrations: []doppler.Integration{
			{Slug: "aws-sm", Name: "AWS Secrets Manager", Type: "aws_secrets_manager", Kind: "sync", Enabled: true, Syncs: []doppler.Sync{
				{Slug: "sync-1", Project: "backend", Config: "dev", Description: "Actions: dev sync", Integration: "aws-sm", Enabled: true, Status: "synced"},
			}},
			{Slug: "gh-actions", Name: "GitHub Actions", Type: "github_actions", Kind: "sync", Enabled: false, Syncs: []doppler.Sync{
				{Slug: "sync-2", Project: "backend", Config: "prd", Description: "Actions: prd sync", Integration: "gh-actions", Enabled: true, Status: "synced"},
			}},
		},
		ServiceTokens: []doppler.ServiceToken{
			{Name: "ci-token", Slug: "ci-token", Project: "backend", Config: "dev", ExpiresAt: &expires},
			{Name: "deploy-token", Slug: "deploy-token", Project: "backend", Config: "prd"},
		},
		Projects: []ProjectData{
			{
				Project: doppler.Project{Slug: "backend", Name: "Backend", Description: "API service"},
				Environments: []doppler.Environment{
					{Slug: "dev", Name: "Development"},
					{Slug: "prd", Name: "Production"},
				},
				Configs: []ConfigData{
					{
						Config:  doppler.Config{Name: "dev", Root: true, Environment: "dev", Project: "backend"},
						Secrets: doppler.SecretsMap{"KEY1": {Raw: "v1"}, "KEY2": {Raw: "v2"}},
					},
					{
						Config:     doppler.Config{Name: "prd", Root: true, Environment: "prd", Project: "backend", Locked: true, Inheritable: true},
						Secrets:    doppler.SecretsMap{"KEY1": {Raw: "${dev.KEY1}"}},
						TrustedIPs: []doppler.TrustedIP{{IP: "10.0.0.1/32"}},
					},
					{
						Config:  doppler.Config{Name: "prd_ci", Root: false, Environment: "prd", Project: "backend", Inherits: []doppler.ConfigDescriptor{{Project: "backend", Config: "prd"}}},
						Secrets: doppler.SecretsMap{"CI_KEY": {Raw: "ci-val"}},
					},
				},
				Webhooks: []doppler.Webhook{
					{ID: "wh1", URL: "https://hooks.example.com/deploy", Enabled: true},
				},
				Members: []doppler.ProjectMember{
					{Type: "workplace_user", Slug: "u1", Role: doppler.MemberRole{Identifier: "admin"}},
					{Type: "group", Slug: "engineering", Role: doppler.MemberRole{Identifier: "viewer"}},
				},
			},
		},
	}
}

func TestGeneratePlanComplete(t *testing.T) {
	data := fullTestData()
	plan := GeneratePlan(data)

	// Summary
	if plan.Summary.ProjectCount != 1 {
		t.Errorf("expected 1 project, got %d", plan.Summary.ProjectCount)
	}
	if plan.Summary.EnvironmentCount != 2 {
		t.Errorf("expected 2 environments, got %d", plan.Summary.EnvironmentCount)
	}
	if plan.Summary.ConfigCount != 3 {
		t.Errorf("expected 3 configs, got %d", plan.Summary.ConfigCount)
	}
	if plan.Summary.SecretCount != 4 {
		t.Errorf("expected 4 secrets, got %d", plan.Summary.SecretCount)
	}
	if plan.Summary.RoleCount != 2 {
		t.Errorf("expected 2 roles (1 wp + 1 proj), got %d", plan.Summary.RoleCount)
	}
	if plan.Summary.GroupCount != 1 {
		t.Errorf("expected 1 group, got %d", plan.Summary.GroupCount)
	}
	if plan.Summary.WebhookCount != 1 {
		t.Errorf("expected 1 webhook, got %d", plan.Summary.WebhookCount)
	}
	if plan.Summary.ServiceAccountCount != 1 {
		t.Errorf("expected 1 service account, got %d", plan.Summary.ServiceAccountCount)
	}
	if plan.Summary.TrustedIPCount != 1 {
		t.Errorf("expected 1 trusted IP, got %d", plan.Summary.TrustedIPCount)
	}
}

func TestGeneratePlanSummaryManualStepCount(t *testing.T) {
	data := fullTestData()
	plan := GeneratePlan(data)

	// 2 integrations + 2 syncs = 4 manual step items. The 2 service tokens are
	// the token page's, and are not counted here.
	if plan.Summary.ManualStepCount != 4 {
		t.Errorf("expected 4 manual step items, got %d", plan.Summary.ManualStepCount)
	}
}

func TestGeneratePlanWorkplace(t *testing.T) {
	data := fullTestData()
	plan := GeneratePlan(data)

	if !plan.Workplace.Settings {
		t.Error("expected settings=true")
	}
	if len(plan.Workplace.WorkplaceRoles) != 1 || plan.Workplace.WorkplaceRoles[0] != "custom-admin" {
		t.Errorf("unexpected workplace roles: %v", plan.Workplace.WorkplaceRoles)
	}
	if len(plan.Workplace.ProjectRoles) != 1 || plan.Workplace.ProjectRoles[0] != "deployer" {
		t.Errorf("unexpected project roles: %v", plan.Workplace.ProjectRoles)
	}
	if len(plan.Workplace.Groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(plan.Workplace.Groups))
	}
	if plan.Workplace.Groups[0].Name != "Engineering" || plan.Workplace.Groups[0].MemberCount != 2 {
		t.Errorf("unexpected group: %+v", plan.Workplace.Groups[0])
	}
	if len(plan.Workplace.ServiceAccounts) != 1 || plan.Workplace.ServiceAccounts[0] != "CI Bot" {
		t.Errorf("unexpected service accounts: %v", plan.Workplace.ServiceAccounts)
	}
}

func TestGeneratePlanProjects(t *testing.T) {
	data := fullTestData()
	plan := GeneratePlan(data)

	if len(plan.Projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(plan.Projects))
	}

	pp := plan.Projects[0]
	if pp.Name != "Backend" {
		t.Errorf("expected project name 'Backend', got %q", pp.Name)
	}
	if pp.Description != "API service" {
		t.Errorf("expected description 'API service', got %q", pp.Description)
	}

	// Environments
	if len(pp.Environments) != 2 {
		t.Errorf("expected 2 environments, got %d", len(pp.Environments))
	}

	// Configs
	if len(pp.Configs) != 3 {
		t.Fatalf("expected 3 configs, got %d", len(pp.Configs))
	}

	// dev config
	devCfg := pp.Configs[0]
	if devCfg.Name != "dev" || !devCfg.Root || devCfg.SecretCount != 2 {
		t.Errorf("unexpected dev config: %+v", devCfg)
	}

	// prd config
	prdCfg := pp.Configs[1]
	if prdCfg.Name != "prd" || !prdCfg.Root || !prdCfg.Locked || !prdCfg.Inheritable {
		t.Errorf("unexpected prd config: %+v", prdCfg)
	}
	if prdCfg.TrustedIPs != 1 {
		t.Errorf("expected 1 trusted IP in prd, got %d", prdCfg.TrustedIPs)
	}

	// prd_ci branch config
	branchCfg := pp.Configs[2]
	if branchCfg.Name != "prd_ci" || branchCfg.Root {
		t.Errorf("unexpected branch config: %+v", branchCfg)
	}
	if len(branchCfg.Inherits) != 1 || branchCfg.Inherits[0] != "prd" {
		t.Errorf("expected inherits=[prd], got %v", branchCfg.Inherits)
	}

	// Webhooks
	if len(pp.Webhooks) != 1 {
		t.Fatalf("expected 1 webhook, got %d", len(pp.Webhooks))
	}
	if pp.Webhooks[0].URL != "https://hooks.example.com/deploy" || !pp.Webhooks[0].Enabled {
		t.Errorf("unexpected webhook: %+v", pp.Webhooks[0])
	}

	// Members
	if len(pp.Members) != 2 {
		t.Fatalf("expected 2 members, got %d", len(pp.Members))
	}
	if pp.Members[0].Type != "workplace_user" || pp.Members[0].Slug != "u1" || pp.Members[0].Role != "admin" {
		t.Errorf("unexpected member 0: %+v", pp.Members[0])
	}
	if pp.Members[1].Type != "group" || pp.Members[1].Slug != "engineering" {
		t.Errorf("unexpected member 1: %+v", pp.Members[1])
	}
}

func TestGeneratePlanManualStepsIntegrations(t *testing.T) {
	data := fullTestData()
	plan := GeneratePlan(data)

	var integStep *ManualStep
	for i := range plan.ManualSteps {
		if plan.ManualSteps[i].Category == "integration" {
			integStep = &plan.ManualSteps[i]
			break
		}
	}

	if integStep == nil {
		t.Fatal("expected integration manual step")
	}
	if len(integStep.Items) != 2 {
		t.Fatalf("expected 2 integration items, got %d", len(integStep.Items))
	}
	if integStep.Items[0].Name != "AWS Secrets Manager" {
		t.Errorf("expected 'AWS Secrets Manager', got %q", integStep.Items[0].Name)
	}
	if integStep.Items[0].Context["type"] != "aws_secrets_manager" {
		t.Errorf("expected type 'aws_secrets_manager', got %q", integStep.Items[0].Context["type"])
	}
}

func TestGeneratePlanManualStepsServiceTokens(t *testing.T) {
	// Service tokens are issued or skipped on the token page, which cutover
	// waits on. Listing them as manual steps too would put the same work in two
	// places, with two ways of marking it done.
	data := fullTestData()
	plan := GeneratePlan(data)

	for _, ms := range plan.ManualSteps {
		if ms.Category == "service_token" || ms.Category == "service_account_token" {
			t.Fatalf("tokens should not be manual steps: %+v", ms)
		}
	}

	// What the manual step used to carry now reaches the token page's ledger:
	// the placement and the source expiry, which issuing relies on.
	ledger := NewTokenLedger("")
	ledger.Seed(data, time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	records := ledger.Records()
	if len(records) < 2 {
		t.Fatalf("expected both service tokens in the ledger, got %d", len(records))
	}
	if records[0].Project != "backend" {
		t.Errorf("expected project 'backend', got %q", records[0].Project)
	}
	if records[0].SourceExpiresAt == nil || !records[0].SourceExpiresAt.Equal(time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("expected the source expiry to carry over, got %v", records[0].SourceExpiresAt)
	}
	if records[1].SourceExpiresAt != nil {
		t.Error("expected no expiry for the second token")
	}
}

func TestGeneratePlanManualStepsSyncs(t *testing.T) {
	data := fullTestData()
	plan := GeneratePlan(data)

	var syncStep *ManualStep
	for i := range plan.ManualSteps {
		if plan.ManualSteps[i].Category == "sync" {
			syncStep = &plan.ManualSteps[i]
			break
		}
	}

	if syncStep == nil {
		t.Fatal("expected sync manual step")
	}
	if syncStep.Title != "Secrets Syncs" {
		t.Errorf("expected title 'Secrets Syncs', got %q", syncStep.Title)
	}
	// Should have 2 sync items (one from each integration)
	if len(syncStep.Items) != 2 {
		t.Fatalf("expected 2 sync items, got %d", len(syncStep.Items))
	}
	if syncStep.Items[0].Name != "Actions: dev sync" {
		t.Errorf("expected sync item name 'Actions: dev sync', got %q", syncStep.Items[0].Name)
	}
	if syncStep.Items[0].Context["project"] != "backend" {
		t.Errorf("expected project 'backend', got %q", syncStep.Items[0].Context["project"])
	}
	if syncStep.Items[0].Context["config"] != "dev" {
		t.Errorf("expected config 'dev', got %q", syncStep.Items[0].Context["config"])
	}
	if syncStep.Items[0].Context["integration"] != "AWS Secrets Manager" {
		t.Errorf("expected integration 'AWS Secrets Manager', got %q", syncStep.Items[0].Context["integration"])
	}
}

func TestGeneratePlanEmptyWorkplace(t *testing.T) {
	data := &MigrationData{
		Workplace: &doppler.Workplace{ID: "wp1", Name: "Empty"},
	}
	plan := GeneratePlan(data)

	if plan.Summary.ProjectCount != 0 {
		t.Errorf("expected 0 projects, got %d", plan.Summary.ProjectCount)
	}
	if plan.Summary.RoleCount != 0 {
		t.Errorf("expected 0 roles, got %d", plan.Summary.RoleCount)
	}
	if !plan.Workplace.Settings {
		t.Error("expected settings=true even for empty workplace")
	}
	if len(plan.Projects) != 0 {
		t.Errorf("expected 0 project plans, got %d", len(plan.Projects))
	}
	// Sync step should NOT appear when no integrations/syncs
	for _, ms := range plan.ManualSteps {
		if ms.Category == "sync" {
			t.Error("expected no sync manual step when no integrations with syncs")
		}
	}
}

func TestGeneratePlanNoIntegrations(t *testing.T) {
	data := &MigrationData{
		Workplace: &doppler.Workplace{ID: "wp1", Name: "Test"},
	}
	plan := GeneratePlan(data)

	for _, ms := range plan.ManualSteps {
		if ms.Category == "integration" {
			t.Error("expected no integration manual step when no integrations")
		}
	}
}

func TestGeneratePlanNoSyncs(t *testing.T) {
	data := &MigrationData{
		Workplace: &doppler.Workplace{ID: "wp1", Name: "Test"},
		Integrations: []doppler.Integration{
			{Slug: "aws-sm", Name: "AWS SM", Type: "aws_secrets_manager", Enabled: true},
		},
	}
	plan := GeneratePlan(data)

	for _, ms := range plan.ManualSteps {
		if ms.Category == "sync" {
			t.Error("expected no sync manual step when integrations have no syncs")
		}
	}
	// Integration step should still exist
	hasIntegStep := false
	for _, ms := range plan.ManualSteps {
		if ms.Category == "integration" {
			hasIntegStep = true
		}
	}
	if !hasIntegStep {
		t.Error("expected integration manual step")
	}
}

func TestGeneratePlanNoServiceTokens(t *testing.T) {
	data := &MigrationData{
		Workplace: &doppler.Workplace{ID: "wp1", Name: "Test"},
	}
	plan := GeneratePlan(data)

	for _, ms := range plan.ManualSteps {
		if ms.Category == "service_token" {
			t.Error("expected no service_token manual step when no tokens")
		}
	}
}

// --- User check tests ---

func TestCheckUsersAllExist(t *testing.T) {
	data := fullTestData()
	mc := newMockClient()
	mc.users = []doppler.WorkplaceUser{
		{ID: "t1", User: struct {
			Email    string `json:"email"`
			Name     string `json:"name"`
			Username string `json:"username"`
		}{Email: "alice@test.com", Username: "alice"}},
		{ID: "t2", User: struct {
			Email    string `json:"email"`
			Name     string `json:"name"`
			Username string `json:"username"`
		}{Email: "bob@test.com", Username: "bob"}},
	}

	result, err := CheckUsers(context.Background(), data, mc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Missing) != 0 {
		t.Errorf("expected 0 missing, got %d: %v", len(result.Missing), result.Missing)
	}
	if len(result.Existing) != 2 {
		t.Errorf("expected 2 existing, got %d", len(result.Existing))
	}
}

func TestCheckUsersSomeMissing(t *testing.T) {
	data := fullTestData()
	mc := newMockClient()
	// Only alice exists on target
	mc.users = []doppler.WorkplaceUser{
		{ID: "t1", User: struct {
			Email    string `json:"email"`
			Name     string `json:"name"`
			Username string `json:"username"`
		}{Email: "alice@test.com", Username: "alice"}},
	}

	result, err := CheckUsers(context.Background(), data, mc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Missing) != 1 {
		t.Fatalf("expected 1 missing, got %d: %v", len(result.Missing), result.Missing)
	}
	if result.Missing[0] != "bob@test.com" {
		t.Errorf("expected missing 'bob@test.com', got %q", result.Missing[0])
	}
	if len(result.Existing) != 1 {
		t.Errorf("expected 1 existing, got %d", len(result.Existing))
	}
}

func TestCheckUsersNoUsersRequired(t *testing.T) {
	data := &MigrationData{
		Workplace: &doppler.Workplace{ID: "wp1"},
		// No groups or project members
	}
	mc := newMockClient()

	result, err := CheckUsers(context.Background(), data, mc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Required) != 0 {
		t.Errorf("expected 0 required, got %d", len(result.Required))
	}
	if len(result.Missing) != 0 {
		t.Errorf("expected 0 missing, got %d", len(result.Missing))
	}
}

func TestCheckUsersTargetError(t *testing.T) {
	data := fullTestData()
	mc := newMockClient()
	mc.usersErr = fmt.Errorf("connection refused")

	_, err := CheckUsers(context.Background(), data, mc)
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); got != "check users on target: connection refused" {
		t.Errorf("unexpected error: %s", got)
	}
}

func TestCheckUsersDeduplicates(t *testing.T) {
	// alice appears in both a group and a project member (by user ID)
	data := &MigrationData{
		Workplace: &doppler.Workplace{ID: "wp1"},
		WorkplaceUsers: []doppler.WorkplaceUser{
			{ID: "u1", User: struct {
				Email    string `json:"email"`
				Name     string `json:"name"`
				Username string `json:"username"`
			}{Email: "alice@test.com", Username: "alice"}},
		},
		Groups: []GroupWithMembers{
			{
				Group:   doppler.Group{Name: "Team", Slug: "team"},
				Members: []doppler.GroupMember{{Type: "workplace_user", Slug: "u1"}},
			},
		},
		Projects: []ProjectData{
			{
				Members: []doppler.ProjectMember{
					{Type: "workplace_user", Slug: "u1", Role: doppler.MemberRole{Identifier: "admin"}},
				},
			},
		},
	}

	mc := newMockClient()
	mc.users = []doppler.WorkplaceUser{
		{ID: "t1", User: struct {
			Email    string `json:"email"`
			Name     string `json:"name"`
			Username string `json:"username"`
		}{Email: "alice@test.com", Username: "alice"}},
	}

	result, err := CheckUsers(context.Background(), data, mc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have alice only once
	if len(result.Required) != 1 {
		t.Errorf("expected 1 required (deduplicated), got %d: %v", len(result.Required), result.Required)
	}
}

func TestCheckUsersSkipsNonUserMembers(t *testing.T) {
	// group members that are service accounts should not be included
	data := &MigrationData{
		Workplace: &doppler.Workplace{ID: "wp1"},
		Groups: []GroupWithMembers{
			{
				Group: doppler.Group{Name: "Team", Slug: "team"},
				Members: []doppler.GroupMember{
					{Type: "workplace_user", Slug: "u1"},
					{Type: "service_account", Slug: "ci-bot"},
				},
			},
		},
		Projects: []ProjectData{
			{
				Members: []doppler.ProjectMember{
					{Type: "group", Slug: "team", Role: doppler.MemberRole{Identifier: "viewer"}},
				},
			},
		},
	}

	mc := newMockClient()
	result, err := CheckUsers(context.Background(), data, mc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Only u1 should be required (service_account and group types excluded)
	if len(result.Required) != 1 {
		t.Errorf("expected 1 required, got %d: %v", len(result.Required), result.Required)
	}

	if result.Required[0] != "u1" {
		t.Errorf("expected required[0]='u1', got %q", result.Required[0])
	}
}
