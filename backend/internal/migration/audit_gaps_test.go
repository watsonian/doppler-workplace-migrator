package migration

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/doppleruniversity/workplace-migrator/internal/doppler"
)

func identityFixture() *MigrationData {
	return &MigrationData{
		ServiceAccounts: []doppler.ServiceAccount{
			{Slug: "src-sa-1", Name: "deployer"},
			{Slug: "src-sa-2", Name: "orphan"},
		},
		ServiceAccountIdentities: []doppler.ServiceAccountIdentity{
			{
				Slug: "src-identity", Name: "github-actions", Method: "oidc", TTLSeconds: 3600,
				Config:         json.RawMessage(`{"discovery_url":"https://token.actions.githubusercontent.com","claims_type":"exact","claims":{"aud":"doppler","sub":"repo:acme/app:ref:refs/heads/main"}}`),
				ServiceAccount: "src-sa-1",
			},
			{Name: "orphaned", Method: "oidc", TTLSeconds: 900, ServiceAccount: "src-sa-2"},
		},
	}
}

func runPhase2(t *testing.T, data *MigrationData, target *mockReadWriteClient, devMode bool) *CheckpointManager {
	t.Helper()
	cm := NewCheckpointManager(filepath.Join(t.TempDir(), "migration-checkpoint.json"))
	exec := NewExecutor(&mockClient{}, target, data, cm, devMode, nil)
	if err := exec.phase2GroupsAndServiceAccounts(context.Background()); err != nil {
		t.Fatalf("phase2: %v", err)
	}
	return cm
}

func TestServiceAccountIdentityMigratedToRemappedAccount(t *testing.T) {
	target := newMockReadWriteClient()
	// Only one of the two accounts exists on the target, under a different slug.
	target.serviceAccounts = []doppler.ServiceAccount{{Slug: "tgt-sa-77", Name: "deployer"}}

	cm := runPhase2(t, identityFixture(), target, false)

	if len(target.createdIdentities) != 1 {
		t.Fatalf("expected 1 identity created, got %d", len(target.createdIdentities))
	}
	call := target.createdIdentities[0]

	if call.serviceAccount != "tgt-sa-77" {
		t.Errorf("identity attached to %q, want the target slug tgt-sa-77", call.serviceAccount)
	}
	if call.identity.Name != "github-actions" || call.identity.Method != "oidc" || call.identity.TTLSeconds != 3600 {
		t.Errorf("identity fields not carried across: %+v", call.identity)
	}

	// The OIDC config must round-trip byte-for-byte — it is passed through, not modeled.
	var got, want map[string]interface{}
	json.Unmarshal(call.identity.Config, &got)
	json.Unmarshal([]byte(`{"discovery_url":"https://token.actions.githubusercontent.com","claims_type":"exact","claims":{"aud":"doppler","sub":"repo:acme/app:ref:refs/heads/main"}}`), &want)
	if len(got) != len(want) || got["discovery_url"] != want["discovery_url"] || got["claims_type"] != want["claims_type"] {
		t.Errorf("config did not round-trip: %s", string(call.identity.Config))
	}

	if s := cm.GetItemStatus("groups_and_service_accounts", "service_account_identity:src-sa-1/github-actions"); s != StatusCompleted {
		t.Errorf("status = %q, want completed", s)
	}
	// The identity whose account is missing on the target is skipped, not failed.
	if s := cm.GetItemStatus("groups_and_service_accounts", "service_account_identity:src-sa-2/orphaned"); s != StatusSkipped {
		t.Errorf("orphaned identity status = %q, want skipped", s)
	}
}

func TestServiceAccountIdentityIsIdempotent(t *testing.T) {
	data := identityFixture()
	target := newMockReadWriteClient()
	target.serviceAccounts = []doppler.ServiceAccount{{Slug: "tgt-sa-77", Name: "deployer"}}
	cm := NewCheckpointManager(filepath.Join(t.TempDir(), "migration-checkpoint.json"))

	for i := 0; i < 2; i++ {
		exec := NewExecutor(&mockClient{}, target, data, cm, false, nil)
		if err := exec.phase2GroupsAndServiceAccounts(context.Background()); err != nil {
			t.Fatalf("run %d: %v", i+1, err)
		}
	}

	if len(target.createdIdentities) != 1 {
		t.Errorf("expected 1 identity after two runs, got %d", len(target.createdIdentities))
	}
}

func TestServiceAccountIdentityDevModeSkipsWrites(t *testing.T) {
	target := newMockReadWriteClient()
	target.serviceAccounts = []doppler.ServiceAccount{{Slug: "tgt-sa-77", Name: "deployer"}}

	runPhase2(t, identityFixture(), target, true)

	if len(target.createdIdentities) != 0 {
		t.Errorf("dev mode should not create identities, got %d", len(target.createdIdentities))
	}
}

