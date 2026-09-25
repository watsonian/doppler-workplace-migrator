package migration

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/doppleruniversity/workplace-migrator/internal/doppler"
)

// workplaceUser builds a WorkplaceUser; the nested User is an anonymous struct,
// which is unpleasant to write inline.
func workplaceUser(id, email, username string) doppler.WorkplaceUser {
	u := doppler.WorkplaceUser{ID: id}
	u.User.Email = email
	u.User.Username = username
	return u
}

// policyFixture returns gathered data with one policy whose rule names a user, a
// group and a service account — each by a source-instance slug that must be
// translated before the policy can be created on the target.
func policyFixture() *MigrationData {
	return &MigrationData{
		WorkplaceUsers: []doppler.WorkplaceUser{
			workplaceUser("src-user-1", "alice@acme.com", "alice"),
		},
		Groups: []GroupWithMembers{
			{Group: doppler.Group{Slug: "src-grp-1", Name: "Platform"}},
		},
		ServiceAccounts: []doppler.ServiceAccount{
			{Slug: "src-sa-1", Name: "deployer"},
		},
		ChangeRequestPolicies: []doppler.ChangeRequestPolicy{
			{
				ID:          "src-policy",
				Name:        "Production approvals",
				Description: "Two reviewers",
				Rules: []doppler.ChangeRequestPolicyRule{
					{Type: doppler.RuleDisallowSelfReview},
					{Type: doppler.RuleRequiredReviewer, Count: 2, Subjects: []doppler.ChangeRequestPolicySubject{
						{Type: doppler.SubjectWorkplaceUser, Slug: "src-user-1", Name: "alice"},
						{Type: doppler.SubjectGroup, Slug: "src-grp-1", Name: "Platform"},
						{Type: doppler.SubjectServiceAccount, Slug: "src-sa-1", Name: "deployer"},
					}},
				},
				Targets: doppler.ChangeRequestPolicyTargets{
					AllProjects: false,
					Projects: map[string]*doppler.ChangeRequestPolicyTarget{
						"api": {All: false, EnvSlugs: []string{"prd"}, ConfigNames: []string{"prd_eu"}},
					},
				},
			},
		},
	}
}

// targetWithIdentities returns a mock target whose users, groups and service
// accounts carry different slugs than the source, as a real instance would.
func targetWithIdentities() *mockReadWriteClient {
	target := newMockReadWriteClient()
	target.users = []doppler.WorkplaceUser{
		workplaceUser("tgt-user-9", "alice@acme.com", "alice"),
	}
	target.groups = []doppler.Group{{Slug: "tgt-grp-9", Name: "Platform"}}
	target.serviceAccounts = []doppler.ServiceAccount{{Slug: "tgt-sa-9", Name: "deployer"}}
	return target
}

func runPolicyPhase(t *testing.T, data *MigrationData, target *mockReadWriteClient) *CheckpointManager {
	t.Helper()
	cm := NewCheckpointManager(filepath.Join(t.TempDir(), "migration-checkpoint.json"))
	exec := NewExecutor(&mockClient{}, target, data, cm, false, nil)
	if err := exec.phase9ChangeRequestPolicies(context.Background()); err != nil {
		t.Fatalf("migrateChangeRequestPolicies: %v", err)
	}
	return cm
}

func TestPolicySubjectSlugsAreRemappedToTarget(t *testing.T) {
	target := targetWithIdentities()
	cm := runPolicyPhase(t, policyFixture(), target)

	if len(target.createdPolicies) != 1 {
		t.Fatalf("expected 1 policy created, got %d", len(target.createdPolicies))
	}
	created := target.createdPolicies[0]

	if created.ID != "" {
		t.Errorf("source id should be cleared, got %q", created.ID)
	}

	var reviewer doppler.ChangeRequestPolicyRule
	for _, r := range created.Rules {
		if r.Type == doppler.RuleRequiredReviewer {
			reviewer = r
		}
	}
	if len(reviewer.Subjects) != 3 {
		t.Fatalf("expected 3 subjects, got %d", len(reviewer.Subjects))
	}

	got := map[string]string{}
	for _, s := range reviewer.Subjects {
		got[s.Type] = s.Slug
	}
	want := map[string]string{
		doppler.SubjectWorkplaceUser:  "tgt-user-9",
		doppler.SubjectGroup:          "tgt-grp-9",
		doppler.SubjectServiceAccount: "tgt-sa-9",
	}
	for typ, wantSlug := range want {
		if got[typ] != wantSlug {
			t.Errorf("%s slug = %q, want %q (source slug must not survive)", typ, got[typ], wantSlug)
		}
	}

	// Targets are preserved verbatim — project slugs and config names carry across.
	api := created.Targets.Projects["api"]
	if api == nil || api.EnvSlugs[0] != "prd" || api.ConfigNames[0] != "prd_eu" {
		t.Errorf("targets should carry across unchanged, got %+v", api)
	}

	if status := cm.GetItemStatus("change_request_policies", "change_request_policy:Production approvals"); status != StatusCompleted {
		t.Errorf("checkpoint status = %q, want completed", status)
	}
}

