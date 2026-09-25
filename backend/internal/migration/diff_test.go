package migration

import (
	"testing"

	"github.com/doppler/workplace-migrator/internal/doppler"
)

func secret(raw string) doppler.Secret {
	return doppler.Secret{Raw: raw, RawVisibility: "masked"}
}

func sourceWith(configName string, secrets map[string]string, ips ...string) *MigrationData {
	sm := doppler.SecretsMap{}
	for name, value := range secrets {
		sm[name] = secret(value)
	}
	trusted := make([]doppler.TrustedIP, 0, len(ips))
	for _, ip := range ips {
		trusted = append(trusted, doppler.TrustedIP{IP: ip})
	}
	return &MigrationData{
		Projects: []ProjectData{{
			Project: doppler.Project{Slug: "payments", Name: "payments"},
			Configs: []ConfigData{{
				Config:      doppler.Config{Name: configName},
				Secrets:     sm,
				SecretCount: len(sm),
				TrustedIPs:  trusted,
			}},
		}},
	}
}

func targetWith(configName string, secrets map[string]string, ips ...string) *TargetSnapshot {
	fps := map[string]SecretFingerprint{}
	for name, value := range secrets {
		fps[name] = FingerprintOf(secret(value))
	}
	trusted := doppler.TrustedIPSet{}
	for _, ip := range ips {
		trusted[ip] = struct{}{}
	}
	return &TargetSnapshot{
		WorkplaceRoles:  map[string]doppler.Role{},
		ProjectRoles:    map[string]doppler.Role{},
		Groups:          map[string]GroupSnapshot{},
		ServiceAccounts: map[string]doppler.ServiceAccount{},
		Projects: map[string]ProjectSnapshot{
			"payments": {
				Project:      doppler.Project{Slug: "payments"},
				Environments: map[string]doppler.Environment{},
				Webhooks:     map[string]doppler.Webhook{},
				Members:      map[string]doppler.ProjectMember{},
				Configs: map[string]ConfigSnapshot{
					configName: {Config: doppler.Config{Name: configName}, Secrets: fps, TrustedIPs: trusted},
				},
			},
		},
	}
}

func findDiff(t *testing.T, d *Diff, kind ResourceKind, name string) *Difference {
	t.Helper()
	for i := range d.Differences {
		if d.Differences[i].Kind == kind && d.Differences[i].Name == name {
			return &d.Differences[i]
		}
	}
	return nil
}

func TestDiffAgainstAnEmptyTargetIsAllCreates(t *testing.T) {
	// A first migration and a fifth run the same code. If this is not true, the
	// diff is a second implementation of the migration and will drift from it.
	src := sourceWith("prd", map[string]string{"A": "1", "B": "2"})

	d := ComputeDiff(src, nil)
	counts := d.Counts()

	if counts.Create == 0 {
		t.Fatal("an empty target should make everything a create")
	}
	if counts.Update != 0 || counts.Orphan != 0 {
		t.Errorf("nothing should be an update or an orphan: %+v", counts)
	}
	if p := findDiff(t, d, KindProject, "payments"); p == nil || p.Disposition != DispositionCreate {
		t.Error("the project itself should be a create")
	}
}

func TestDiffClassifiesSecrets(t *testing.T) {
	src := sourceWith("prd", map[string]string{"SAME": "v1", "CHANGED": "new", "ADDED": "v"})
	tgt := targetWith("prd", map[string]string{"SAME": "v1", "CHANGED": "old", "REMOVED": "v"})

	d := ComputeDiff(src, tgt)

	if got := findDiff(t, d, KindSecret, "ADDED"); got == nil || got.Disposition != DispositionCreate {
		t.Error("a secret only on the source is a create")
	}
	if got := findDiff(t, d, KindSecret, "CHANGED"); got == nil || got.Disposition != DispositionUpdate {
		t.Error("a secret whose value differs is an update")
	}
	if got := findDiff(t, d, KindSecret, "REMOVED"); got == nil || got.Disposition != DispositionOrphan {
		t.Error("a secret only on the target is an orphan")
	}
	if findDiff(t, d, KindSecret, "SAME") != nil {
		t.Error("an identical secret should not appear as a difference")
	}
	if d.Matched[KindSecret] != 1 {
		t.Errorf("matched secrets = %d, want 1", d.Matched[KindSecret])
	}
}

