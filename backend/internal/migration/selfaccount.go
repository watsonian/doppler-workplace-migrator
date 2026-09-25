package migration

import "github.com/doppleruniversity/workplace-migrator/internal/doppler"

// ExcludeServiceAccount takes the service account the source token belongs to
// out of the migration, along with its tokens, identities and memberships.
//
// That account exists to run this migration. Carrying it across gives the
// target an account nobody asked for, with a workplace role broad enough to
// read everything, and a worklist row offering to mint it a fresh token. Its
// memberships go too: without the account they name nobody on the target.
//
// Returns the account removed, or nil if the slug named none.
func ExcludeServiceAccount(data *MigrationData, slug string) *doppler.ServiceAccount {
	if data == nil || slug == "" {
		return nil
	}

	var removed *doppler.ServiceAccount
	accounts := data.ServiceAccounts[:0:0]
	for _, sa := range data.ServiceAccounts {
		if sa.Slug == slug {
			sa := sa
			removed = &sa
			continue
		}
		accounts = append(accounts, sa)
	}
	if removed == nil {
		return nil
	}
	data.ServiceAccounts = accounts
	data.ExcludedServiceAccount = removed

	tokens := data.ServiceAccountTokens[:0:0]
	for _, t := range data.ServiceAccountTokens {
		if t.ServiceAccount != slug {
			tokens = append(tokens, t)
		}
	}
	data.ServiceAccountTokens = tokens

	identities := data.ServiceAccountIdentities[:0:0]
	for _, id := range data.ServiceAccountIdentities {
		if id.ServiceAccount != slug {
			identities = append(identities, id)
		}
	}
	data.ServiceAccountIdentities = identities

	isSelf := func(memberType, memberSlug string) bool {
		return memberType == "service_account" && memberSlug == slug
	}
	for i := range data.Groups {
		members := data.Groups[i].Members[:0:0]
		for _, m := range data.Groups[i].Members {
			if !isSelf(m.Type, m.Slug) {
				members = append(members, m)
			}
		}
		data.Groups[i].Members = members
	}
	for i := range data.Projects {
		members := data.Projects[i].Members[:0:0]
		for _, m := range data.Projects[i].Members {
			if !isSelf(m.Type, m.Slug) {
				members = append(members, m)
			}
		}
		data.Projects[i].Members = members
	}

	return removed
}
