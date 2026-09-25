package migration

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/doppler/workplace-migrator/internal/doppler"
)

func inheritanceFixture() *MigrationData {
	return &MigrationData{
		Projects: []ProjectData{
			{
				Project: doppler.Project{Slug: "shared", Name: "Shared"},
				Configs: []ConfigData{
					{Config: doppler.Config{Name: "prd", Inheritable: true}},
				},
			},
			{
				Project: doppler.Project{Slug: "api", Name: "API"},
				Configs: []ConfigData{
					{Config: doppler.Config{
						Name: "prd",
						// Inherits across projects; both ends are in the migration.
						Inherits: []doppler.ConfigDescriptor{{Project: "shared", Config: "prd"}},
					}},
					{Config: doppler.Config{
						Name:     "stg",
						Inherits: []doppler.ConfigDescriptor{{Project: "gone", Config: "prd"}},
					}},
					{Config: doppler.Config{Name: "dev"}},
				},
			},
		},
	}
}

func runPhase6(t *testing.T, data *MigrationData, target *mockReadWriteClient, devMode bool) *CheckpointManager {
	t.Helper()
	cm := NewCheckpointManager(filepath.Join(t.TempDir(), "migration-checkpoint.json"))
	exec := NewExecutor(&mockClient{}, target, data, cm, devMode, nil)
	if err := exec.phase6ConfigInheritance(context.Background()); err != nil {
		t.Fatalf("phase6: %v", err)
	}
	return cm
}

func TestConfigInheritanceRelationshipsAreApplied(t *testing.T) {
	target := newMockReadWriteClient()
	cm := runPhase6(t, inheritanceFixture(), target, false)

	got, ok := target.configInherits["api/prd"]
	if !ok {
		t.Fatal("api/prd inheritance was never set — this is the gap the audit found")
	}
	if len(got) != 1 || got[0].Project != "shared" || got[0].Config != "prd" {
		t.Errorf("inherits = %+v, want shared/prd", got)
	}

	// The inheritable flag still gets set, and must precede the relationship.
	if len(target.inheritableCalls) == 0 {
		t.Error("expected the inheritable flag to still be set")
	}

	// A config with no inherits is left alone.
	if _, ordered := target.configInherits["api/dev"]; ordered {
		t.Error("a config with no inheritance should not be written")
	}

	if s := cm.GetItemStatus("config_inheritance", "inherits:api/prd"); s != StatusCompleted {
		t.Errorf("status = %q, want completed", s)
	}
}

// A reference to a config outside the migration is skipped with a reason rather
// than sent, since the target would reject it with a less useful error.
func TestConfigInheritanceSkipsUnknownReferences(t *testing.T) {
	target := newMockReadWriteClient()
	cm := runPhase6(t, inheritanceFixture(), target, false)

	if _, sent := target.configInherits["api/stg"]; sent {
		t.Error("should not send a reference to a config outside the migration")
	}

	status := cm.GetItemStatus("config_inheritance", "inherits:api/stg")
	if status != StatusSkipped {
		t.Fatalf("status = %q, want skipped", status)
	}
	reason := cm.Data().Phases["config_inheritance"].Items["inherits:api/stg"].Reason
	if !strings.Contains(reason, "gone/prd") {
		t.Errorf("reason should name the missing config, got %q", reason)
	}
}

func TestConfigInheritanceIsIdempotent(t *testing.T) {
	data := inheritanceFixture()
	target := newMockReadWriteClient()
	cm := NewCheckpointManager(filepath.Join(t.TempDir(), "migration-checkpoint.json"))

	for i := 0; i < 2; i++ {
		exec := NewExecutor(&mockClient{}, target, data, cm, false, nil)
		if err := exec.phase6ConfigInheritance(context.Background()); err != nil {
			t.Fatalf("run %d: %v", i+1, err)
		}
	}
	if s := cm.GetItemStatus("config_inheritance", "inherits:api/prd"); s != StatusCompleted {
		t.Errorf("status = %q, want completed", s)
	}
}

func TestConfigInheritanceDevModeSkipsWrites(t *testing.T) {
	target := newMockReadWriteClient()
	runPhase6(t, inheritanceFixture(), target, true)
	if len(target.configInherits) != 0 {
		t.Errorf("dev mode should not write inheritance, got %v", target.configInherits)
	}
}

func TestConfigInheritanceFailureIsRecorded(t *testing.T) {
	target := newMockReadWriteClient()
	target.inheritsErr = map[string]error{"api/prd": errors.New("boom")}
	cm := runPhase6(t, inheritanceFixture(), target, false)

	if s := cm.GetItemStatus("config_inheritance", "inherits:api/prd"); s != StatusFailed {
		t.Errorf("status = %q, want failed", s)
	}
}

