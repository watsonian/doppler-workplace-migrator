package migration

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/doppler/workplace-migrator/internal/doppler"
)

// scopeData is a small workplace with the dependency shapes a partial has to
// follow: a group that is a member of one project, a service account in
// another, custom roles reachable only through them, and a config whose parent
// lives in a third project.
func scopeData() *MigrationData {
	role := func(id string) doppler.MemberRole { return doppler.MemberRole{Identifier: id} }
	return &MigrationData{
		Workplace: &doppler.Workplace{ID: "wp", Name: "Acme"},
		WorkplaceRoles: []doppler.Role{
			{Name: "CI Deployer", Identifier: "ci-deployer", IsCustomRole: true},
			{Name: "Unused Workplace Role", Identifier: "unused-wp", IsCustomRole: true},
		},
		ProjectRoles: []doppler.Role{
			{Name: "Deployer", Identifier: "deployer", IsCustomRole: true},
			{Name: "Auditor", Identifier: "auditor", IsCustomRole: true},
			{Name: "Unused Project Role", Identifier: "unused-proj", IsCustomRole: true},
		},
		Groups: []GroupWithMembers{
			{Group: doppler.Group{Name: "payments-eng", Slug: "payments-eng",
				DefaultRole: &doppler.GroupDefaultRole{Identifier: "auditor"}}},
			{Group: doppler.Group{Name: "unused-group", Slug: "unused-group"}},
		},
		ServiceAccounts: []doppler.ServiceAccount{
			{Name: "ci-checkout", Slug: "ci-checkout", WorkplaceRole: &doppler.GroupDefaultRole{Identifier: "ci-deployer"}},
			{Name: "unused-sa", Slug: "unused-sa"},
		},
		ChangeRequestPolicies: []doppler.ChangeRequestPolicy{
			{Name: "Payments review", Targets: doppler.ChangeRequestPolicyTargets{
				Projects: map[string]*doppler.ChangeRequestPolicyTarget{"payments": {}}}},
			{Name: "Search review", Targets: doppler.ChangeRequestPolicyTargets{
				Projects: map[string]*doppler.ChangeRequestPolicyTarget{"search": {}}}},
		},
		Projects: []ProjectData{
			{
				Project: doppler.Project{Slug: "payments", Name: "Payments"},
				Configs: []ConfigData{{Config: doppler.Config{Name: "prd"}, SecretCount: 40}},
				Members: []doppler.ProjectMember{
					{Type: "group", Slug: "payments-eng", Role: role("deployer")},
					{Type: "workplace_user", Slug: "alice", Role: role("admin")},
				},
			},
			{
				Project: doppler.Project{Slug: "checkout", Name: "Checkout"},
				Configs: []ConfigData{{
					Config:      doppler.Config{Name: "prd", Inherits: []doppler.ConfigDescriptor{{Project: "shared", Config: "base"}}},
					SecretCount: 12,
				}},
				Members: []doppler.ProjectMember{{Type: "service_account", Slug: "ci-checkout", Role: role("viewer")}},
			},
			{
				Project: doppler.Project{Slug: "shared", Name: "Shared"},
				Configs: []ConfigData{{Config: doppler.Config{Name: "base", Inheritable: true}, SecretCount: 5}},
			},
			{Project: doppler.Project{Slug: "search", Name: "Search"}, Configs: []ConfigData{{Config: doppler.Config{Name: "prd"}, SecretCount: 9}}},
		},
		ServiceTokens: []doppler.ServiceToken{
			{Project: "payments", Config: "prd", Name: "ci"},
			{Project: "search", Config: "prd", Name: "other"},
		},
		ServiceAccountTokens: []doppler.ServiceAccountToken{
			{ServiceAccount: "ci-checkout", Name: "runner"},
			{ServiceAccount: "unused-sa", Name: "nope"},
		},
		Integrations: []doppler.Integration{{Slug: "aws", Syncs: []doppler.Sync{
			{Slug: "s1", Project: "payments"}, {Slug: "s2", Project: "search"},
		}}},
	}
}

func resolve(t *testing.T, data *MigrationData, projects ...string) *ScopeResolution {
	t.Helper()
	res, err := ResolveScope(data, Scope{Mode: ScopePartial, Projects: projects}, nil)
	if err != nil {
		t.Fatalf("ResolveScope: %v", err)
	}
	return res
}

