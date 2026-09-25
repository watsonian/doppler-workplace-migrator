package migration

import (
	"fmt"
	"sort"
	"strings"

	"github.com/doppleruniversity/workplace-migrator/internal/doppler"
)

// Disposition is what a run will do about one difference.
type Disposition string

const (
	// DispositionCreate: on the source, absent from the target.
	DispositionCreate Disposition = "create"
	// DispositionUpdate: on both, and not the same.
	DispositionUpdate Disposition = "update"
	// DispositionMatch: on both and identical. Counted, never acted on — the
	// count is what tells you a run looked at everything and changed only what
	// needed changing.
	DispositionMatch Disposition = "match"
	// DispositionOrphan: on the target, absent from the source. Deleted only
	// when a run is told to; deferred to cutover otherwise.
	DispositionOrphan Disposition = "orphan"
)

// ResourceKind groups differences by what they are.
type ResourceKind string

const (
	KindWorkplaceRole ResourceKind = "workplace_role"
	KindProjectRole   ResourceKind = "project_role"
	KindGroup         ResourceKind = "group"
	KindGroupMembers  ResourceKind = "group_members"
	KindServiceAcct   ResourceKind = "service_account"
	KindProject       ResourceKind = "project"
	KindEnvironment   ResourceKind = "environment"
	KindConfig        ResourceKind = "config"
	KindSecret        ResourceKind = "secret"
	KindWebhook       ResourceKind = "webhook"
	KindProjectMember ResourceKind = "project_member"
	KindTrustedIPs    ResourceKind = "trusted_ips"
)

// FieldChange is one field that differs, named on both sides.
//
// Values here are safe to show. A secret's value never appears — the secret
// change records the word "value" and stops there.
type FieldChange struct {
	Field string `json:"field"`
	Was   string `json:"was"`
	Now   string `json:"now"`
}

// Difference is one thing that is not the same between source and target.
type Difference struct {
	Kind        ResourceKind `json:"kind"`
	Disposition Disposition  `json:"disposition"`

	// Project and Config locate the item; Name identifies it within that scope.
	Project string `json:"project,omitempty"`
	Config  string `json:"config,omitempty"`
	Name    string `json:"name"`

	// Scope is how the item reads in a list: "payments/prd" for a config-scoped
	// thing, the bare name otherwise.
	Scope string `json:"scope,omitempty"`

	// Changes are the fields that differ, for an update.
	Changes []FieldChange `json:"changes,omitempty"`

	// Weight is how much is at stake, for totalling a selection. Secrets carry
	// one each; a config carries its secret count.
	Weight int `json:"weight,omitempty"`

	// Contains counts what a project create brings with it, by kind. Those
	// items are not listed as differences of their own — the converger builds
	// a new project whole, and listing each would bury the fact that the
	// project is new — but they are still additions, and a comparison that
	// reported them as nothing would say the target already had them.
	Contains map[ResourceKind]int `json:"contains,omitempty"`

	// TargetID is the target's identifier for the item, where it differs from
	// the source's: a group's or account's slug, a webhook's ID, a member's
	// target slug. Groups, accounts and users are assigned slugs by each
	// instance, so writing to the target under the source's gets a 404.
	TargetID string `json:"target_id,omitempty"`

	// Label is how the item reads, when Name is an identifier that means
	// nothing to a person — "group Engineering" rather than a UUID.
	Label string `json:"label,omitempty"`
}

// Key uniquely identifies a difference, for selection and for re-running.
func (d Difference) Key() string {
	return fmt.Sprintf("%s|%s|%s|%s", d.Kind, d.Project, d.Config, d.Name)
}