func userAccessFixture() *MigrationData {
	return &MigrationData{
		WorkplaceUsers: []doppler.WorkplaceUser{
			func() doppler.WorkplaceUser {
				u := workplaceUser("src-1", "alice@acme.com", "alice")
				u.Access = "admin"
				return u
			}(),
			func() doppler.WorkplaceUser {
				u := workplaceUser("src-2", "bob@acme.com", "bob")
				u.Access = "collaborator"
				return u
			}(),
			func() doppler.WorkplaceUser {
				u := workplaceUser("src-3", "carol@acme.com", "carol")
				u.Access = "admin"
				return u
			}(),
		},
	}
}

func targetWithUsers() *mockReadWriteClient {
	target := newMockReadWriteClient()
	target.users = []doppler.WorkplaceUser{
		workplaceUser("tgt-1", "alice@acme.com", "alice"),
		workplaceUser("tgt-2", "bob@acme.com", "bob"),
		// carol was never provisioned on the target
	}
	return target
}

func runPhase8(t *testing.T, data *MigrationData, target *mockReadWriteClient) *CheckpointManager {
	t.Helper()
	cm := NewCheckpointManager(filepath.Join(t.TempDir(), "migration-checkpoint.json"))
	exec := NewExecutor(&mockClient{}, target, data, cm, false, nil)
	if err := exec.phase8Members(context.Background()); err != nil {
		t.Fatalf("phase8: %v", err)
	}
	return cm
}

func TestWorkplaceUserAccessIsApplied(t *testing.T) {
	target := targetWithUsers()
	cm := runPhase8(t, userAccessFixture(), target)

	// Applied against the target's user slug, not the source's.
	if target.userAccess["tgt-1"] != "admin" {
		t.Errorf("alice access = %q, want admin", target.userAccess["tgt-1"])
	}
	if target.userAccess["tgt-2"] != "collaborator" {
		t.Errorf("bob access = %q, want collaborator", target.userAccess["tgt-2"])
	}
	if _, ok := target.userAccess["src-1"]; ok {
		t.Error("source slug must not be used as the target user id")
	}

	// A user missing on the target is skipped, not failed.
	if s := cm.GetItemStatus("members", "user_access:carol@acme.com"); s != StatusSkipped {
		t.Errorf("carol status = %q, want skipped", s)
	}
}

// Applying a lower access level to the account running the migration could revoke
// the permissions the migration still needs, so that user is skipped.
func TestWorkplaceUserAccessSkipsTheMigratingAccount(t *testing.T) {
	target := targetWithUsers()
	target.tokenInfo = &doppler.TokenInfo{
		Principal: doppler.TokenPrincipal{Type: "workplace_user", Slug: "tgt-2"},
	}

	cm := runPhase8(t, userAccessFixture(), target)

	if _, changed := target.userAccess["tgt-2"]; changed {
		t.Error("must not change the access level of the account running the migration")
	}
	if target.userAccess["tgt-1"] != "admin" {
		t.Error("other users should still be updated")
	}

	reason := cm.Data().Phases["members"].Items["user_access:bob@acme.com"].Reason
	if !strings.Contains(reason, "running the migration") {
		t.Errorf("skip reason should explain why, got %q", reason)
	}
}

func TestWorkplaceUserAccessSkipsEmptyAccess(t *testing.T) {
	data := &MigrationData{WorkplaceUsers: []doppler.WorkplaceUser{
		workplaceUser("src-1", "alice@acme.com", "alice"), // no Access set
	}}
	target := targetWithUsers()

	cm := runPhase8(t, data, target)

	if len(target.userAccess) != 0 {
		t.Errorf("expected no access writes, got %v", target.userAccess)
	}
	if s := cm.GetItemStatus("members", "user_access:alice@acme.com"); s != StatusSkipped {
		t.Errorf("status = %q, want skipped", s)
	}
}

func TestWorkplaceUserAccessFailureIsRecorded(t *testing.T) {
	target := targetWithUsers()
	target.userAccessErr = map[string]error{"tgt-1": errors.New("forbidden")}

	cm := runPhase8(t, userAccessFixture(), target)

	if s := cm.GetItemStatus("members", "user_access:alice@acme.com"); s != StatusFailed {
		t.Errorf("status = %q, want failed", s)
	}
	// One failure must not stop the others.
	if target.userAccess["tgt-2"] != "collaborator" {
		t.Error("expected the remaining users to still be updated")
	}
}

