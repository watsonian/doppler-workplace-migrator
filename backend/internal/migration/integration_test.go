package migration

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/doppler/onprem-migrator/internal/doppler"
)

// multiProjectData creates a realistic dataset with multiple projects, environments, configs, and secrets.
func multiProjectData() *MigrationData {
	return &MigrationData{
		Workplace: &doppler.Workplace{ID: "wp1", Name: "Acme Corp", BillingEmail: "billing@acme.com"},
		WorkplaceUsers: []doppler.WorkplaceUser{
			{ID: "u1", User: struct {
				Email    string `json:"email"`
				Name     string `json:"name"`
				Username string `json:"username"`
			}{Email: "alice@acme.com", Name: "Alice", Username: "alice"}},
			{ID: "u2", User: struct {
				Email    string `json:"email"`
				Name     string `json:"name"`
				Username string `json:"username"`
			}{Email: "bob@acme.com", Name: "Bob", Username: "bob"}},
		},
		WorkplaceRoles: []doppler.Role{
			{Name: "security-admin", Permissions: []string{"read", "write", "audit"}, IsCustomRole: true},
			{Name: "read-only", Permissions: []string{"read"}, IsCustomRole: true},
		},
		ProjectRoles: []doppler.Role{
			{Name: "deployer", Permissions: []string{"deploy"}, IsCustomRole: true},
		},
		Groups: []GroupWithMembers{
			{
				Group:   doppler.Group{Name: "Engineering", Slug: "engineering"},
				Members: []doppler.GroupMember{{Type: "workplace_user", Slug: "alice"}, {Type: "workplace_user", Slug: "bob"}},
			},
			{
				Group:   doppler.Group{Name: "DevOps", Slug: "devops"},
				Members: []doppler.GroupMember{{Type: "workplace_user", Slug: "bob"}},
			},
		},
		ServiceAccounts: []doppler.ServiceAccount{
			{Name: "CI Bot", Slug: "ci-bot"},
			{Name: "Deploy Bot", Slug: "deploy-bot"},
		},
		Projects: []ProjectData{
			{
				Project: doppler.Project{Slug: "api", Name: "API", Description: "Backend API"},
				Environments: []doppler.Environment{
					{Slug: "dev", Name: "Development", Project: "api"},
					{Slug: "stg", Name: "Staging", Project: "api"},
					{Slug: "prd", Name: "Production", Project: "api"},
				},
				Configs: []ConfigData{
					{
						Config:  doppler.Config{Name: "dev", Root: true, Environment: "dev", Project: "api"},
						Secrets: doppler.SecretsMap{"API_KEY": {Raw: "dev-key"}, "DB_URL": {Raw: "postgres://dev"}},
					},
					{
						Config:  doppler.Config{Name: "stg", Root: true, Environment: "stg", Project: "api"},
						Secrets: doppler.SecretsMap{"API_KEY": {Raw: "${dev.API_KEY}"}, "DB_URL": {Raw: "postgres://stg"}},
					},
					{
						Config:     doppler.Config{Name: "prd", Root: true, Environment: "prd", Project: "api", Inheritable: true},
						Secrets:    doppler.SecretsMap{"API_KEY": {Raw: "prd-key"}, "DB_URL": {Raw: "postgres://prd", Note: "prod database"}},
						TrustedIPs: []doppler.TrustedIP{{IP: "10.0.0.1/32"}, {IP: "10.0.0.2/32"}},
					},
					{
						Config:  doppler.Config{Name: "prd_ci", Root: false, Environment: "prd", Project: "api", Locked: true},
						Secrets: doppler.SecretsMap{"CI_KEY": {Raw: "ci-val"}},
					},
				},
				Webhooks: []doppler.Webhook{
					{ID: "wh1", URL: "https://hooks.acme.com/api-deploy", Enabled: true},
				},
				Members: []doppler.ProjectMember{
					{Type: "workplace_user", Slug: "alice", Role: doppler.MemberRole{Identifier: "admin"}},
					{Type: "group", Slug: "engineering", Role: doppler.MemberRole{Identifier: "viewer"}},
				},
			},
			{
				Project: doppler.Project{Slug: "web", Name: "Web", Description: "Frontend app"},
				Environments: []doppler.Environment{
					{Slug: "dev", Name: "Development", Project: "web"},
					{Slug: "prd", Name: "Production", Project: "web"},
				},
				Configs: []ConfigData{
					{
						Config:  doppler.Config{Name: "dev", Root: true, Environment: "dev", Project: "web"},
						Secrets: doppler.SecretsMap{"NEXT_PUBLIC_API": {Raw: "http://localhost:3000"}},
					},
					{
						Config:  doppler.Config{Name: "prd", Root: true, Environment: "prd", Project: "web"},
						Secrets: doppler.SecretsMap{"NEXT_PUBLIC_API": {Raw: "https://api.acme.com"}},
					},
				},
				Members: []doppler.ProjectMember{
					{Type: "workplace_user", Slug: "bob", Role: doppler.MemberRole{Identifier: "deployer"}},
				},
			},
		},
	}
}

