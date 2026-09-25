package migration

import (
	"context"
	"strings"
	"testing"

	"github.com/doppleruniversity/workplace-migrator/internal/doppler"
)

// migratedEarlier is a workplace as it stands after a run in this session:
// payments/prd was written, so its values were dropped, while the target holds
// what was written — and the source has since changed one value.
func migratedEarlier() (*MigrationData, *mockClient, *TargetSnapshot) {
	env := doppler.Environment{Slug: "prd", Name: "Production"}
	cfg := doppler.Config{Name: "prd", Root: true, Environment: "prd", Project: "payments"}

	data := &MigrationData{Projects: []ProjectData{{
		Project:      doppler.Project{Slug: "payments", Name: "payments"},
		Environments: []doppler.Environment{env},
		// Dropped after the write; the count is what survives.
		Configs: []ConfigData{{Config: cfg, Secrets: nil, SecretCount: 2}},
	}}}

	source := newMockClient()
	source.secrets["payments/prd"] = doppler.SecretsMap{
		"API_KEY":        {Raw: "rotated"},
		"DB_URL":         {Raw: "postgres://db"},
		"DOPPLER_CONFIG": {Raw: "prd"}, // managed by the instance; never migrated
	}

	snap := &TargetSnapshot{Projects: map[string]ProjectSnapshot{"payments": {
		Project:      doppler.Project{Slug: "payments", Name: "payments"},
		Environments: map[string]doppler.Environment{"prd": env},
		Configs: map[string]ConfigSnapshot{"prd": {
			Config: cfg,
			Secrets: map[string]SecretFingerprint{
				"API_KEY": FingerprintOf(doppler.Secret{Raw: "original"}),
				"DB_URL":  FingerprintOf(doppler.Secret{Raw: "postgres://db"}),
			},
		}},
	}}}
	return data, source, snap
}

func TestDroppedValuesNeverReadAsOrphans(t *testing.T) {
	// The backstop. Without it every secret on the target read as "removed from
	// the source", and a run asked to delete orphans deleted all of them.
	data, _, snap := migratedEarlier()
	diff := ComputeDiff(data, snap)

	if orphans := diff.Orphans(); len(orphans) != 0 {
		t.Fatalf("dropped values produced orphans: %+v", orphans)
	}
	var flagged bool
	for _, d := range diff.Differences {
		if d.Kind == KindSecret && strings.Contains(d.Name, "not loaded") {
			flagged = true
		}
	}
	if !flagged {
		t.Fatal("the config with dropped values should be reported, not silently matched")
	}
}

func TestDeletingOrphansAfterARunDeletesNothingItShouldNot(t *testing.T) {
	// The concrete loss: a top-up asked to delete what the source no longer
	// has, diffing a config whose values were dropped.
	data, _, snap := migratedEarlier()
	target := newMockReadWriteClient()
	opts := DefaultConvergeOptions()
	opts.DeleteOrphans = true

	conv := NewConverger(target, data, ComputeDiff(data, snap), opts, NewErrorStore(), func(MigrateProgress) {})
	_, _ = conv.Run(context.Background())

	if n := len(target.deletedSecrets["payments/prd"]); n != 0 {
		t.Fatalf("deleted %d secrets the source still has: %v", n, target.deletedSecrets)
	}
}

func TestReloadThenTopUpUpdatesWhatChanged(t *testing.T) {
	// What the second stage of a full run does after a partial in the same
	// session: reload, diff, write only the difference.
	data, source, snap := migratedEarlier()

	n, err := ReloadSecrets(context.Background(), source, data, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("reloaded %d configs, want 1", n)
	}

	diff := ComputeDiff(data, snap)
	if len(diff.Orphans()) != 0 {
		t.Fatalf("orphans after reload: %+v", diff.Orphans())
	}

	target := newMockReadWriteClient()
	opts := DefaultConvergeOptions()
	opts.DeleteOrphans = true
	conv := NewConverger(target, data, diff, opts, NewErrorStore(), func(MigrateProgress) {})
	if _, err := conv.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	changes := target.secretChanges["payments/prd"]
	if len(changes) != 1 || changes[0].Name != "API_KEY" || *changes[0].Value != "rotated" {
		t.Fatalf("want exactly the changed secret written, got %+v", changes)
	}
	if len(target.deletedSecrets["payments/prd"]) != 0 {
		t.Fatalf("deleted secrets the source still has: %v", target.deletedSecrets)
	}
}

func TestReloadOnlyTouchesWhatWasDropped(t *testing.T) {
	data, source, _ := migratedEarlier()
	// A second project whose values are still in memory, and a third outside
	// the requested projects.
	data.Projects = append(data.Projects,
		ProjectData{Project: doppler.Project{Slug: "search"}, Configs: []ConfigData{{
			Config: doppler.Config{Name: "prd"}, Secrets: doppler.SecretsMap{"K": {Raw: "in memory"}}, SecretCount: 1,
		}}},
		ProjectData{Project: doppler.Project{Slug: "billing"}, Configs: []ConfigData{{
			Config: doppler.Config{Name: "prd"}, Secrets: nil, SecretCount: 3,
		}}},
	)
	source.secrets["search/prd"] = doppler.SecretsMap{"K": {Raw: "from the source"}}

	n, err := ReloadSecrets(context.Background(), source, data, []string{"payments", "search"})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("reloaded %d, want only payments/prd", n)
	}

	got := data.Projects[0].Configs[0]
	if _, ok := got.Secrets["DOPPLER_CONFIG"]; ok {
		t.Error("an instance-managed DOPPLER_ secret was reloaded")
	}
	if got.SecretCount != 2 || got.Secrets["API_KEY"].Raw != "rotated" {
		t.Fatalf("payments/prd: count %d, values %v", got.SecretCount, got.Secrets)
	}
	if data.Projects[1].Configs[0].Secrets["K"].Raw != "in memory" {
		t.Error("a config whose values were never dropped was re-read")
	}
	if data.Projects[2].Configs[0].Secrets != nil {
		t.Error("a project outside the requested ones was reloaded")
	}
}

func TestPlanCountsSecretsAfterTheirValuesAreDropped(t *testing.T) {
	// A partial's summary read "145 of 113 secrets": the source total was
	// recounted from data whose migrated configs had just been cleared.
	data, _, _ := migratedEarlier()
	if got := GeneratePlan(data).Summary.SecretCount; got != 2 {
		t.Fatalf("secret count %d after the values were dropped, want the 2 gathered", got)
	}
}