func includedKeys(res *ScopeResolution, kind string) []string {
	var out []string
	for _, r := range res.Included {
		if r.Kind == kind {
			out = append(out, r.Key)
		}
	}
	return out
}

func TestFullScopeCoversEverything(t *testing.T) {
	res, err := ResolveScope(scopeData(), Scope{Mode: ScopeFull}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Projects) != 4 || res.DeferredProjects != 0 {
		t.Fatalf("got %v, deferred %d", res.Projects, res.DeferredProjects)
	}
	if got := ApplyScope(scopeData(), res); got.DeferWorkplaceLevel {
		t.Fatal("a full scope must not defer the workplace layer")
	}
}

func TestPartialScopeRejectsWhatItCannotHonour(t *testing.T) {
	if _, err := ResolveScope(scopeData(), Scope{Mode: ScopePartial}, nil); err == nil {
		t.Error("an empty partial should be refused")
	}
	if _, err := ResolveScope(scopeData(), Scope{Mode: ScopePartial, Projects: []string{"nope"}}, nil); err == nil {
		t.Error("an unknown project should be refused, not ignored")
	}
}

func TestPartialFollowsMembersToTheirWorkplaceResources(t *testing.T) {
	res := resolve(t, scopeData(), "payments")

	// The group is a member; its default project role and the role it holds in
	// payments both come with it. Leaving either out drops the member.
	if got := includedKeys(res, "group"); len(got) != 1 || got[0] != "payments-eng" {
		t.Errorf("groups: %v", got)
	}
	roles := strings.Join(includedKeys(res, "project_role"), ",")
	if !strings.Contains(roles, "deployer") || !strings.Contains(roles, "auditor") {
		t.Errorf("project roles: %s — want the member's role and the group's default", roles)
	}
	if strings.Contains(roles, "unused-proj") {
		t.Error("a role nothing in scope uses should wait for the full run")
	}
	if got := includedKeys(res, "service_account"); len(got) != 0 {
		t.Errorf("no service account is a member of payments, got %v", got)
	}
}

func TestPartialBringsAServiceAccountsWorkplaceRole(t *testing.T) {
	// Without it the account lands on the target's default role — a privilege
	// change in whichever direction the default differs.
	res := resolve(t, scopeData(), "checkout")
	if got := includedKeys(res, "workplace_role"); len(got) != 1 || got[0] != "ci-deployer" {
		t.Fatalf("workplace roles: %v", got)
	}
}

func TestIncludedResourcesSayWhy(t *testing.T) {
	res := resolve(t, scopeData(), "payments")
	for _, r := range res.Included {
		if len(r.Reasons) == 0 {
			t.Errorf("%s %s has no reason", r.Kind, r.Key)
		}
	}
	for _, r := range res.Included {
		if r.Key == "payments-eng" && !strings.Contains(r.Reasons[0], "payments") {
			t.Errorf("the group's reason should name the project: %q", r.Reasons[0])
		}
	}
}

func TestInheritanceAcrossProjectsAddsTheParentAndSaysSo(t *testing.T) {
	res := resolve(t, scopeData(), "checkout")

	if strings.Join(res.Projects, ",") != "checkout,shared" {
		t.Fatalf("got %v, want the parent's project added", res.Projects)
	}
	if len(res.Added) != 1 || res.Added[0].Project != "shared" || !strings.Contains(res.Added[0].Because, "checkout/prd") {
		t.Fatalf("added: %+v", res.Added)
	}
	// Chosen stays what was ticked; the addition is reported, not disguised.
	if strings.Join(res.Chosen, ",") != "checkout" {
		t.Fatalf("chosen: %v", res.Chosen)
	}
}

func TestPartialReportsWhatItDefers(t *testing.T) {
	res := resolve(t, scopeData(), "payments")

	if res.DeferredProjects != 3 || !res.DefersWorkplaceSettings || res.DeferredPolicies != 2 {
		t.Fatalf("deferred: projects %d, settings %v, policies %d", res.DeferredProjects, res.DefersWorkplaceSettings, res.DeferredPolicies)
	}
	if res.DeferredGroups != 1 || res.DeferredServiceAccounts != 2 {
		t.Fatalf("deferred groups %d, service accounts %d", res.DeferredGroups, res.DeferredServiceAccounts)
	}
	// Only the policy that covers payments is worth a warning.
	if strings.Join(res.PoliciesAffected, ",") != "Payments review" {
		t.Fatalf("policies affected: %v", res.PoliciesAffected)
	}
	if res.Configs != 1 || res.Secrets != 40 {
		t.Fatalf("weight: %d configs, %d secrets", res.Configs, res.Secrets)
	}
}