// setupTargetForMultiProject configures a mockReadWriteClient for multi-project migration.
func setupTargetForMultiProject(target *mockReadWriteClient) {
	// Neither project exists on target
	target.getProjectErr["api"] = fmt.Errorf("not found")
	target.getProjectErr["web"] = fmt.Errorf("not found")

	// Both users exist on target (different IDs than source to verify slug remapping)
	target.users = []doppler.WorkplaceUser{
		{ID: "target-alice", User: struct {
			Email    string `json:"email"`
			Name     string `json:"name"`
			Username string `json:"username"`
		}{Email: "alice@acme.com", Name: "Alice", Username: "alice"}},
		{ID: "target-bob", User: struct {
			Email    string `json:"email"`
			Name     string `json:"name"`
			Username string `json:"username"`
		}{Email: "bob@acme.com", Name: "Bob", Username: "bob"}},
	}

	// Target groups have different slugs than source
	target.groups = []doppler.Group{
		{Name: "Engineering", Slug: "target-engineering"},
		{Name: "DevOps", Slug: "target-devops"},
	}

	// Target service accounts have different slugs than source
	target.serviceAccounts = []doppler.ServiceAccount{
		{Name: "CI Bot", Slug: "target-ci-bot"},
		{Name: "Deploy Bot", Slug: "target-deploy-bot"},
	}
}

