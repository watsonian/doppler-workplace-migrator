package migration

import (
	"context"
	"reflect"
	"testing"

	"github.com/doppler/onprem-migrator/internal/doppler"
)

// crossInstanceFixture is one project, one group and one account migrated
// earlier, as two real instances hold them: every group, account and user
// slug differs between the two, because each instance assigns its own.
func crossInstanceFixture() (*MigrationData, *TargetSnapshot) {
	user := func(id, email string) doppler.WorkplaceUser {
		u := doppler.WorkplaceUser{ID: id}
		u.User.Email = email
		return u
	}
	role := func(id string) *doppler.GroupDefaultRole { return &doppler.GroupDefaultRole{Identifier: id} }

	data := &MigrationData{
		WorkplaceUsers: []doppler.WorkplaceUser{user("src-ana", "ana@example.com"), user("src-bob", "bob@example.com")},
		Groups: []GroupWithMembers{{
			Group: doppler.Group{Name: "Engineering", Slug: "src-grp"},
			Members: []doppler.GroupMember{
				{Type: "service_account", Slug: "src-sa"},
				{Type: "workplace_user", Slug: "src-ana"},
				{Type: "workplace_user", Slug: "src-bob"}, // not provisioned on the target
			},
		}},
		ServiceAccounts: []doppler.ServiceAccount{{Name: "CI", Slug: "src-sa", WorkplaceRole: role("viewer")}},
		Projects: []ProjectData{{
			Project: doppler.Project{Slug: "backend", Name: "backend"},
			Members: []doppler.ProjectMember{
				{Type: "group", Slug: "src-grp", Role: doppler.MemberRole{Identifier: "viewer"}},
				{Type: "service_account", Slug: "src-sa", Role: doppler.MemberRole{Identifier: "collaborator"}},
				{Type: "workplace_user", Slug: "src-ana", Role: doppler.MemberRole{Identifier: "viewer"}},
				{Type: "workplace_user", Slug: "src-bob", Role: doppler.MemberRole{Identifier: "viewer"}},
			},
			Webhooks: []doppler.Webhook{{ID: "src-hook", URL: "https://hooks.example.com/a", Enabled: true}},
		}},
	}

	snap := &TargetSnapshot{
		WorkplaceRoles: map[string]doppler.Role{},
		ProjectRoles:   map[string]doppler.Role{},
		Groups: map[string]GroupSnapshot{
			"tgt-grp": {
				Group:   doppler.Group{Name: "Engineering", Slug: "tgt-grp"},
				Members: []doppler.GroupMember{{Type: "service_account", Slug: "tgt-sa"}},
			},
		},
		ServiceAccounts: map[string]doppler.ServiceAccount{
			"tgt-sa": {Name: "CI", Slug: "tgt-sa", WorkplaceRole: role("admin")},
		},
		Users: map[string]doppler.WorkplaceUser{"ana@example.com": user("tgt-ana", "ana@example.com")},
		Projects: map[string]ProjectSnapshot{
			"backend": {
				Project:      doppler.Project{Slug: "backend"},
				Environments: map[string]doppler.Environment{},
				Configs:      map[string]ConfigSnapshot{},
				Webhooks: map[string]doppler.Webhook{
					"https://hooks.example.com/a": {ID: "tgt-hook", URL: "https://hooks.example.com/a", Enabled: false},
				},
				Members: map[string]doppler.ProjectMember{
					"group:tgt-grp":          {Type: "group", Slug: "tgt-grp", Role: doppler.MemberRole{Identifier: "admin"}},
					"workplace_user:tgt-ana": {Type: "workplace_user", Slug: "tgt-ana", Role: doppler.MemberRole{Identifier: "viewer"}},
				},
			},
		},
	}
	return data, snap
}

func TestDiffMatchesIdentitiesAcrossInstances(t *testing.T) {
	data, snap := crossInstanceFixture()
	d := ComputeDiff(data, snap)

	byKey := map[string]Difference{}
	for _, diff := range d.Differences {
		byKey[string(diff.Kind)+" "+diff.Name] = diff
	}

	// The group and account exist under other slugs: neither is a create.
	if _, ok := byKey["group Engineering"]; ok {
		t.Error("the group exists on the target by name and should not be recreated")
	}
	if sa := byKey["service_account CI"]; sa.Disposition != DispositionUpdate || sa.TargetID != "tgt-sa" {
		t.Errorf("service account: %+v, want an update of tgt-sa", sa)
	}

	// Membership differs only by Ana, once both sides are target identities.
	// Bob is not on the target and cannot be added, so he is not a difference.
	if gm := byKey["group_members Engineering"]; gm.TargetID != "tgt-grp" || gm.Changes[0].Now != "2 members" {
		t.Errorf("group members: %+v", gm)
	}

	if m := byKey["project_member group:src-grp"]; m.Disposition != DispositionUpdate || m.TargetID != "tgt-grp" || m.Label != "group Engineering" {
		t.Errorf("group member: %+v", m)
	}
	if m := byKey["project_member service_account:src-sa"]; m.Disposition != DispositionCreate || m.TargetID != "tgt-sa" {
		t.Errorf("account member: %+v", m)
	}
	if _, ok := byKey["project_member workplace_user:src-ana"]; ok {
		t.Error("Ana is already a member with the same role")
	}
	if _, ok := byKey["project_member workplace_user:src-bob"]; ok {
		t.Error("Bob is not on the target; he should not be a difference")
	}
	if d.Matched[KindProjectMember] != 1 {
		t.Errorf("matched members = %d, want 1", d.Matched[KindProjectMember])
	}

	if wh := byKey["webhook https://hooks.example.com/a"]; wh.TargetID != "tgt-hook" {
		t.Errorf("webhook: %+v", wh)
	}
}