func TestServiceAccountIdentityCreateFailureRecorded(t *testing.T) {
	target := newMockReadWriteClient()
	target.serviceAccounts = []doppler.ServiceAccount{{Slug: "tgt-sa-77", Name: "deployer"}}
	target.createIdentityErr = map[string]error{"github-actions": errors.New("boom")}

	cm := runPhase2(t, identityFixture(), target, false)

	if s := cm.GetItemStatus("groups_and_service_accounts", "service_account_identity:src-sa-1/github-actions"); s != StatusFailed {
		t.Errorf("status = %q, want failed", s)
	}
}

func TestEnvironmentOrderIsReproduced(t *testing.T) {
	data := &MigrationData{
		Projects: []ProjectData{
			{
				Project: doppler.Project{Slug: "api", Name: "API"},
				Environments: []doppler.Environment{
					{Slug: "dev", Name: "Development"},
					{Slug: "stg", Name: "Staging"},
					{Slug: "prd", Name: "Production"},
				},
			},
			{
				// A single environment needs no ordering call.
				Project:      doppler.Project{Slug: "solo", Name: "Solo"},
				Environments: []doppler.Environment{{Slug: "dev", Name: "Development"}},
			},
		},
	}

	target := newMockReadWriteClient()
	cm := NewCheckpointManager(filepath.Join(t.TempDir(), "migration-checkpoint.json"))
	exec := NewExecutor(&mockClient{}, target, data, cm, false, nil)
	if err := exec.phase3ProjectsAndEnvironments(context.Background()); err != nil {
		t.Fatalf("phase3: %v", err)
	}

	got := target.envOrders["api"]
	want := []string{"dev", "stg", "prd"}
	if len(got) != len(want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v (source order must be preserved)", got, want)
		}
	}

	if _, ordered := target.envOrders["solo"]; ordered {
		t.Error("a single-environment project should not be reordered")
	}
	if s := cm.GetItemStatus("projects_and_environments", "env_order:api"); s != StatusCompleted {
		t.Errorf("status = %q, want completed", s)
	}
}

func TestEnvironmentOrderFailureIsNonFatal(t *testing.T) {
	data := &MigrationData{
		Projects: []ProjectData{{
			Project: doppler.Project{Slug: "api", Name: "API"},
			Environments: []doppler.Environment{
				{Slug: "dev", Name: "Development"},
				{Slug: "prd", Name: "Production"},
			},
		}},
	}
	target := newMockReadWriteClient()
	target.envOrderErr = map[string]error{"api": errors.New("nope")}

	cm := NewCheckpointManager(filepath.Join(t.TempDir(), "migration-checkpoint.json"))
	exec := NewExecutor(&mockClient{}, target, data, cm, false, nil)
	if err := exec.phase3ProjectsAndEnvironments(context.Background()); err != nil {
		t.Fatalf("ordering failure should not abort the phase: %v", err)
	}
	if s := cm.GetItemStatus("projects_and_environments", "env_order:api"); s != StatusFailed {
		t.Errorf("status = %q, want failed", s)
	}
}

// Rotated secrets and service account tokens can't be migrated — the report is the
// deliverable, so assert it carries what someone needs to recreate them.
func TestReportOnlyResourcesBecomeManualSteps(t *testing.T) {
	data := &MigrationData{
		ServiceAccountTokens: []doppler.ServiceAccountToken{
			{Name: "ci-token", Slug: "tok-1", ServiceAccount: "deployer"},
		},
		RotatedSecrets: []doppler.RotatedSecret{
			{
				Name: "db-password", Project: "api", Config: "prd",
				RotationPeriodSec: 86400, Enabled: true,
				Status: doppler.RotatedSecretStatus{Type: "retrying", Error: "credentials rejected"},
				Integration: struct {
					Slug string `json:"slug"`
					Name string `json:"name"`
					Type string `json:"type"`
				}{Slug: "int-1", Name: "AWS Prod", Type: "aws_secrets_manager"},
			},
		},
	}

	steps := buildManualSteps(data)

	byCategory := map[string]ManualStep{}
	for _, s := range steps {
		byCategory[s.Category] = s
	}

	// The audit gap was a service account token vanishing without a trace. It
	// is not a manual step any more — the token page issues it or records it
	// skipped — so what has to hold is that the page's ledger picks it up, and
	// knows which account it hangs off.
	if _, ok := byCategory["service_account_token"]; ok {
		t.Error("service account tokens belong to the token page, not the manual steps")
	}
	ledger := NewTokenLedger("")
	ledger.Seed(data, time.Now())
	rec, ok := ledger.Get(ServiceAccountTokenKey("deployer", "ci-token"))
	if !ok {
		t.Fatal("the service account token reached neither the manual steps nor the token ledger")
	}
	if rec.ServiceAccount != "deployer" {
		t.Errorf("token should name its service account, got %+v", rec)
	}

	rs, ok := byCategory["rotated_secret"]
	if !ok {
		t.Fatal("expected a rotated_secret manual step")
	}
	if len(rs.Items) != 1 || rs.Items[0].Name != "db-password" {
		t.Fatalf("unexpected rotated secret items: %+v", rs.Items)
	}
	ctx := rs.Items[0].Context
	// Recreating one needs the integration and the rotation period.
	for key, want := range map[string]string{
		"project": "api", "config": "prd",
		"integration": "AWS Prod", "rotation_period_sec": "86400",
		"status": "retrying", "status_error": "credentials rejected",
	} {
		if ctx[key] != want {
			t.Errorf("context[%q] = %q, want %q", key, ctx[key], want)
		}
	}
	if !strings.Contains(rs.Items[0].Detail, "AWS Prod") {
		t.Errorf("detail should name the integration, got %q", rs.Items[0].Detail)
	}
}