func TestCarriedProjectsStayInALaterPartial(t *testing.T) {
	// An earlier partial's projects are always in scope: leaving one out would
	// not remove it from the target, and nothing could bring it up to date.
	res, err := ResolveScope(scopeData(), Scope{Mode: ScopePartial, Projects: []string{"search"}}, []string{"payments"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.Projects, ",") != "payments,search" {
		t.Fatalf("got %v", res.Projects)
	}
}

func TestApplyScopeNarrowsEverythingProjectShaped(t *testing.T) {
	data := scopeData()
	out := ApplyScope(data, resolve(t, data, "checkout"))

	if !out.DeferWorkplaceLevel {
		t.Error("a partial defers the workplace layer")
	}
	if out.ChangeRequestPolicies != nil {
		t.Error("policies wait for the full run")
	}
	if len(out.Projects) != 2 {
		t.Errorf("projects: %d, want checkout and its parent", len(out.Projects))
	}
	if len(out.Groups) != 0 || len(out.ServiceAccounts) != 1 || len(out.WorkplaceRoles) != 1 {
		t.Errorf("groups %d, accounts %d, workplace roles %d", len(out.Groups), len(out.ServiceAccounts), len(out.WorkplaceRoles))
	}
	if len(out.ServiceTokens) != 0 {
		t.Error("service tokens from projects outside the scope leaked in")
	}
	if len(out.ServiceAccountTokens) != 1 || out.ServiceAccountTokens[0].ServiceAccount != "ci-checkout" {
		t.Errorf("account tokens: %+v", out.ServiceAccountTokens)
	}
	if len(out.Integrations) != 0 {
		t.Errorf("no sync touches checkout or shared, got %+v", out.Integrations)
	}

	// The original is untouched: narrowing works on a copy.
	if len(data.Projects) != 4 || len(data.Groups) != 2 || data.ChangeRequestPolicies == nil || data.DeferWorkplaceLevel {
		t.Fatal("ApplyScope modified the gathered data")
	}
}

func TestApplyScopeKeepsOnlyTheSyncsInScope(t *testing.T) {
	data := scopeData()
	out := ApplyScope(data, resolve(t, data, "payments"))
	if len(out.Integrations) != 1 || len(out.Integrations[0].Syncs) != 1 || out.Integrations[0].Syncs[0].Slug != "s1" {
		t.Fatalf("integrations: %+v", out.Integrations)
	}
	if len(data.Integrations[0].Syncs) != 2 {
		t.Fatal("narrowing the syncs modified the original integration")
	}
}

func TestWithProjectsSplitsTheTwoStages(t *testing.T) {
	data := scopeData()
	earlier := []string{"payments"}

	first := WithProjects(data, earlier, true)
	second := WithProjects(data, earlier, false)

	if len(first.Projects) != 3 || len(second.Projects) != 1 || second.Projects[0].Project.Slug != "payments" {
		t.Fatalf("first %d, second %d", len(first.Projects), len(second.Projects))
	}
	// Workplace resources stay on both sides: stage one creates what is new,
	// stage two brings the earlier ones up to date.
	if len(first.Groups) != 2 || len(second.Groups) != 2 {
		t.Fatal("workplace resources should not be split")
	}
}

func TestScopeSnapshotKeepsRealOrphansOnly(t *testing.T) {
	snap := &TargetSnapshot{Projects: map[string]ProjectSnapshot{
		"payments":    {},
		"search":      {}, // on the source, out of scope
		"legacy-only": {}, // on the target and nowhere on the source
	}}
	out := ScopeSnapshot(snap, []string{"payments"}, scopeData())

	if _, ok := out.Projects["search"]; ok {
		t.Error("an out-of-scope source project would read as orphaned")
	}
	if _, ok := out.Projects["legacy-only"]; !ok {
		t.Error("a project that exists nowhere on the source is a real orphan and must stay")
	}
	if _, ok := out.Projects["payments"]; !ok {
		t.Error("the in-scope project went missing")
	}
	if len(snap.Projects) != 3 {
		t.Fatal("ScopeSnapshot modified the original")
	}
}

func TestScopedDiffDoesNotCallTheRestOrphans(t *testing.T) {
	// The end-to-end form of the above: diff scoped data against a target that
	// holds a project outside the scope.
	data := scopeData()
	scoped := ApplyScope(data, resolve(t, data, "payments"))
	snap := &TargetSnapshot{Projects: map[string]ProjectSnapshot{"search": {}}}

	diff := ComputeDiff(scoped, ScopeSnapshot(snap, []string{"payments"}, data))
	for _, d := range diff.Orphans() {
		if d.Project == "search" {
			t.Fatal("an out-of-scope project was reported as an orphan")
		}
	}
}

func TestScopeKey(t *testing.T) {
	if got := ScopeKey(ScopeFull, nil, nil); got != "" {
		t.Errorf("a plain full run keeps the empty key, so old checkpoints still resume: %q", got)
	}
	if got := ScopeKey(ScopePartial, []string{"b", "a"}, nil); got != "partial:a,b" {
		t.Errorf("got %q", got)
	}
	if got := ScopeKey(ScopeFull, nil, []string{"b", "a"}); got != "full-except:a,b" {
		t.Errorf("got %q", got)
	}
}

func TestCheckpointFromAnotherScopeIsNotResumed(t *testing.T) {
	// The failure this prevents: an interrupted partial resumed under a full
	// scope would skip every phase the partial finished — for its projects only.
	path := filepath.Join(t.TempDir(), "cp.json")

	cm := NewCheckpointManager(path)
	if _, err := cm.LoadForScope("s", "t", "partial:payments"); err != nil {
		t.Fatal(err)
	}
	cm.InitPhase("configs")
	cm.CompletePhase("configs")
	if err := cm.Save(); err != nil {
		t.Fatal(err)
	}

	same := NewCheckpointManager(path)
	if adopted, _ := same.LoadForScope("s", "t", "partial:payments"); !adopted {
		t.Error("the same scope should resume")
	}

	wider := NewCheckpointManager(path)
	if adopted, _ := wider.LoadForScope("s", "t", ""); adopted {
		t.Fatal("a checkpoint from a partial was resumed by a full run")
	}
	if wider.IsPhaseComplete("configs") {
		t.Fatal("the full run inherited the partial's completed phase")
	}
}

func TestScopeHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	at := time.Date(2026, 9, 21, 9, 0, 0, 0, time.UTC)

	h, err := LoadScopeHistory(path, "src", "tgt")
	if err != nil {
		t.Fatal(err)
	}
	if h.CutoverBlocked() {
		t.Error("no history at all must never block — that is every migration from before partial runs")
	}

	_ = h.Record(ScopeRun{Mode: ScopePartial, Projects: []string{"payments", "checkout"}, At: at})
	if !h.CutoverBlocked() {
		t.Error("cutover after a partial would prune and lock down a target missing most of the source")
	}

	reloaded, _ := LoadScopeHistory(path, "src", "tgt")
	if got := reloaded.MigratedAmong([]string{"checkout", "search", "payments"}); strings.Join(got, ",") != "checkout,payments" {
		t.Fatalf("migrated: %v", got)
	}
	if lp := reloaded.LastPartial(); lp == nil || !lp.At.Equal(at) {
		t.Fatalf("last partial: %+v", lp)
	}

	_ = reloaded.Record(ScopeRun{Mode: ScopeFull, Projects: []string{"payments", "checkout", "search"}, At: at.Add(time.Hour)})
	if reloaded.CutoverBlocked() {
		t.Error("a full run after the partial should clear the block")
	}
	// The first-migrated time is the earlier run's, not the full run's.
	if got := reloaded.Migrated()["payments"]; !got.Equal(at) {
		t.Errorf("payments first migrated at %v, want %v", got, at)
	}

	other, _ := LoadScopeHistory(path, "src", "a-different-target")
	if len(other.Runs) != 0 {
		t.Fatal("a history from another target was applied to this one")
	}
}