// If the token can't be identified, proceed rather than skipping everyone — the
// guard is a safety net, not a precondition.
func TestWorkplaceUserAccessProceedsWhenTokenUnidentified(t *testing.T) {
	target := targetWithUsers()
	target.tokenInfoErr = errors.New("nope")

	runPhase8(t, userAccessFixture(), target)

	if target.userAccess["tgt-1"] != "admin" {
		t.Error("expected access to still be applied")
	}
}

func TestWorkplaceUserAccessDevModeSkipsWrites(t *testing.T) {
	target := targetWithUsers()
	cm := NewCheckpointManager(filepath.Join(t.TempDir(), "migration-checkpoint.json"))
	exec := NewExecutor(&mockClient{}, target, userAccessFixture(), cm, true, nil)

	if err := exec.phase8Members(context.Background()); err != nil {
		t.Fatalf("dev mode: %v", err)
	}
	if len(target.userAccess) != 0 {
		t.Errorf("dev mode should not change access, got %v", target.userAccess)
	}
}

// A config's trusted IP list starts as ["0.0.0.0/0"] on the target. Adding the
// source's ranges without removing that default leaves the config wide open while
// appearing migrated.
func TestTrustedIPsRemoveTheAllowAllDefault(t *testing.T) {
	data := &MigrationData{Projects: []ProjectData{{
		Project: doppler.Project{Slug: "api", Name: "API"},
		Configs: []ConfigData{
			{Config: doppler.Config{Name: "prd"}, TrustedIPs: []doppler.TrustedIP{{IP: "1.2.3.4/32"}}},
			// Source is unrestricted — the target default must be left alone.
			{Config: doppler.Config{Name: "dev"}},
			// Source explicitly allows everything — nothing to remove.
			{Config: doppler.Config{Name: "stg"}, TrustedIPs: []doppler.TrustedIP{{IP: "0.0.0.0/0"}}},
		},
	}}}

	target := newMockReadWriteClient()
	cm := NewCheckpointManager(filepath.Join(t.TempDir(), "migration-checkpoint.json"))
	exec := NewExecutor(&mockClient{}, target, data, cm, false, nil)
	if err := exec.phase10TrustedIPs(context.Background()); err != nil {
		t.Fatalf("phase9: %v", err)
	}

	var removedFrom []string
	for _, call := range target.removedIPs {
		if call.IP != allowAllCIDR {
			t.Errorf("only the allow-all default should be removed, got %+v", call)
		}
		removedFrom = append(removedFrom, call.Config)
	}
	if len(removedFrom) != 1 || removedFrom[0] != "prd" {
		t.Errorf("allow-all removed from %v, want only [prd]", removedFrom)
	}

	// The restriction itself must still be added.
	var added bool
	for _, call := range target.addedIPs {
		if call.Config == "prd" && call.IP == "1.2.3.4/32" {
			added = true
		}
	}
	if !added {
		t.Error("expected the source range to be added")
	}
	if s := cm.GetItemStatus("trusted_ips", "trusted_ips:api/prd"); s != StatusCompleted {
		t.Errorf("status = %q, want completed", s)
	}
}

// If the allow-all entry cannot be removed, the item must not report success —
// the config would be left open.
func TestTrustedIPsFailWhenAllowAllCannotBeRemoved(t *testing.T) {
	data := &MigrationData{Projects: []ProjectData{{
		Project: doppler.Project{Slug: "api", Name: "API"},
		Configs: []ConfigData{{Config: doppler.Config{Name: "prd"}, TrustedIPs: []doppler.TrustedIP{{IP: "1.2.3.4/32"}}}},
	}}}

	target := newMockReadWriteClient()
	target.removeIPErr = map[string]error{"api/prd/" + allowAllCIDR: errors.New("forbidden")}

	cm := NewCheckpointManager(filepath.Join(t.TempDir(), "migration-checkpoint.json"))
	exec := NewExecutor(&mockClient{}, target, data, cm, false, nil)
	if err := exec.phase10TrustedIPs(context.Background()); err != nil {
		t.Fatalf("phase9: %v", err)
	}

	if s := cm.GetItemStatus("trusted_ips", "trusted_ips:api/prd"); s != StatusFailed {
		t.Errorf("status = %q, want failed — the config is still open", s)
	}
}

// Trusted IPs must be the last phase: restricting a config locks the migrator out
// of it, so every other write to that config has to have happened already.
func TestTrustedIPsRunLast(t *testing.T) {
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

	if len(order) == 0 || order[len(order)-1] != "trusted_ips" {
		t.Errorf("phase order %v — trusted_ips must run last", order)
	}
}