// Diff is the whole comparison.
type Diff struct {
	Differences []Difference `json:"differences"`

	// Matched counts identical items by kind. Never acted on, always reported:
	// "17,696 already current" is what makes a top-up trustworthy, and a run
	// that only reports what it wrote says nothing about what it looked at.
	Matched map[ResourceKind]int `json:"matched"`

	// Drift is what changed on the target after we wrote it, from the config
	// logs rather than from any record this tool keeps.
	Drift []DriftRecord `json:"drift,omitempty"`

	// DriftUnavailable explains why drift could not be determined, when it
	// could not. Reporting no drift and being unable to tell are different
	// answers and must not look the same.
	DriftUnavailable string `json:"drift_unavailable,omitempty"`

	// ids translates source identities to target ones, for the converger.
	ids *crossIndex
}

// DriftRecord is a change made to the target by somebody other than this
// migrator, after this migrator last wrote.
type DriftRecord struct {
	Project   string `json:"project"`
	Config    string `json:"config"`
	Scope     string `json:"scope"`
	ChangedAt string `json:"changed_at"`
	ChangedBy string `json:"changed_by"`
	Summary   string `json:"summary"`
}

// Counts summarises a diff for a heading.
type Counts struct {
	Create int `json:"create"`
	Update int `json:"update"`
	Match  int `json:"match"`
	Orphan int `json:"orphan"`
	Drift  int `json:"drift"`
}

// Counts totals the differences by disposition.
func (d *Diff) Counts() Counts {
	var c Counts
	for _, diff := range d.Differences {
		switch diff.Disposition {
		case DispositionCreate:
			c.Create++
			for _, n := range diff.Contains {
				c.Create += n
			}
		case DispositionUpdate:
			c.Update++
		case DispositionOrphan:
			c.Orphan++
		}
	}
	for _, n := range d.Matched {
		c.Match += n
	}
	c.Drift = len(d.Drift)
	return c
}

// ByDisposition returns the differences of one disposition, in a stable order.
func (d *Diff) ByDisposition(disp Disposition) []Difference {
	out := []Difference{}
	for _, diff := range d.Differences {
		if diff.Disposition == disp {
			out = append(out, diff)
		}
	}
	return out
}

// Orphans returns everything on the target that the source no longer has.
func (d *Diff) Orphans() []Difference { return d.ByDisposition(DispositionOrphan) }

// ComputeDiff compares gathered source data against a target snapshot.
//
// A nil or empty snapshot yields a diff in which everything is a create, which
// is what a first migration is — so the same code drives run one and run five.
func ComputeDiff(data *MigrationData, snap *TargetSnapshot) *Diff {
	// Empty rather than nil, so a comparison that finds nothing reaches the
	// browser as [] and not null — which the Compare page, looping over it,
	// crashed on for exactly the result a top-up most hopes for.
	d := &Diff{Differences: []Difference{}, Matched: map[ResourceKind]int{}}
	if data == nil {
		d.ids = buildCrossIndex(nil, nil)
		return d
	}
	if snap == nil {
		snap = &TargetSnapshot{
			WorkplaceRoles:  map[string]doppler.Role{},
			ProjectRoles:    map[string]doppler.Role{},
			Groups:          map[string]GroupSnapshot{},
			ServiceAccounts: map[string]doppler.ServiceAccount{},
			Projects:        map[string]ProjectSnapshot{},
		}
	}

	d.ids = buildCrossIndex(data, snap)

	diffRoles(d, data, snap)
	diffGroups(d, data, snap)
	diffServiceAccounts(d, data, snap)
	diffProjects(d, data, snap)

	sort.SliceStable(d.Differences, func(i, j int) bool {
		if d.Differences[i].Kind != d.Differences[j].Kind {
			return d.Differences[i].Kind < d.Differences[j].Kind
		}
		return d.Differences[i].Scope+d.Differences[i].Name < d.Differences[j].Scope+d.Differences[j].Name
	})

	return d
}