func TestPlanDoesNotClaimWorkplaceSettingsForAPartial(t *testing.T) {
	data := scopeData()
	plan := GeneratePlan(ApplyScope(data, resolve(t, data, "payments")))
	if plan.Workplace.Settings {
		t.Fatal("the plan for a partial says workplace settings will be migrated")
	}
}

func TestExecutorDefersTheWorkplaceLayerOnAPartial(t *testing.T) {
	run := func(deferred bool) *mockReadWriteClient {
		data := testMigrationData()
		data.DeferWorkplaceLevel = deferred
		data.WorkplaceUsers[1].Access = "admin"

		target := newMockReadWriteClient()
		target.getProjectErr["backend"] = context.DeadlineExceeded // not found; any error will do
		// Bob exists on the target, so his access is something a run could change.
		target.users = []doppler.WorkplaceUser{data.WorkplaceUsers[1]}
		target.users[0].ID = "target-u2"

		exec, _, _ := newTestExecutor(data, target, false)
		_ = exec.Execute(context.Background())
		return target
	}

	// The control: without the flag both writes happen, so the assertions
	// below are about the flag and not about a fixture that never writes.
	full := run(false)
	if full.updatedWorkplace == nil || len(full.userAccess) == 0 {
		t.Fatalf("control run: settings %v, access %v — the fixture does not exercise these writes",
			full.updatedWorkplace != nil, full.userAccess)
	}

	partial := run(true)
	if partial.updatedWorkplace != nil {
		t.Error("a partial run rewrote the target's workplace settings")
	}
	if len(partial.userAccess) != 0 {
		t.Errorf("a partial run changed users' workplace access: %v", partial.userAccess)
	}
}