// Pause and Resume were an atomic flag plus a separately-assigned channel, so a
// Pause immediately followed by a Resume could close an already-closed channel.
// Run with -race to catch the unsynchronized read in checkPause.
func TestPauseResumeIsRaceFree(t *testing.T) {
	cm := NewCheckpointManager(filepath.Join(t.TempDir(), "migration-checkpoint.json"))
	exec := NewExecutor(&mockClient{}, newMockReadWriteClient(), &MigrationData{}, cm, true, nil)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(3)
		go func() { defer wg.Done(); exec.Pause() }()
		go func() { defer wg.Done(); exec.Resume() }()
		go func() { defer wg.Done(); _ = exec.IsPaused() }()
	}
	wg.Wait()

	// Leave it unpaused so checkPause returns rather than blocking.
	exec.Resume()
	if err := exec.checkPause(context.Background()); err != nil {
		t.Fatalf("checkPause: %v", err)
	}
}

// Data() used to be a shallow copy sharing the Phases and Items maps, so encoding
// it while a migration wrote to them was a concurrent map read and write — which Go
// turns into an unrecoverable fatal error.
func TestCheckpointDataIsSafeToReadWhileWriting(t *testing.T) {
	cm := NewCheckpointManager(filepath.Join(t.TempDir(), "migration-checkpoint.json"))
	cm.InitPhase("configs")

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 500; i++ {
			cm.SetItemStatus("configs", fmt.Sprintf("config:app/%d", i), StatusCompleted, "")
		}
	}()

	for i := 0; i < 500; i++ {
		data := cm.Data()
		// Walk the copy the way JSON encoding would.
		for _, phase := range data.Phases {
			for range phase.Items {
			}
		}
	}
	<-done
}

// Secrets must be written through the change_requests format so each one keeps the
// visibility it had on the source. The plain secrets map carries no visibility, so
// a restricted secret would land on the target readable.
func TestSecretVisibilityIsPreserved(t *testing.T) {
	secrets := doppler.SecretsMap{
		"API_KEY":  {Raw: "abc123", RawVisibility: "restricted"},
		"DB_HOST":  {Raw: "db.internal", RawVisibility: "unmasked"},
		"PASSWORD": {Raw: "hunter2", RawVisibility: "masked"},
		"LEGACY":   {Raw: "plain"}, // no visibility reported
	}
	data := &MigrationData{Projects: []ProjectData{{
		Project: doppler.Project{Slug: "api", Name: "API"},
		Configs: []ConfigData{{
			Config:      doppler.Config{Name: "prd"},
			Secrets:     secrets,
			SecretCount: len(secrets),
		}},
	}}}

	target := newMockReadWriteClient()
	cm := NewCheckpointManager(filepath.Join(t.TempDir(), "migration-checkpoint.json"))
	exec := NewExecutor(&mockClient{}, target, data, cm, false, nil)
	if err := exec.phase5SecretsAndLocks(context.Background()); err != nil {
		t.Fatalf("phase5: %v", err)
	}

	changes := target.secretChanges["api/prd"]
	if len(changes) != 4 {
		t.Fatalf("expected 4 change requests, got %d", len(changes))
	}

	byName := map[string]doppler.SecretChangeRequest{}
	for _, c := range changes {
		byName[c.Name] = c
	}

	for name, want := range map[string]string{
		"API_KEY": "restricted", "DB_HOST": "unmasked", "PASSWORD": "masked",
	} {
		c := byName[name]
		if c.Visibility == nil {
			t.Errorf("%s: visibility not sent — it would land at the target default", name)
			continue
		}
		if *c.Visibility != want {
			t.Errorf("%s visibility = %q, want %q", name, *c.Visibility, want)
		}
	}

	// A secret whose source reported no visibility should not have one invented.
	if byName["LEGACY"].Visibility != nil {
		t.Errorf("LEGACY visibility = %q, want none sent", *byName["LEGACY"].Visibility)
	}

	// Raw values still go across, so ${REFERENCE} syntax survives.
	if byName["API_KEY"].Value == nil || *byName["API_KEY"].Value != "abc123" {
		t.Error("raw value not carried across")
	}
	// originalName must be present and null: these secrets are new on the target,
	// and the API rejects the field being absent.
	if byName["API_KEY"].OriginalName != nil {
		t.Error("originalName should be null for a secret that does not exist yet")
	}
}