func diffRoles(d *Diff, data *MigrationData, snap *TargetSnapshot) {
	for _, role := range data.WorkplaceRoles {
		existing, ok := snap.WorkplaceRoles[role.Identifier]
		switch {
		case !ok:
			d.add(Difference{Kind: KindWorkplaceRole, Disposition: DispositionCreate, Name: role.Name})
		case !samePermissions(role.Permissions, existing.Permissions):
			d.add(Difference{
				Kind: KindWorkplaceRole, Disposition: DispositionUpdate, Name: role.Name,
				Changes: []FieldChange{{
					Field: "permissions",
					Was:   describeCount(len(existing.Permissions), "permission"),
					Now:   describeCount(len(role.Permissions), "permission"),
				}},
			})
		default:
			d.Matched[KindWorkplaceRole]++
		}
	}

	for _, role := range data.ProjectRoles {
		existing, ok := snap.ProjectRoles[role.Identifier]
		switch {
		case !ok:
			d.add(Difference{Kind: KindProjectRole, Disposition: DispositionCreate, Name: role.Name})
		case !samePermissions(role.Permissions, existing.Permissions):
			d.add(Difference{
				Kind: KindProjectRole, Disposition: DispositionUpdate, Name: role.Name,
				Changes: []FieldChange{{
					Field: "permissions",
					Was:   describeCount(len(existing.Permissions), "permission"),
					Now:   describeCount(len(role.Permissions), "permission"),
				}},
			})
		default:
			d.Matched[KindProjectRole]++
		}
	}
}

func diffGroups(d *Diff, data *MigrationData, snap *TargetSnapshot) {
	for _, gwm := range data.Groups {
		// Matched by name: the target's copy has its own slug.
		targetSlug, ok := d.ids.groups[gwm.Group.Slug]
		if !ok {
			d.add(Difference{Kind: KindGroup, Disposition: DispositionCreate, Name: gwm.Group.Name})
			continue
		}
		existing := snap.Groups[targetSlug]

		var changes []FieldChange
		if gwm.Group.Name != existing.Group.Name {
			changes = append(changes, FieldChange{Field: "name", Was: existing.Group.Name, Now: gwm.Group.Name})
		}
		if role := roleIdentifier(gwm.Group.DefaultRole); role != roleIdentifier(existing.Group.DefaultRole) {
			changes = append(changes, FieldChange{
				Field: "default role", Was: roleIdentifier(existing.Group.DefaultRole), Now: role,
			})
		}
		if len(changes) > 0 {
			d.add(Difference{Kind: KindGroup, Disposition: DispositionUpdate, Name: gwm.Group.Name, Changes: changes, TargetID: targetSlug})
		} else {
			d.Matched[KindGroup]++
		}

		// Membership is compared separately because it is fixed separately: the
		// API replaces the whole list, so it is one difference however many
		// members moved.
		// Compared as target identities. Members with none — users not yet
		// provisioned — are left out of both the comparison and the write.
		members, _ := d.ids.translateMembers(gwm.Members)
		if !sameMembers(members, existing.Members) {
			d.add(Difference{
				Kind: KindGroupMembers, Disposition: DispositionUpdate, Name: gwm.Group.Name, TargetID: targetSlug,
				Changes: []FieldChange{{
					Field: "members",
					Was:   describeCount(len(existing.Members), "member"),
					Now:   describeCount(len(members), "member"),
				}},
			})
		} else {
			d.Matched[KindGroupMembers]++
		}
	}
}

func diffServiceAccounts(d *Diff, data *MigrationData, snap *TargetSnapshot) {
	for _, account := range data.ServiceAccounts {
		// Matched by name, like groups.
		targetSlug, ok := d.ids.accounts[account.Slug]
		if !ok {
			d.add(Difference{Kind: KindServiceAcct, Disposition: DispositionCreate, Name: account.Name})
			continue
		}
		existing := snap.ServiceAccounts[targetSlug]
		var changes []FieldChange
		if account.Name != existing.Name {
			changes = append(changes, FieldChange{Field: "name", Was: existing.Name, Now: account.Name})
		}
		if role := roleIdentifier(account.WorkplaceRole); role != roleIdentifier(existing.WorkplaceRole) {
			changes = append(changes, FieldChange{
				Field: "workplace role", Was: roleIdentifier(existing.WorkplaceRole), Now: role,
			})
		}
		if len(changes) > 0 {
			d.add(Difference{Kind: KindServiceAcct, Disposition: DispositionUpdate, Name: account.Name, Changes: changes, TargetID: targetSlug})
		} else {
			d.Matched[KindServiceAcct]++
		}
	}
}