func TestDiffNamesWhatDiffersWithoutTheValue(t *testing.T) {
	// The plan is read on screen, screenshotted and exported. It says which
	// secrets differ, never what they differ to.
	src := sourceWith("prd", map[string]string{"TOKEN": "super-secret-source-value"})
	tgt := targetWith("prd", map[string]string{"TOKEN": "super-secret-target-value"})

	d := ComputeDiff(src, tgt)
	got := findDiff(t, d, KindSecret, "TOKEN")
	if got == nil {
		t.Fatal("expected a difference")
	}
	if len(got.Changes) != 1 || got.Changes[0].Field != "value" {
		t.Errorf("Changes = %+v, want a single 'value' field", got.Changes)
	}
	if got.Changes[0].Was != "" || got.Changes[0].Now != "" {
		t.Fatalf("a secret difference carried values: %+v", got.Changes[0])
	}
}

func TestDiffDetectsVisibilityAndTypeChanges(t *testing.T) {
	src := sourceWith("prd", nil)
	src.Projects[0].Configs[0].Secrets = doppler.SecretsMap{
		"KEY": {Raw: "v", RawVisibility: "restricted", RawValueType: &doppler.SecretValueType{Type: "json"}},
	}
	src.Projects[0].Configs[0].SecretCount = 1
	tgt := targetWith("prd", map[string]string{"KEY": "v"}) // masked, no type

	d := ComputeDiff(src, tgt)
	got := findDiff(t, d, KindSecret, "KEY")
	if got == nil || got.Disposition != DispositionUpdate {
		t.Fatal("a visibility change is a difference even when the value matches")
	}
	// Visibility and type are metadata, not the value, and read as they were
	// and will be.
	want := []FieldChange{
		{Field: "visibility", Was: "masked", Now: "restricted"},
		{Field: "type", Was: "string", Now: "json"},
	}
	if len(got.Changes) != 2 || got.Changes[0] != want[0] || got.Changes[1] != want[1] {
		t.Errorf("Changes = %+v, want %+v", got.Changes, want)
	}
}

func TestDiffReportsUnreadableTargetSecretsRatherThanAssumingEmpty(t *testing.T) {
	// Treating an unreadable config as empty would propose rewriting every
	// secret in it — a destructive answer to a permissions problem.
	src := sourceWith("prd", map[string]string{"A": "1", "B": "2"})
	tgt := targetWith("prd", nil)
	cs := tgt.Projects["payments"].Configs["prd"]
	cs.Secrets = nil
	tgt.Projects["payments"].Configs["prd"] = cs

	d := ComputeDiff(src, tgt)
	if got := findDiff(t, d, KindSecret, "(unreadable)"); got == nil {
		t.Fatal("an unreadable config should be one reported difference")
	}
	if findDiff(t, d, KindSecret, "A") != nil {
		t.Error("individual secrets should not be proposed against an unreadable config")
	}
}

func TestDiffMirrorsTrustedIPs(t *testing.T) {
	src := sourceWith("prd", map[string]string{"A": "1"}, "10.0.0.0/8")
	tgt := targetWith("prd", map[string]string{"A": "1"}, "0.0.0.0/0", "192.168.0.0/16")

	d := ComputeDiff(src, tgt)
	got := findDiff(t, d, KindTrustedIPs, "prd")
	if got == nil {
		t.Fatal("expected a trusted IP difference")
	}

	var add, remove string
	for _, c := range got.Changes {
		if c.Field == "add" {
			add = c.Now
		}
		if c.Field == "remove" {
			remove = c.Was
		}
	}
	if add != "10.0.0.0/8" {
		t.Errorf("add = %q", add)
	}
	// Both the default and the stray range go: a mirror, not a merge.
	if remove != "0.0.0.0/0, 192.168.0.0/16" {
		t.Errorf("remove = %q", remove)
	}
}

func TestDiffIgnoresTrustedIPsWhenBothAreAtTheDefault(t *testing.T) {
	src := sourceWith("prd", map[string]string{"A": "1"}, "0.0.0.0/0")
	tgt := targetWith("prd", map[string]string{"A": "1"}, "0.0.0.0/0")

	d := ComputeDiff(src, tgt)
	if findDiff(t, d, KindTrustedIPs, "prd") != nil {
		t.Error("a config nobody has restricted has nothing to mirror")
	}
	if d.Matched[KindTrustedIPs] != 1 {
		t.Error("it should still be counted as matched")
	}
}