// Notes are project-scoped: the same secret name carries one note across every
// config in the project, so writing it once per config repeats the same call.
func TestSecretNotesAreWrittenOncePerProject(t *testing.T) {
	withNote := func() doppler.SecretsMap {
		return doppler.SecretsMap{"DB_URL": {Raw: "postgres://x", Note: "prod database"}}
	}
	data := &MigrationData{Projects: []ProjectData{{
		Project: doppler.Project{Slug: "api", Name: "API"},
		Configs: []ConfigData{
			{Config: doppler.Config{Name: "prd"}, Secrets: withNote(), SecretCount: 1},
			{Config: doppler.Config{Name: "stg"}, Secrets: withNote(), SecretCount: 1},
			{Config: doppler.Config{Name: "dev"}, Secrets: withNote(), SecretCount: 1},
		},
	}}}

	target := newMockReadWriteClient()
	cm := NewCheckpointManager(filepath.Join(t.TempDir(), "migration-checkpoint.json"))
	exec := NewExecutor(&mockClient{}, target, data, cm, false, nil)
	if err := exec.phase5SecretsAndLocks(context.Background()); err != nil {
		t.Fatalf("phase5: %v", err)
	}

	if len(target.updatedNotes) != 1 {
		t.Fatalf("wrote the note %d times across 3 configs, want 1", len(target.updatedNotes))
	}
	got := target.updatedNotes[0]
	if got.Project != "api" || got.SecretName != "DB_URL" || got.Note != "prod database" {
		t.Errorf("unexpected note call: %+v", got)
	}
}

// A secret's declared type (json, yaml, integer, …) must survive migration, or
// everything lands on the target as a plain string. The API models it as an object
// with a "type" field, not a bare string.
func TestSecretValueTypeIsPreserved(t *testing.T) {
	secrets := doppler.SecretsMap{
		"CONFIG_JSON": {Raw: `{"a":1}`, RawValueType: &doppler.SecretValueType{Type: "json"}},
		"PORT":        {Raw: "8080", RawValueType: &doppler.SecretValueType{Type: "integer"}},
		"PLAIN":       {Raw: "hello"}, // no declared type
	}
	data := &MigrationData{Projects: []ProjectData{{
		Project: doppler.Project{Slug: "api", Name: "API"},
		Configs: []ConfigData{{
			Config:      doppler.Config{Name: "prd"},
			Secrets:     secrets,
			SecretCount: len(secrets),
		}},
	}}}

	target := newMockReadWriteClient()
	cm := NewCheckpointManager(filepath.Join(t.TempDir(), "migration-checkpoint.json"))
	exec := NewExecutor(&mockClient{}, target, data, cm, false, nil)
	if err := exec.phase5SecretsAndLocks(context.Background()); err != nil {
		t.Fatalf("phase5: %v", err)
	}

	byName := map[string]doppler.SecretChangeRequest{}
	for _, c := range target.secretChanges["api/prd"] {
		byName[c.Name] = c
	}

	for name, want := range map[string]string{"CONFIG_JSON": "json", "PORT": "integer"} {
		got := byName[name].ValueType
		if got == nil {
			t.Errorf("%s: valueType not sent — it would land as a plain string", name)
			continue
		}
		if got.Type != want {
			t.Errorf("%s valueType = %q, want %q", name, got.Type, want)
		}
	}

	// Nothing is invented for a secret the source did not type.
	if byName["PLAIN"].ValueType != nil {
		t.Errorf("PLAIN valueType = %+v, want none sent", byName["PLAIN"].ValueType)
	}
}

// Renaming the target workplace is opt-in: the target's name is usually chosen
// deliberately, and a migration should not overwrite it without being asked.
func TestWorkplaceNameIsOnlyMigratedWhenEnabled(t *testing.T) {
	for _, tt := range []struct {
		name         string
		enabled      bool
		wantIncluded bool
	}{
		{"off by default", false, false},
		{"enabled explicitly", true, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			data := &MigrationData{Workplace: &doppler.Workplace{
				Name:         "Source Workplace",
				BillingEmail: "billing@acme.com",
			}}
			target := newMockReadWriteClient()
			cm := NewCheckpointManager(filepath.Join(t.TempDir(), "migration-checkpoint.json"))

			exec := NewExecutor(&mockClient{}, target, data, cm, false, nil, WithWorkplaceName(tt.enabled))
			if err := exec.phase1WorkplaceSettingsAndRoles(context.Background()); err != nil {
				t.Fatalf("phase1: %v", err)
			}

			if target.updatedWorkplace == nil {
				t.Fatal("expected the workplace settings to be updated")
			}
			if target.workplaceNameIncluded != tt.wantIncluded {
				t.Errorf("name included = %v, want %v", target.workplaceNameIncluded, tt.wantIncluded)
			}
		})
	}
}