func diffProjects(d *Diff, data *MigrationData, snap *TargetSnapshot) {
	seenProjects := map[string]struct{}{}

	for i := range data.Projects {
		pd := &data.Projects[i]
		seenProjects[pd.Project.Slug] = struct{}{}

		ps, ok := snap.Projects[pd.Project.Slug]
		if !ok {
			d.add(Difference{
				Kind: KindProject, Disposition: DispositionCreate,
				Project: pd.Project.Slug, Name: pd.Project.Slug,
				Weight:   countSecrets(pd),
				Contains: projectContents(pd),
			})
			// Everything beneath a project that does not exist is a create too,
			// but listing each one would bury the fact that the project is new.
			// Contains carries the counts instead.
			continue
		}
		d.Matched[KindProject]++

		diffEnvironments(d, pd, ps)
		diffConfigs(d, pd, ps)
		diffWebhooks(d, pd, ps)
		diffProjectMembers(d, d.ids, pd, ps)
	}

	for slug, ps := range snap.Projects {
		if _, ok := seenProjects[slug]; !ok {
			d.add(Difference{
				Kind: KindProject, Disposition: DispositionOrphan,
				Project: slug, Name: slug, Scope: slug,
				Weight: len(ps.Configs),
			})
		}
	}
}

func diffEnvironments(d *Diff, pd *ProjectData, ps ProjectSnapshot) {
	for _, env := range pd.Environments {
		existing, ok := ps.Environments[env.Slug]
		switch {
		case !ok:
			d.add(Difference{
				Kind: KindEnvironment, Disposition: DispositionCreate,
				Project: pd.Project.Slug, Name: env.Slug, Scope: pd.Project.Slug,
			})
		case env.Name != existing.Name:
			d.add(Difference{
				Kind: KindEnvironment, Disposition: DispositionUpdate,
				Project: pd.Project.Slug, Name: env.Slug, Scope: pd.Project.Slug,
				Changes: []FieldChange{{Field: "name", Was: existing.Name, Now: env.Name}},
			})
		default:
			d.Matched[KindEnvironment]++
		}
	}
}

func diffConfigs(d *Diff, pd *ProjectData, ps ProjectSnapshot) {
	seen := map[string]struct{}{}

	for i := range pd.Configs {
		cd := &pd.Configs[i]
		seen[cd.Config.Name] = struct{}{}
		scope := pd.Project.Slug + "/" + cd.Config.Name

		cs, ok := ps.Configs[cd.Config.Name]
		if !ok {
			d.add(Difference{
				Kind: KindConfig, Disposition: DispositionCreate,
				Project: pd.Project.Slug, Config: cd.Config.Name, Name: cd.Config.Name,
				Scope: scope, Weight: cd.SecretCount,
			})
			continue
		}
		d.Matched[KindConfig]++

		diffSecrets(d, pd, cd, cs, scope)
		diffTrustedIPs(d, pd, cd, cs, scope)
	}

	for name, cs := range ps.Configs {
		if _, ok := seen[name]; !ok {
			d.add(Difference{
				Kind: KindConfig, Disposition: DispositionOrphan,
				Project: pd.Project.Slug, Config: name, Name: name,
				Scope: pd.Project.Slug + "/" + name, Weight: len(cs.Secrets),
			})
		}
	}
}