func TestDiffCountsMatchesSoARunCanSayWhatItLookedAt(t *testing.T) {
	src := sourceWith("prd", map[string]string{"A": "1", "B": "2", "C": "3"})
	tgt := targetWith("prd", map[string]string{"A": "1", "B": "2", "C": "3"})

	counts := ComputeDiff(src, tgt).Counts()
	if counts.Create != 0 || counts.Update != 0 || counts.Orphan != 0 {
		t.Errorf("identical inputs produced work: %+v", counts)
	}
	// "3 already current" is the number that makes a top-up trustworthy.
	if counts.Match < 3 {
		t.Errorf("Match = %d, want at least the 3 secrets", counts.Match)
	}
}

func TestDiffGroupMembershipIsOneDifference(t *testing.T) {
	// The API replaces the whole member list, so however many members moved it
	// is one fix and should read as one row.
	src := &MigrationData{Groups: []GroupWithMembers{{
		Group:   doppler.Group{Slug: "oncall", Name: "oncall"},
		Members: []doppler.GroupMember{{Type: "workplace_user", Slug: "a"}, {Type: "workplace_user", Slug: "b"}},
	}}}
	tgt := &TargetSnapshot{
		WorkplaceRoles: map[string]doppler.Role{}, ProjectRoles: map[string]doppler.Role{},
		ServiceAccounts: map[string]doppler.ServiceAccount{}, Projects: map[string]ProjectSnapshot{},
		Groups: map[string]GroupSnapshot{"oncall": {
			Group:   doppler.Group{Slug: "oncall", Name: "oncall"},
			Members: []doppler.GroupMember{{Type: "workplace_user", Slug: "a"}},
		}},
	}

	d := ComputeDiff(src, tgt)
	if got := findDiff(t, d, KindGroupMembers, "oncall"); got == nil {
		t.Fatal("changed membership should be a difference")
	}
	if findDiff(t, d, KindGroup, "oncall") != nil {
		t.Error("the group itself is unchanged and should not also appear")
	}
}

func TestDiffOrphansAProjectTheSourceNoLongerHas(t *testing.T) {
	src := sourceWith("prd", map[string]string{"A": "1"})
	tgt := targetWith("prd", map[string]string{"A": "1"})
	tgt.Projects["retired"] = ProjectSnapshot{
		Project:  doppler.Project{Slug: "retired"},
		Configs:  map[string]ConfigSnapshot{"prd": {}},
		Webhooks: map[string]doppler.Webhook{}, Members: map[string]doppler.ProjectMember{},
		Environments: map[string]doppler.Environment{},
	}

	d := ComputeDiff(src, tgt)
	got := findDiff(t, d, KindProject, "retired")
	if got == nil || got.Disposition != DispositionOrphan {
		t.Fatal("a project only on the target is an orphan")
	}
	if len(d.Orphans()) != 1 {
		t.Errorf("Orphans() = %d, want 1", len(d.Orphans()))
	}
}

func TestDiffCountsWhatANewProjectBringsWithIt(t *testing.T) {
	// A partial migration of one project reported its environments, configs and
	// secrets as identical, because a new project was one difference and nothing
	// beneath it was counted anywhere.
	src := sourceWith("dev", map[string]string{"A": "1", "B": "2"})
	pd := &src.Projects[0]
	pd.Environments = []doppler.Environment{{Slug: "dev"}, {Slug: "stg"}, {Slug: "prd"}}
	pd.Configs = append(pd.Configs,
		ConfigData{Config: doppler.Config{Name: "stg"}, SecretCount: 3},
		ConfigData{Config: doppler.Config{Name: "prd"}, SecretCount: 4})
	pd.Webhooks = []doppler.Webhook{{URL: "https://example.com/hook"}}

	target := targetWith("dev", nil)
	delete(target.Projects, "payments")

	d := ComputeDiff(src, target)

	p := findDiff(t, d, KindProject, "payments")
	if p == nil || p.Disposition != DispositionCreate {
		t.Fatal("the project should be a create")
	}
	want := map[ResourceKind]int{KindEnvironment: 3, KindConfig: 3, KindSecret: 9, KindWebhook: 1}
	for kind, n := range want {
		if p.Contains[kind] != n {
			t.Errorf("contains[%s] = %d, want %d", kind, p.Contains[kind], n)
		}
	}
	if _, ok := p.Contains[KindProjectMember]; ok {
		t.Error("a kind the project has none of should be left out")
	}
	// Still one difference: the converger builds the project whole, and a
	// per-item create would have it build each piece a second time.
	if len(d.Differences) != 1 {
		t.Errorf("want only the project create, got %d differences", len(d.Differences))
	}
	if got := d.Counts().Create; got != 1+3+3+9+1 {
		t.Errorf("counts.create = %d, want the project and everything in it", got)
	}
}