func TestIntegrationFullCycle(t *testing.T) {
	data := multiProjectData()
	target := newMockReadWriteClient()
	setupTargetForMultiProject(target)

	cm := NewCheckpointManager("")
	var progress []MigrateProgress
	exec := NewExecutor(nil, target, data, cm, false, func(p MigrateProgress) {
		progress = append(progress, p)
	})

	err := exec.Execute(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// --- Checkpoint verification ---
	cpData := cm.Data()
	if !cpData.Completed {
		t.Error("expected checkpoint completed")
	}
	if cpData.Error != "" {
		t.Errorf("expected no checkpoint error, got %q", cpData.Error)
	}

	// All 8 phases should be complete
	expectedPhases := []string{
		"workplace_settings_and_roles",
		"groups_and_service_accounts",
		"projects_and_environments",
		"configs",
		"secrets_and_locks",
		"config_inheritance",
		"webhooks",
		"members",
	}
	for _, phase := range expectedPhases {
		if !cm.IsPhaseComplete(phase) {
			t.Errorf("expected phase %q to be complete", phase)
		}
	}

	// --- Phase 1: Workplace settings & roles ---
	if target.updatedWorkplace == nil {
		t.Error("expected workplace to be updated")
	}
	if len(target.createdWpRoles) != 2 {
		t.Errorf("expected 2 workplace roles, got %d", len(target.createdWpRoles))
	}
	if len(target.createdProjRoles) != 1 {
		t.Errorf("expected 1 project role, got %d", len(target.createdProjRoles))
	}

	// --- Phase 2: Groups & service accounts ---
	if len(target.createdGroups) != 2 {
		t.Errorf("expected 2 groups, got %d", len(target.createdGroups))
	}
	if len(target.createdSAs) != 2 {
		t.Errorf("expected 2 service accounts, got %d", len(target.createdSAs))
	}

	// --- Phase 3: Projects & environments ---
	if len(target.createdProjects) != 2 {
		t.Errorf("expected 2 projects, got %d", len(target.createdProjects))
	}
	if len(target.createdEnvs) != 5 { // api: 3 envs, web: 2 envs
		t.Errorf("expected 5 environments, got %d", len(target.createdEnvs))
	}

	// --- Phase 4: Configs ---
	// Only branch configs are created; root configs are verified via GetConfig
	if len(target.createdConfigs) != 1 { // only prd_ci
		t.Errorf("expected 1 branch config, got %d: %v", len(target.createdConfigs), target.createdConfigs)
	}

	// --- Phase 5: Secrets, IPs, locks ---
	if len(target.updatedSecrets) != 6 { // 4 api configs + 2 web configs
		t.Errorf("expected 6 secret updates, got %d", len(target.updatedSecrets))
	}
	// Verify raw reference preservation
	for _, su := range target.updatedSecrets {
		if su.Project == "api" && su.Config == "stg" {
			if su.Secrets["API_KEY"] != "${dev.API_KEY}" {
				t.Errorf("expected reference preserved in api/stg, got %q", su.Secrets["API_KEY"])
			}
		}
	}
	// Secret notes are project-scoped, so they carry no config.
	foundNote := false
	for _, n := range target.updatedNotes {
		if n.Project == "api" && n.SecretName == "DB_URL" && n.Note == "prod database" {
			foundNote = true
		}
	}
	if !foundNote {
		t.Error("expected secret note for api/DB_URL")
	}

	if len(target.addedIPs) != 2 { // 2 trusted IPs on api/prd
		t.Errorf("expected 2 trusted IPs, got %d", len(target.addedIPs))
	}
	if len(target.lockedConfigs) != 1 { // api/prd_ci
		t.Errorf("expected 1 locked config, got %d", len(target.lockedConfigs))
	}

	// --- Phase 6: Config inheritance ---
	if len(target.inheritableCalls) != 1 {
		t.Errorf("expected 1 inheritable call, got %d", len(target.inheritableCalls))
	}
	if target.inheritableCalls[0].Project != "api" || target.inheritableCalls[0].Config != "prd" {
		t.Errorf("expected inheritable on api/prd, got %+v", target.inheritableCalls[0])
	}

	// --- Phase 7: Webhooks ---
	if len(target.createdWebhooks) != 1 {
		t.Errorf("expected 1 webhook, got %d", len(target.createdWebhooks))
	}

	// --- Phase 8: Members ---
	// Group members: engineering (alice, bob), devops (bob) = 3
	if len(target.addedGroupMembers) != 3 {
		t.Fatalf("expected 3 group members, got %d", len(target.addedGroupMembers))
	}
	// Verify all group members use remapped target slugs
	for _, gm := range target.addedGroupMembers {
		if gm.MemberType == "workplace_user" {
			if gm.MemberSlug != "target-alice" && gm.MemberSlug != "target-bob" {
				t.Errorf("expected group member slug remapped to target ID, got %q", gm.MemberSlug)
			}
		}
		// Verify group slugs are remapped
		if gm.GroupSlug != "target-engineering" && gm.GroupSlug != "target-devops" {
			t.Errorf("expected group slug remapped to target slug, got %q", gm.GroupSlug)
		}
	}
	// Project members: api (alice user + engineering group), web (bob user) = 3
	if len(target.addedProjMembers) != 3 {
		t.Fatalf("expected 3 project members, got %d", len(target.addedProjMembers))
	}
	// Verify all project members use remapped target slugs
	for _, pm := range target.addedProjMembers {
		switch pm.MemberType {
		case "workplace_user":
			if pm.MemberSlug != "target-alice" && pm.MemberSlug != "target-bob" {
				t.Errorf("expected project user member slug remapped to target ID, got %q", pm.MemberSlug)
			}
		case "group":
			if pm.MemberSlug != "target-engineering" && pm.MemberSlug != "target-devops" {
				t.Errorf("expected project group member slug remapped to target slug, got %q", pm.MemberSlug)
			}
		}
	}

	// --- Secrets cleared from memory ---
	for _, pd := range data.Projects {
		for _, cd := range pd.Configs {
			if cd.Secrets != nil {
				t.Errorf("secrets should be cleared from memory for %s/%s", pd.Project.Slug, cd.Config.Name)
			}
		}
	}

	// --- Progress callbacks should have been emitted ---
	if len(progress) == 0 {
		t.Error("expected progress callbacks")
	}

	// Verify plan matches gathered data
	plan := GeneratePlan(multiProjectData()) // generate from fresh copy
	if plan.Summary.ProjectCount != 2 {
		t.Errorf("plan: expected 2 projects, got %d", plan.Summary.ProjectCount)
	}
	if plan.Summary.EnvironmentCount != 5 {
		t.Errorf("plan: expected 5 environments, got %d", plan.Summary.EnvironmentCount)
	}
	if plan.Summary.ConfigCount != 6 {
		t.Errorf("plan: expected 6 configs, got %d", plan.Summary.ConfigCount)
	}
	if plan.Summary.SecretCount != 9 {
		t.Errorf("plan: expected 9 secrets, got %d", plan.Summary.SecretCount)
	}
	if plan.Summary.RoleCount != 3 { // 2 wp + 1 proj
		t.Errorf("plan: expected 3 roles, got %d", plan.Summary.RoleCount)
	}
	if plan.Summary.GroupCount != 2 {
		t.Errorf("plan: expected 2 groups, got %d", plan.Summary.GroupCount)
	}
	if plan.Summary.WebhookCount != 1 {
		t.Errorf("plan: expected 1 webhook, got %d", plan.Summary.WebhookCount)
	}
	if plan.Summary.ServiceAccountCount != 2 {
		t.Errorf("plan: expected 2 service accounts, got %d", plan.Summary.ServiceAccountCount)
	}
	if plan.Summary.TrustedIPCount != 2 {
		t.Errorf("plan: expected 2 trusted IPs, got %d", plan.Summary.TrustedIPCount)
	}
}

func TestIntegrationIdempotentRerun(t *testing.T) {
	// First run
	data := multiProjectData()
	target := newMockReadWriteClient()
	setupTargetForMultiProject(target)

	cm := NewCheckpointManager("")
	var mu sync.Mutex
	var progress []MigrateProgress
	exec := NewExecutor(nil, target, data, cm, false, func(p MigrateProgress) {
		mu.Lock()
		progress = append(progress, p)
		mu.Unlock()
	})

	err := exec.Execute(context.Background())
	if err != nil {
		t.Fatalf("first run: unexpected error: %v", err)
	}
	if !cm.Data().Completed {
		t.Fatal("first run: expected checkpoint completed")
	}

	// Record counts from first run
	firstRunProjects := len(target.createdProjects)
	firstRunSecrets := len(target.updatedSecrets)
	firstRunRoles := len(target.createdWpRoles)
	firstRunGroups := len(target.createdGroups)

	// Second run with same checkpoint — all phases already complete
	data2 := multiProjectData()
	exec2 := NewExecutor(nil, target, data2, cm, false, nil)
	err = exec2.Execute(context.Background())
	if err != nil {
		t.Fatalf("second run: unexpected error: %v", err)
	}

	// No additional resources should have been created
	if len(target.createdProjects) != firstRunProjects {
		t.Errorf("idempotent: expected %d projects, got %d", firstRunProjects, len(target.createdProjects))
	}
	if len(target.updatedSecrets) != firstRunSecrets {
		t.Errorf("idempotent: expected %d secret updates, got %d", firstRunSecrets, len(target.updatedSecrets))
	}
	if len(target.createdWpRoles) != firstRunRoles {
		t.Errorf("idempotent: expected %d wp roles, got %d", firstRunRoles, len(target.createdWpRoles))
	}
	if len(target.createdGroups) != firstRunGroups {
		t.Errorf("idempotent: expected %d groups, got %d", firstRunGroups, len(target.createdGroups))
	}

	// Checkpoint should still be complete
	if !cm.Data().Completed {
		t.Error("second run: expected checkpoint still completed")
	}
}

func TestIntegrationPartialFailureAndResume(t *testing.T) {
	data := multiProjectData()
	target := newMockReadWriteClient()
	setupTargetForMultiProject(target)

	// Inject error at phase 4 (configs) — make GetConfig fail for api/dev root config
	target.getConfigErr["api/dev"] = fmt.Errorf("connection timeout")

	cm := NewCheckpointManager("")
	exec := NewExecutor(nil, target, data, cm, false, nil)

	err := exec.Execute(context.Background())
	// Individual item failures are non-fatal — migration completes
	if err != nil {
		t.Fatalf("expected no error (item failures are non-fatal), got: %v", err)
	}

	// Verify phases 1-3 completed (no failures)
	for _, phase := range []string{
		"workplace_settings_and_roles",
		"groups_and_service_accounts",
		"projects_and_environments",
	} {
		if !cm.IsPhaseComplete(phase) {
			t.Errorf("expected phase %q complete", phase)
		}
	}

	// Phase 4 should NOT be marked complete (it has item failures)
	if cm.IsPhaseComplete("configs") {
		t.Error("phase 4 (configs) should not be complete — it has item failures")
	}

	// But phases 5-8 should have run and completed (no errors in those)
	for _, phase := range []string{
		"secrets_and_locks",
		"config_inheritance",
		"webhooks",
		"members",
	} {
		if !cm.IsPhaseComplete(phase) {
			t.Errorf("expected phase %q to have completed (runs after phase 4 item failures)", phase)
		}
	}

	// The failed config item should be tracked
	failedStatus := cm.GetItemStatus("configs", "config:api/dev")
	if failedStatus != StatusFailed {
		t.Errorf("expected api/dev config to be failed, got %s", failedStatus)
	}

	// Checkpoint should be completed (migration ran to end) but have an error message
	cpData := cm.Data()
	if !cpData.Completed {
		t.Error("expected checkpoint completed")
	}
	if cpData.Error == "" {
		t.Error("expected checkpoint error to note phases with failures")
	}

	// Record what was already created
	projectsCreatedBefore := len(target.createdProjects)
	envsCreatedBefore := len(target.createdEnvs)
	rolesCreatedBefore := len(target.createdWpRoles)

	// --- Resume ---
	// Fix the mock error
	delete(target.getConfigErr, "api/dev")

	// Create fresh data for retry
	data2 := multiProjectData()
	exec2 := NewExecutor(nil, target, data2, cm, false, nil)
	err = exec2.Execute(context.Background())
	if err != nil {
		t.Fatalf("resume: unexpected error: %v", err)
	}

	// Phases 1-3 should NOT have re-run (already completed)
	if len(target.createdWpRoles) != rolesCreatedBefore {
		t.Errorf("resume: roles should not re-create, expected %d got %d", rolesCreatedBefore, len(target.createdWpRoles))
	}
	if len(target.createdProjects) != projectsCreatedBefore {
		t.Errorf("resume: projects should not re-create, expected %d got %d", projectsCreatedBefore, len(target.createdProjects))
	}
	if len(target.createdEnvs) != envsCreatedBefore {
		t.Errorf("resume: envs should not re-create, expected %d got %d", envsCreatedBefore, len(target.createdEnvs))
	}

	// Phase 4 should now be complete (failed item retried successfully)
	if !cm.IsPhaseComplete("configs") {
		t.Error("resume: expected phase 4 (configs) to complete after retry")
	}

	// Checkpoint should be complete
	cpData = cm.Data()
	if !cpData.Completed {
		t.Error("resume: expected checkpoint completed")
	}
}

func TestIntegrationDevMode(t *testing.T) {
	data := multiProjectData()
	target := newMockReadWriteClient()
	// Don't set up target project errors — dev mode shouldn't call GetProject

	exec, cm, _ := newTestExecutor(data, target, true)

	err := exec.Execute(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// --- Verify NO writes were made ---
	if target.updatedWorkplace != nil {
		t.Error("dev mode: should not update workplace")
	}
	if len(target.createdWpRoles) != 0 {
		t.Errorf("dev mode: expected 0 wp roles, got %d", len(target.createdWpRoles))
	}
	if len(target.createdProjRoles) != 0 {
		t.Errorf("dev mode: expected 0 proj roles, got %d", len(target.createdProjRoles))
	}
	if len(target.createdGroups) != 0 {
		t.Errorf("dev mode: expected 0 groups, got %d", len(target.createdGroups))
	}
	if len(target.createdSAs) != 0 {
		t.Errorf("dev mode: expected 0 service accounts, got %d", len(target.createdSAs))
	}
	if len(target.createdProjects) != 0 {
		t.Errorf("dev mode: expected 0 projects, got %d", len(target.createdProjects))
	}
	if len(target.createdEnvs) != 0 {
		t.Errorf("dev mode: expected 0 envs, got %d", len(target.createdEnvs))
	}
	if len(target.createdConfigs) != 0 {
		t.Errorf("dev mode: expected 0 configs, got %d", len(target.createdConfigs))
	}
	if len(target.updatedSecrets) != 0 {
		t.Errorf("dev mode: expected 0 secret updates, got %d", len(target.updatedSecrets))
	}
	if len(target.addedIPs) != 0 {
		t.Errorf("dev mode: expected 0 trusted IPs, got %d", len(target.addedIPs))
	}
	if len(target.lockedConfigs) != 0 {
		t.Errorf("dev mode: expected 0 locked configs, got %d", len(target.lockedConfigs))
	}
	if len(target.inheritableCalls) != 0 {
		t.Errorf("dev mode: expected 0 inheritable calls, got %d", len(target.inheritableCalls))
	}
	if len(target.createdWebhooks) != 0 {
		t.Errorf("dev mode: expected 0 webhooks, got %d", len(target.createdWebhooks))
	}
	if len(target.addedGroupMembers) != 0 {
		t.Errorf("dev mode: expected 0 group members, got %d", len(target.addedGroupMembers))
	}
	if len(target.addedProjMembers) != 0 {
		t.Errorf("dev mode: expected 0 project members, got %d", len(target.addedProjMembers))
	}

	// Checkpoint should still complete
	cpData := cm.Data()
	if !cpData.Completed {
		t.Error("dev mode: expected checkpoint completed")
	}

	// Plan should still be generated correctly from the data
	plan := GeneratePlan(multiProjectData())
	if plan.Summary.ProjectCount != 2 {
		t.Errorf("dev mode: plan should still have 2 projects, got %d", plan.Summary.ProjectCount)
	}
}

func TestIntegrationLargeDataset(t *testing.T) {
	const (
		numProjects = 10
		numEnvs     = 3
		numConfigs  = 5 // 3 root + 2 branch per project
		numSecrets  = 20
	)

	data := &MigrationData{
		Workplace:      &doppler.Workplace{ID: "wp1", Name: "Large Corp"},
		WorkplaceRoles: []doppler.Role{{Name: "auditor", Permissions: []string{"audit"}, IsCustomRole: true}},
	}

	target := newMockReadWriteClient()

	envNames := []string{"dev", "stg", "prd"}

	for i := 0; i < numProjects; i++ {
		slug := fmt.Sprintf("project-%d", i)
		target.getProjectErr[slug] = fmt.Errorf("not found")

		pd := ProjectData{
			Project: doppler.Project{Slug: slug, Name: fmt.Sprintf("Project %d", i)},
		}

		for _, env := range envNames {
			pd.Environments = append(pd.Environments, doppler.Environment{
				Slug: env, Name: env, Project: slug,
			})
		}

		// 3 root configs (one per env) + 2 branch configs
		for _, env := range envNames {
			secrets := make(doppler.SecretsMap, numSecrets)
			for s := 0; s < numSecrets; s++ {
				secrets[fmt.Sprintf("SECRET_%d", s)] = doppler.Secret{Raw: fmt.Sprintf("value-%d-%s-%d", i, env, s)}
			}
			pd.Configs = append(pd.Configs, ConfigData{
				Config:  doppler.Config{Name: env, Root: true, Environment: env, Project: slug},
				Secrets: secrets,
			})
		}
		// 2 branch configs
		for b := 0; b < 2; b++ {
			secrets := make(doppler.SecretsMap, numSecrets)
			for s := 0; s < numSecrets; s++ {
				secrets[fmt.Sprintf("SECRET_%d", s)] = doppler.Secret{Raw: fmt.Sprintf("branch-%d-%d-%d", i, b, s)}
			}
			branchName := fmt.Sprintf("prd_branch_%d", b)
			pd.Configs = append(pd.Configs, ConfigData{
				Config:  doppler.Config{Name: branchName, Root: false, Environment: "prd", Project: slug},
				Secrets: secrets,
			})
		}

		data.Projects = append(data.Projects, pd)
	}

	// Generate plan BEFORE execute (Execute clears secrets from memory)
	plan := GeneratePlan(data)
	if plan.Summary.ProjectCount != numProjects {
		t.Errorf("plan: expected %d projects, got %d", numProjects, plan.Summary.ProjectCount)
	}
	expectedTotalConfigs := numProjects * numConfigs
	if plan.Summary.ConfigCount != expectedTotalConfigs {
		t.Errorf("plan: expected %d configs, got %d", expectedTotalConfigs, plan.Summary.ConfigCount)
	}
	expectedTotalSecrets := numProjects * numConfigs * numSecrets
	if plan.Summary.SecretCount != expectedTotalSecrets {
		t.Errorf("plan: expected %d secrets, got %d", expectedTotalSecrets, plan.Summary.SecretCount)
	}

	exec, cm, _ := newTestExecutor(data, target, false)

	err := exec.Execute(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify checkpoint
	cpData := cm.Data()
	if !cpData.Completed {
		t.Error("expected checkpoint completed")
	}

	// Verify counts
	if len(target.createdProjects) != numProjects {
		t.Errorf("expected %d projects, got %d", numProjects, len(target.createdProjects))
	}
	expectedEnvs := numProjects * numEnvs
	if len(target.createdEnvs) != expectedEnvs {
		t.Errorf("expected %d environments, got %d", expectedEnvs, len(target.createdEnvs))
	}
	expectedBranch := numProjects * 2
	if len(target.createdConfigs) != expectedBranch {
		t.Errorf("expected %d branch configs, got %d", expectedBranch, len(target.createdConfigs))
	}
	expectedSecretUpdates := numProjects * numConfigs // each config gets one UpdateSecrets call
	if len(target.updatedSecrets) != expectedSecretUpdates {
		t.Errorf("expected %d secret updates, got %d", expectedSecretUpdates, len(target.updatedSecrets))
	}
}