func diffSecrets(d *Diff, pd *ProjectData, cd *ConfigData, cs ConfigSnapshot, scope string) {
	// The source side's values were dropped after an earlier write and not
	// reloaded. Diffing anyway would see a source with no secrets and call every
	// secret on the target an orphan — and a run asked to delete orphans would
	// delete them all. Callers reload first (ReloadSecrets); this is the backstop
	// if one does not, reported as a single difference with no orphans at all.
	if valuesDropped(cd) {
		d.add(Difference{
			Kind: KindSecret, Disposition: DispositionUpdate,
			Project: pd.Project.Slug, Config: cd.Config.Name, Name: "(source values not loaded)",
			Scope:   scope,
			Changes: []FieldChange{{Field: "secrets", Was: "not loaded from the source", Now: describeCount(cd.SecretCount, "secret")}},
		})
		return
	}

	// A nil map means the target's secrets could not be read. Treating that as
	// "none exist" would propose rewriting every secret in the config, so it is
	// reported as one difference instead.
	if cs.Secrets == nil {
		d.add(Difference{
			Kind: KindSecret, Disposition: DispositionUpdate,
			Project: pd.Project.Slug, Config: cd.Config.Name, Name: "(unreadable)",
			Scope:   scope,
			Changes: []FieldChange{{Field: "secrets", Was: "could not be read", Now: describeCount(cd.SecretCount, "secret")}},
		})
		return
	}

	seen := map[string]struct{}{}
	for name, secret := range cd.Secrets {
		seen[name] = struct{}{}
		want := FingerprintOf(secret)

		have, ok := cs.Secrets[name]
		switch {
		case !ok:
			d.add(Difference{
				Kind: KindSecret, Disposition: DispositionCreate,
				Project: pd.Project.Slug, Config: cd.Config.Name, Name: name, Scope: scope, Weight: 1,
			})
		case !want.Matches(have):
			d.add(Difference{
				Kind: KindSecret, Disposition: DispositionUpdate,
				Project: pd.Project.Slug, Config: cd.Config.Name, Name: name, Scope: scope, Weight: 1,
				// One row per field. The value's row names it and stops there:
				// this is the plan screen, and a value here would be on screen,
				// in a screenshot and in the export. The metadata beside it —
				// visibility, type, note — is shown as it was and will be.
				Changes: want.FieldChanges(have),
			})
		default:
			d.Matched[KindSecret]++
		}
	}

	for name := range cs.Secrets {
		if _, ok := seen[name]; !ok {
			d.add(Difference{
				Kind: KindSecret, Disposition: DispositionOrphan,
				Project: pd.Project.Slug, Config: cd.Config.Name, Name: name, Scope: scope, Weight: 1,
			})
		}
	}
}

func diffTrustedIPs(d *Diff, pd *ProjectData, cd *ConfigData, cs ConfigSnapshot, scope string) {
	source := doppler.NewTrustedIPSet(cd.TrustedIPs)
	if len(source) == 0 {
		// The source reported nothing, which is not the same as reporting the
		// default. Nothing to mirror either way.
		return
	}

	plan := doppler.PlanTrustedIPs(source, cs.TrustedIPs)
	if plan.Empty() {
		d.Matched[KindTrustedIPs]++
		return
	}

	changes := []FieldChange{}
	if len(plan.Add) > 0 {
		changes = append(changes, FieldChange{Field: "add", Now: strings.Join(plan.Add, ", ")})
	}
	if len(plan.Remove) > 0 {
		changes = append(changes, FieldChange{Field: "remove", Was: strings.Join(plan.Remove, ", ")})
	}

	d.add(Difference{
		Kind: KindTrustedIPs, Disposition: DispositionUpdate,
		Project: pd.Project.Slug, Config: cd.Config.Name, Name: cd.Config.Name,
		Scope: scope, Changes: changes,
	})
}

func diffWebhooks(d *Diff, pd *ProjectData, ps ProjectSnapshot) {
	for _, wh := range pd.Webhooks {
		existing, ok := ps.Webhooks[wh.URL]
		if !ok {
			d.add(Difference{
				Kind: KindWebhook, Disposition: DispositionCreate,
				Project: pd.Project.Slug, Name: wh.URL, Scope: pd.Project.Slug,
			})
			continue
		}
		var changes []FieldChange
		if wh.Enabled != existing.Enabled {
			changes = append(changes, FieldChange{
				Field: "enabled", Was: fmt.Sprintf("%t", existing.Enabled), Now: fmt.Sprintf("%t", wh.Enabled),
			})
		}
		if !sameStrings(wh.EnabledConfigs, existing.EnabledConfigs) {
			changes = append(changes, FieldChange{
				Field: "configs",
				Was:   describeCount(len(existing.EnabledConfigs), "config"),
				Now:   describeCount(len(wh.EnabledConfigs), "config"),
			})
		}
		if len(changes) > 0 {
			d.add(Difference{
				Kind: KindWebhook, Disposition: DispositionUpdate,
				Project: pd.Project.Slug, Name: wh.URL, Scope: pd.Project.Slug, Changes: changes,
				TargetID: existing.ID,
			})
		} else {
			d.Matched[KindWebhook]++
		}
	}
}