// A policy whose reviewer is absent from the target is skipped rather than created
// without them — creating it would silently weaken the policy.
func TestPolicyWithMissingReviewerIsSkipped(t *testing.T) {
	target := targetWithIdentities()
	target.users = nil // alice was never provisioned on the target

	cm := runPolicyPhase(t, policyFixture(), target)

	if len(target.createdPolicies) != 0 {
		t.Fatalf("expected no policy to be created, got %d", len(target.createdPolicies))
	}

	status := cm.GetItemStatus("change_request_policies", "change_request_policy:Production approvals")
	if status != StatusSkipped {
		t.Fatalf("status = %q, want skipped", status)
	}
	data := cm.Data()
	reason := data.Phases["change_request_policies"].Items["change_request_policy:Production approvals"].Reason
	if reason == "" || !strings.Contains(reason, "alice") {
		t.Errorf("skip reason should name the missing reviewer, got %q", reason)
	}
}

func TestPolicyAlreadyOnTargetIsSkipped(t *testing.T) {
	target := targetWithIdentities()
	target.changeRequestPolicies = []doppler.ChangeRequestPolicy{{ID: "existing", Name: "Production approvals"}}

	cm := runPolicyPhase(t, policyFixture(), target)

	if len(target.createdPolicies) != 0 {
		t.Errorf("expected no create when a policy of that name exists, got %d", len(target.createdPolicies))
	}
	if status := cm.GetItemStatus("change_request_policies", "change_request_policy:Production approvals"); status != StatusSkipped {
		t.Errorf("status = %q, want skipped", status)
	}
}

func TestPolicyMigrationIsIdempotent(t *testing.T) {
	data := policyFixture()
	target := targetWithIdentities()
	cm := NewCheckpointManager(filepath.Join(t.TempDir(), "migration-checkpoint.json"))

	for i := 0; i < 2; i++ {
		exec := NewExecutor(&mockClient{}, target, data, cm, false, nil)
		if err := exec.phase9ChangeRequestPolicies(context.Background()); err != nil {
			t.Fatalf("run %d: %v", i+1, err)
		}
	}

	if len(target.createdPolicies) != 1 {
		t.Errorf("expected 1 policy after two runs, got %d", len(target.createdPolicies))
	}
}

func TestPolicyCreateFailureIsRecorded(t *testing.T) {
	target := targetWithIdentities()
	target.createPolicyErr = map[string]error{"Production approvals": errors.New("boom")}

	cm := runPolicyPhase(t, policyFixture(), target)

	status := cm.GetItemStatus("change_request_policies", "change_request_policy:Production approvals")
	if status != StatusFailed {
		t.Errorf("status = %q, want failed", status)
	}
	if !cm.PhaseHasFailures("change_request_policies") {
		t.Error("expected the phase to report failures")
	}
}

func TestPolicyDevModeSkipsWrites(t *testing.T) {
	target := targetWithIdentities()
	cm := NewCheckpointManager(filepath.Join(t.TempDir(), "migration-checkpoint.json"))
	exec := NewExecutor(&mockClient{}, target, policyFixture(), cm, true, nil)

	if err := exec.phase9ChangeRequestPolicies(context.Background()); err != nil {
		t.Fatalf("dev mode: %v", err)
	}
	if len(target.createdPolicies) != 0 {
		t.Errorf("dev mode should not create policies, got %d", len(target.createdPolicies))
	}
}

// Change request policies name users, groups and service accounts as reviewers, so
// they must run after every one of those is in place. Trusted IPs stay after them,
// since restricting a config locks the migrator out of it.
func TestPhaseOrderPutsPoliciesAfterUsersAndGroups(t *testing.T) {
	cm := NewCheckpointManager(filepath.Join(t.TempDir(), "migration-checkpoint.json"))
	exec := NewExecutor(&mockClient{}, newMockReadWriteClient(), &MigrationData{}, cm, true, nil)

	var order []string
	exec.onProgress = func(p MigrateProgress) {
		if p.Status == "started" && p.Resource == "" {
			order = append(order, p.Name)
		}
	}
	if err := exec.Execute(context.Background()); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	index := map[string]int{}
	for i, name := range order {
		index[name] = i
	}

	for _, prerequisite := range []string{"groups_and_service_accounts", "members"} {
		if index[prerequisite] >= index["change_request_policies"] {
			t.Errorf("%q (%d) must run before change_request_policies (%d): policy reviewers reference it",
				prerequisite, index[prerequisite], index["change_request_policies"])
		}
	}

	if order[len(order)-1] != "trusted_ips" {
		t.Errorf("phase order %v — trusted_ips must still run last", order)
	}
}
