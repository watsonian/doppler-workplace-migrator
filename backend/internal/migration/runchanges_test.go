package migration

import (
	"context"
	"strings"
	"testing"

	"github.com/doppler/workplace-migrator/internal/doppler"
)

func TestChangesFromCheckpointNameThingsAsPeopleKnowThem(t *testing.T) {
	cm := NewCheckpointManager("")
	cm.InitPhase("groups_and_service_accounts")
	cm.SetItemStatus("groups_and_service_accounts", "group:9f1c-uuid", StatusCompleted, "")
	cm.SetItemStatus("groups_and_service_accounts", "service_account:77ab-uuid", StatusFailed, "boom")
	cm.InitPhase("members")
	cm.SetItemStatus("members", "project_member:payments/u-123", StatusCompleted, "")
	cm.SetItemStatus("members", "user_access:ana@example.com", StatusSkipped, "self")
	cm.InitPhase("projects_and_environments")
	cm.SetItemStatus("projects_and_environments", "env_order:payments", StatusCompleted, "")

	u := doppler.WorkplaceUser{ID: "u-123"}
	u.User.Email = "ana@example.com"
	data := &MigrationData{
		Groups:          []GroupWithMembers{{Group: doppler.Group{Slug: "9f1c-uuid", Name: "Engineering"}}},
		ServiceAccounts: []doppler.ServiceAccount{{Slug: "77ab-uuid", Name: "CI"}},
		WorkplaceUsers:  []doppler.WorkplaceUser{u},
	}

	rc := ChangesFromCheckpoint(cm.Data(), data)
	got := map[string]ChangeGroup{}
	for _, g := range rc.Groups {
		got[g.Label] = g
	}
	if g := got["Groups"]; g.Migrated != 1 || g.Items[0].Item != "Engineering" {
		t.Errorf("groups %+v", g)
	}
	if g := got["Service accounts"]; g.Failed != 1 || g.Items[0].Item != "CI" {
		t.Errorf("accounts %+v", g)
	}
	if g := got["Project members"]; g.Items[0].Item != "payments · ana@example.com" {
		t.Errorf("members %+v", g)
	}
	if _, ok := got["User access"]; ok {
		t.Error("a skipped item did nothing and should not be listed")
	}
	for _, g := range rc.Groups {
		for _, it := range g.Items {
			if strings.Contains(it.Item, "env_order") {
				t.Error("bookkeeping keys should not be shown")
			}
		}
	}
}

func TestRunChangesCapItemsButKeepCounts(t *testing.T) {
	rc := &RunChanges{}
	for i := 0; i < changeItemCap+25; i++ {
		rc.Add("Configs", "migrated", "x")
	}
	g := rc.Groups[0]
	if g.Migrated != changeItemCap+25 || len(g.Items) != changeItemCap || g.More != 25 {
		t.Fatalf("migrated %d, items %d, more %d", g.Migrated, len(g.Items), g.More)
	}
}

func TestAConvergeRunRecordsItsChanges(t *testing.T) {
	data, snap := crossInstanceFixture()
	conv := NewConverger(newMockReadWriteClient(), data, ComputeDiff(data, snap), DefaultConvergeOptions(), NewErrorStore(), nil)
	result, err := conv.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	labels := map[string]ChangeGroup{}
	for _, g := range result.Changes.Groups {
		labels[g.Label] = g
	}
	if g := labels["Service accounts"]; g.Updated != 1 || g.Items[0].Item != "CI" {
		t.Errorf("accounts %+v", g)
	}
	if g := labels["Project members"]; g.Created != 1 || g.Updated != 1 {
		t.Errorf("members %+v", g)
	}
	if g := labels["Webhooks"]; g.Updated != 1 || !strings.Contains(g.Items[0].Item, "hooks.example.com") {
		t.Errorf("webhooks %+v", g)
	}
}

func TestATopUpCountsWhatItsFirstStageCreated(t *testing.T) {
	// The report: a top-up rebuilt a project deleted from the target, and its
	// run read "nothing had changed" — the first stage was left out entirely.
	cm := NewCheckpointManager("")
	cm.InitPhase("projects_and_environments")
	cm.SetItemStatus("projects_and_environments", "project:something-new", StatusCompleted, "")
	cm.SetItemStatus("projects_and_environments", "env:something-new/dev", StatusCompleted, "")
	cm.InitPhase("secrets_and_locks")
	cm.SetItemStatus("secrets_and_locks", "secrets:something-new/dev", StatusCompleted, "")
	// Re-applied on every run whether or not anything changed.
	cm.InitPhase("members")
	cm.SetItemStatus("members", "user_access:ana@example.com", StatusCompleted, "")
	cm.SetItemStatus("members", "group_member:eng/u-1", StatusCompleted, "")
	cm.InitPhase("workplace_settings_and_roles")
	cm.SetItemStatus("workplace_settings_and_roles", "workplace_settings", StatusCompleted, "")
	// Already there, so nothing was done.
	cm.InitPhase("groups_and_service_accounts")
	cm.SetItemStatus("groups_and_service_accounts", "group:eng", StatusSkipped, "already exists")

	rc := ChangesFromCheckpoint(cm.Data(), nil, ReappliedEveryRun...)
	written, failed := rc.Totals()
	if written != 3 || failed != 0 {
		t.Fatalf("written %d, failed %d; want the project, its environment and its secrets", written, failed)
	}
	for _, g := range rc.Groups {
		if g.Label == "User access" || g.Label == "Group members" || g.Label == "Workplace settings" || g.Label == "Groups" {
			t.Errorf("%s should not be reported: %+v", g.Label, g)
		}
	}
}