func TestManualStepCountIncludesNewCategories(t *testing.T) {
	// The rotated secret counts; the service account token does not, because the
	// token page, not the manual steps, is where it gets dealt with.
	data := &MigrationData{
		ServiceAccountTokens: []doppler.ServiceAccountToken{{Name: "a", ServiceAccount: "sa"}},
		RotatedSecrets:       []doppler.RotatedSecret{{Name: "b", Project: "p", Config: "c"}},
	}
	plan := GeneratePlan(data)
	if plan.Summary.ManualStepCount != 1 {
		t.Errorf("manual step count = %d, want 1", plan.Summary.ManualStepCount)
	}
}

// Personal configs belong to one user and cannot be migrated. They must be skipped
// before any secret is read — those are that user's own overrides — and reported
// rather than silently dropped.
func TestPersonalConfigsAreSkippedAndReported(t *testing.T) {
	source := &mockClient{
		workplace: &doppler.Workplace{Name: "Acme"},
		projects:  []doppler.Project{{Slug: "api", Name: "API"}},
		configs: map[string][]doppler.Config{
			"api": {
				{Name: "prd"},
				{Name: "dev"},
				{Name: "dev_personal"},
				{Name: "prd_personal"},
			},
		},
		secrets: map[string]doppler.SecretsMap{
			"api/prd":          {"A": {Raw: "1"}},
			"api/dev":          {"B": {Raw: "2"}},
			"api/dev_personal": {"SHOULD_NOT_BE_READ": {Raw: "secret"}},
			"api/prd_personal": {"SHOULD_NOT_BE_READ": {Raw: "secret"}},
		},
	}

	data, err := NewGatherer(source, nil).GatherAll(context.Background())
	if err != nil {
		t.Fatalf("GatherAll: %v", err)
	}

	var names []string
	for _, cd := range data.Projects[0].Configs {
		names = append(names, cd.Config.Name)
		if strings.HasSuffix(cd.Config.Name, "_personal") {
			t.Errorf("personal config %q was gathered", cd.Config.Name)
		}
		for secretName := range cd.Secrets {
			if secretName == "SHOULD_NOT_BE_READ" {
				t.Error("read secrets from a personal config")
			}
		}
	}
	if len(names) != 2 {
		t.Errorf("gathered configs = %v, want just the two shared ones", names)
	}

	if len(data.PersonalConfigs) != 2 {
		t.Fatalf("recorded %d personal configs, want 2", len(data.PersonalConfigs))
	}

	// They must reach the plan, the manual steps and the summary count.
	plan := GeneratePlan(data)
	if plan.Summary.PersonalConfigCount != 2 {
		t.Errorf("summary count = %d, want 2", plan.Summary.PersonalConfigCount)
	}

	var step *ManualStep
	for i := range plan.ManualSteps {
		if plan.ManualSteps[i].Category == "personal_config" {
			step = &plan.ManualSteps[i]
		}
	}
	if step == nil {
		t.Fatal("expected a personal_config manual step")
	}
	if len(step.Items) != 2 {
		t.Errorf("manual step lists %d configs, want 2", len(step.Items))
	}
	if !strings.Contains(step.Description, "set it again") {
		t.Errorf("description should say what the operator must do, got %q", step.Description)
	}
}

func TestIsPersonalConfig(t *testing.T) {
	for name, want := range map[string]bool{
		"dev_personal":     true,
		"prd_personal":     true,
		"dev":              false,
		"prd":              false,
		"personal":         false,
		"my_personal_conf": false,
	} {
		if got := doppler.IsPersonalConfig(name); got != want {
			t.Errorf("IsPersonalConfig(%q) = %v, want %v", name, got, want)
		}
	}
}