// The promise a full run after a partial makes: the partial's projects are
// brought up to date. This walks the second stage's recipe — the same one the
// API's topUp runs — against a target where the partial's project holds a value
// the source has since changed, and where stage one has just created another.
func TestSecondStageUpdatesWhatThePartialMoved(t *testing.T) {
	env := doppler.Environment{Slug: "prd", Name: "Production"}
	project := func(slug, value string) ProjectData {
		return ProjectData{
			Project:      doppler.Project{Slug: slug, Name: slug},
			Environments: []doppler.Environment{env},
			Configs: []ConfigData{{
				Config:      doppler.Config{Name: "prd", Root: true, Environment: "prd", Project: slug},
				Secrets:     doppler.SecretsMap{"KEY": {Raw: value}},
				SecretCount: 1,
			}},
		}
	}
	// The source today: payments' KEY has changed since the partial copied it.
	source := &MigrationData{Projects: []ProjectData{project("payments", "rotated"), project("search", "s")}}

	onTarget := func(slug, value string) ProjectSnapshot {
		return ProjectSnapshot{
			Project:      doppler.Project{Slug: slug, Name: slug},
			Environments: map[string]doppler.Environment{"prd": env},
			Configs: map[string]ConfigSnapshot{"prd": {
				Config:  doppler.Config{Name: "prd", Root: true, Environment: "prd", Project: slug},
				Secrets: map[string]SecretFingerprint{"KEY": FingerprintOf(doppler.Secret{Raw: value})},
			}},
		}
	}
	snap := &TargetSnapshot{Projects: map[string]ProjectSnapshot{
		"payments": onTarget("payments", "original"), // what the partial wrote
		"search":   onTarget("search", "s"),          // what stage one just created
	}}

	earlier := []string{"payments"}
	data := WithProjects(source, earlier, false)
	diff := ComputeDiff(data, ScopeSnapshot(snap, earlier, source))

	for _, d := range diff.Differences {
		if d.Project == "search" {
			t.Fatalf("stage two looked at a project it does not own: %+v", d)
		}
	}

	target := newMockReadWriteClient()
	conv := NewConverger(target, data, diff, DefaultConvergeOptions(), NewErrorStore(), func(MigrateProgress) {})
	if _, err := conv.Run(context.Background()); err != nil {
		t.Fatalf("converge: %v", err)
	}

	changes := target.secretChanges["payments/prd"]
	if len(changes) != 1 || changes[0].Name != "KEY" || changes[0].Value == nil || *changes[0].Value != "rotated" {
		t.Fatalf("the changed secret was not brought up to date: %+v", changes)
	}
	if len(target.createdProjects) != 0 {
		t.Fatalf("stage two created projects that already exist: %+v", target.createdProjects)
	}
}
