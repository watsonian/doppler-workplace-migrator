package migration

import (
	"strings"

	"github.com/doppler/workplace-migrator/internal/doppler"
)

// crossIndex translates source identities into target ones.
//
// Groups, service accounts and users all have slugs the instance assigns — a
// UUID for a group or an account, an ID for a user — so the same thing has a
// different slug on each side. What carries across is the name, for a group
// or an account, and the email, for a user. The executor matches on those
// (see identityMaps); the diff and the converger go through this so a top-up
// compares and writes the same identities a migration did.
type crossIndex struct {
	groups   map[string]string // source slug → target slug
	accounts map[string]string // source slug → target slug
	users    map[string]string // any source user identifier → target workplace user ID

	// Names for labels: a slug is a UUID and says nothing to a person.
	groupNames   map[string]string // source slug → name
	accountNames map[string]string // source slug → name
	userEmails   map[string]string // any source user identifier → email
}

func buildCrossIndex(data *MigrationData, snap *TargetSnapshot) *crossIndex {
	x := &crossIndex{
		groups: map[string]string{}, accounts: map[string]string{}, users: map[string]string{},
		groupNames: map[string]string{}, accountNames: map[string]string{}, userEmails: map[string]string{},
	}
	if data == nil {
		return x
	}

	targetGroups := map[string]string{}
	targetAccounts := map[string]string{}
	targetUsers := map[string]string{}
	if snap != nil {
		for _, g := range snap.Groups {
			targetGroups[g.Group.Name] = g.Group.Slug
		}
		for _, sa := range snap.ServiceAccounts {
			targetAccounts[sa.Name] = sa.Slug
		}
		for email, u := range snap.Users {
			targetUsers[strings.ToLower(email)] = u.ID
		}
	}

	for _, g := range data.Groups {
		x.groupNames[g.Group.Slug] = g.Group.Name
		if slug, ok := targetGroups[g.Group.Name]; ok {
			x.groups[g.Group.Slug] = slug
		}
	}
	for _, sa := range data.ServiceAccounts {
		x.accountNames[sa.Slug] = sa.Name
		if slug, ok := targetAccounts[sa.Name]; ok {
			x.accounts[sa.Slug] = slug
		}
	}
	for _, u := range data.WorkplaceUsers {
		email := u.User.Email
		if email == "" {
			continue
		}
		for _, id := range []string{u.ID, u.User.Username, email} {
			if id == "" {
				continue
			}
			x.userEmails[id] = email
			if targetID, ok := targetUsers[strings.ToLower(email)]; ok {
				x.users[id] = targetID
			}
		}
	}
	return x
}

// member returns the target slug for a source member, and whether there is
// one. A group or account this run is about to create has none yet; the
// converger adds it once created.
func (x *crossIndex) member(memberType, sourceSlug string) (string, bool) {
	var slug string
	var ok bool
	switch memberType {
	case "group":
		slug, ok = x.groups[sourceSlug]
	case "service_account":
		slug, ok = x.accounts[sourceSlug]
	case "workplace_user":
		slug, ok = x.users[sourceSlug]
	}
	return slug, ok
}

// label names a source member for a person: "group Engineering",
// "service account CI", "user ana@example.com".
func (x *crossIndex) label(memberType, sourceSlug string) string {
	switch memberType {
	case "group":
		if n := x.groupNames[sourceSlug]; n != "" {
			return "group " + n
		}
	case "service_account":
		if n := x.accountNames[sourceSlug]; n != "" {
			return "service account " + n
		}
	case "workplace_user":
		if e := x.userEmails[sourceSlug]; e != "" {
			return "user " + e
		}
	}
	return memberType + " " + sourceSlug
}

// translateMembers maps a group's source members onto target slugs. Members
// with no target counterpart are left out and counted: a user not yet
// provisioned cannot be added, and comparing against them would report a
// difference no run could close.
func (x *crossIndex) translateMembers(members []doppler.GroupMember) ([]doppler.GroupMember, int) {
	out := make([]doppler.GroupMember, 0, len(members))
	missing := 0
	for _, m := range members {
		slug, ok := x.member(m.Type, m.Slug)
		if !ok {
			missing++
			continue
		}
		out = append(out, doppler.GroupMember{Type: m.Type, Slug: slug})
	}
	return out, missing
}