func TestConvergerWritesUnderTargetIdentities(t *testing.T) {
	// The report: a top-up of a previously migrated project failed every member
	// write, because each named a source slug the target had never seen.
	data, snap := crossInstanceFixture()
	target := newMockReadWriteClient()

	conv := NewConverger(target, data, ComputeDiff(data, snap), DefaultConvergeOptions(), NewErrorStore(), nil)
	result, err := conv.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Failed != 0 {
		t.Fatalf("%d failed", result.Failed)
	}

	if !reflect.DeepEqual(target.updatedServiceAccounts, []string{"tgt-sa"}) {
		t.Errorf("updated accounts %v", target.updatedServiceAccounts)
	}
	wantMembers := []doppler.GroupMember{{Type: "service_account", Slug: "tgt-sa"}, {Type: "workplace_user", Slug: "tgt-ana"}}
	if got := target.replacedMembers["tgt-grp"]; !reflect.DeepEqual(got, wantMembers) {
		t.Errorf("group members written %v, want %v", got, wantMembers)
	}
	if !reflect.DeepEqual(target.updatedMembers, []string{"backend/group:tgt-grp=viewer"}) {
		t.Errorf("updated project members %v", target.updatedMembers)
	}
	if len(target.addedProjMembers) != 1 || target.addedProjMembers[0].MemberSlug != "tgt-sa" {
		t.Errorf("added project members %+v", target.addedProjMembers)
	}
	if !reflect.DeepEqual(target.updatedWebhooks, []string{"backend/tgt-hook"}) {
		t.Errorf("updated webhooks %v", target.updatedWebhooks)
	}
}

func TestConvergerResolvesAGroupItCreated(t *testing.T) {
	// A group new to the target has no slug there when the diff is made. A
	// project member naming it is resolved once the group exists.
	data, snap := crossInstanceFixture()
	delete(snap.Groups, "tgt-grp")
	delete(snap.Projects["backend"].Members, "group:tgt-grp")
	target := newMockReadWriteClient() // CreateGroup answers with the name as the slug

	conv := NewConverger(target, data, ComputeDiff(data, snap), DefaultConvergeOptions(), NewErrorStore(), nil)
	if _, err := conv.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	if _, ok := target.replacedMembers["Engineering"]; !ok {
		t.Errorf("the new group's membership was not written: %v", target.replacedMembers)
	}
	var groupMember *projMemberCall
	for i := range target.addedProjMembers {
		if target.addedProjMembers[i].MemberType == "group" {
			groupMember = &target.addedProjMembers[i]
		}
	}
	if groupMember == nil || groupMember.MemberSlug != "Engineering" {
		t.Errorf("project member for the new group: %+v", groupMember)
	}
}

func TestAMemberWithAdminAccessEverywhereIsNotAFailure(t *testing.T) {
	// The report: a project deleted from the target was recreated by a top-up,
	// and adding its members failed for the two with workplace-wide admin —
	// the target already gives them every project and refuses a role on top.
	data, snap := crossInstanceFixture()
	delete(snap.Projects, "backend")

	target := newMockReadWriteClient()
	target.addProjMemberErr["backend/tgt-ana"] = &doppler.APIError{
		StatusCode: 400,
		Message:    "Cannot set project role for workplace user with admin access to all projects.",
	}

	errs := NewErrorStore()
	conv := NewConverger(target, data, ComputeDiff(data, snap), DefaultConvergeOptions(), errs, nil)
	result, err := conv.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Failed != 0 {
		t.Fatalf("%d failed; a member who already has access is not a failure", result.Failed)
	}
	if total, _, _ := errs.Counts(); total != 0 {
		t.Fatalf("%d errors recorded", total)
	}
	if result.Skipped == 0 {
		t.Fatal("the member should be counted as skipped")
	}
}

func TestAProjectCreatedWholeGetsItsSecrets(t *testing.T) {
	// A project deleted from the target and recreated by a top-up arrived with
	// every config empty: the diff counts a missing project as one create, so
	// no secret differences existed for the secrets stage to write.
	data, snap := crossInstanceFixture()
	data.Projects[0].Configs = []ConfigData{{
		Config:      doppler.Config{Name: "prd", Environment: "prd"},
		Secrets:     doppler.SecretsMap{"API_KEY": {Raw: "k", RawVisibility: "masked"}, "DB_URL": {Raw: "d", RawVisibility: "restricted"}},
		SecretCount: 2,
	}}
	delete(snap.Projects, "backend")

	target := newMockReadWriteClient()
	conv := NewConverger(target, data, ComputeDiff(data, snap), DefaultConvergeOptions(), NewErrorStore(), nil)
	result, err := conv.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	written := target.secretChanges["backend/prd"]
	if len(written) != 2 || written[0].Name != "API_KEY" || written[1].Name != "DB_URL" {
		t.Fatalf("secrets written %+v", written)
	}
	if *written[1].Visibility != "restricted" {
		t.Error("visibility should come across")
	}
	if result.SecretsWritten != 2 {
		t.Errorf("secrets written = %d", result.SecretsWritten)
	}
	// Recorded, so later comparisons look for changes made there since.
	if len(result.Writes) != 1 || result.Writes[0].Project != "backend" || result.Writes[0].Config != "prd" {
		t.Errorf("writes %+v", result.Writes)
	}
}