func diffProjectMembers(d *Diff, ids *crossIndex, pd *ProjectData, ps ProjectSnapshot) {
	for _, m := range pd.Members {
		// Name keeps the source's identity, which is what the converger looks
		// the member up by; the comparison is against the target's.
		key := m.Type + ":" + m.Slug
		label := ids.label(m.Type, m.Slug)
		targetSlug, ok := ids.member(m.Type, m.Slug)
		if !ok {
			if m.Type == "workplace_user" {
				// Not provisioned on the target. Nothing can add them until they
				// are; the user check reports who is missing.
				continue
			}
			// A group or account this run creates. The converger resolves it
			// once it exists.
			d.add(Difference{
				Kind: KindProjectMember, Disposition: DispositionCreate,
				Project: pd.Project.Slug, Name: key, Scope: pd.Project.Slug, Label: label,
			})
			continue
		}
		existing, ok := ps.Members[m.Type+":"+targetSlug]
		if !ok {
			d.add(Difference{
				Kind: KindProjectMember, Disposition: DispositionCreate,
				Project: pd.Project.Slug, Name: key, Scope: pd.Project.Slug, Label: label, TargetID: targetSlug,
			})
			continue
		}
		if m.Role.Identifier != existing.Role.Identifier {
			d.add(Difference{
				Kind: KindProjectMember, Disposition: DispositionUpdate,
				Project: pd.Project.Slug, Name: key, Scope: pd.Project.Slug, Label: label, TargetID: targetSlug,
				Changes: []FieldChange{{Field: "role", Was: existing.Role.Identifier, Now: m.Role.Identifier}},
			})
		} else {
			d.Matched[KindProjectMember]++
		}
	}
}

func (d *Diff) add(diff Difference) {
	if diff.Scope == "" {
		diff.Scope = diff.Project
	}
	d.Differences = append(d.Differences, diff)
}

func roleIdentifier(role *doppler.GroupDefaultRole) string {
	if role == nil {
		return ""
	}
	return role.Identifier
}

func samePermissions(a, b []string) bool { return sameStrings(a, b) }

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]int, len(a))
	for _, v := range a {
		seen[v]++
	}
	for _, v := range b {
		seen[v]--
		if seen[v] < 0 {
			return false
		}
	}
	return true
}

func sameMembers(a, b []doppler.GroupMember) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]int, len(a))
	for _, m := range a {
		seen[m.Type+":"+m.Slug]++
	}
	for _, m := range b {
		key := m.Type + ":" + m.Slug
		seen[key]--
		if seen[key] < 0 {
			return false
		}
	}
	return true
}

func describeCount(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func countSecrets(pd *ProjectData) int {
	total := 0
	for i := range pd.Configs {
		total += secretsGathered(pd.Configs[i])
	}
	return total
}

// projectContents counts what creating a project whole will create, leaving
// out kinds it has none of.
func projectContents(pd *ProjectData) map[ResourceKind]int {
	out := map[ResourceKind]int{}
	put := func(k ResourceKind, n int) {
		if n > 0 {
			out[k] = n
		}
	}
	put(KindEnvironment, len(pd.Environments))
	put(KindConfig, len(pd.Configs))
	put(KindSecret, countSecrets(pd))
	put(KindWebhook, len(pd.Webhooks))
	put(KindProjectMember, len(pd.Members))
	return out
}
