package migration

import (
	"testing"

	"github.com/doppleruniversity/workplace-migrator/internal/doppler"
)

func TestExcludeServiceAccountRemovesItAndEverythingNamingIt(t *testing.T) {
	data := &MigrationData{
		ServiceAccounts: []doppler.ServiceAccount{{Slug: "self", Name: "Migration Source"}, {Slug: "ci", Name: "CI"}},
		ServiceAccountTokens: []doppler.ServiceAccountToken{
			{ServiceAccount: "self", Name: "migrator"}, {ServiceAccount: "ci", Name: "deploy"},
		},
		ServiceAccountIdentities: []doppler.ServiceAccountIdentity{{ServiceAccount: "self"}, {ServiceAccount: "ci"}},
		Groups: []GroupWithMembers{{Members: []doppler.GroupMember{
			{Type: "service_account", Slug: "self"}, {Type: "service_account", Slug: "ci"}, {Type: "workplace_user", Slug: "self"},
		}}},
		Projects: []ProjectData{{Members: []doppler.ProjectMember{
			{Type: "service_account", Slug: "self"}, {Type: "group", Slug: "eng"},
		}}},
	}

	removed := ExcludeServiceAccount(data, "self")
	if removed == nil || removed.Name != "Migration Source" || data.ExcludedServiceAccount != removed {
		t.Fatalf("removed %+v", removed)
	}
	if len(data.ServiceAccounts) != 1 || data.ServiceAccounts[0].Slug != "ci" {
		t.Errorf("accounts %+v", data.ServiceAccounts)
	}
	if len(data.ServiceAccountTokens) != 1 || len(data.ServiceAccountIdentities) != 1 {
		t.Errorf("tokens %d, identities %d", len(data.ServiceAccountTokens), len(data.ServiceAccountIdentities))
	}
	// Only the account's own membership goes; a user who happens to share the
	// slug string is somebody else.
	if len(data.Groups[0].Members) != 2 {
		t.Errorf("group members %+v", data.Groups[0].Members)
	}
	if len(data.Projects[0].Members) != 1 || data.Projects[0].Members[0].Slug != "eng" {
		t.Errorf("project members %+v", data.Projects[0].Members)
	}
}

func TestExcludeServiceAccountLeavesDataAloneWhenNoneMatches(t *testing.T) {
	data := &MigrationData{ServiceAccounts: []doppler.ServiceAccount{{Slug: "ci"}}}
	if ExcludeServiceAccount(data, "self") != nil || len(data.ServiceAccounts) != 1 || data.ExcludedServiceAccount != nil {
		t.Fatal("nothing should change")
	}
}
