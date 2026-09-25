package migration

import (
	"fmt"
	"sort"
	"strings"

	"github.com/doppler/onprem-migrator/internal/doppler"
)

// Scope is what a run migrates: the whole source, or a chosen set of projects.
//
// A partial run exists to try the migration on a few projects before committing
// to the rest. Its unit is the project, taken whole, because that is where the
// dependencies stay intact: a config without its environment, or a secret
// without its config, is not a smaller test but a broken one.
//
// Around those projects a partial brings only the workplace resources their
// members point at — a group that is a member, the custom role it is given — and
// leaves the workplace layer itself (settings, change-request policies, users'
// workplace access) for the full run. Anything less would drop members on the
// target; anything more would rewrite workplace-wide state as part of a test.

// ScopeMode is full or partial.
type ScopeMode string

const (
	ScopeFull    ScopeMode = "full"
	ScopePartial ScopeMode = "partial"
)

// Scope is the choice as made on the Scope page.
type Scope struct {
	Mode ScopeMode `json:"mode"`
	// Projects are the slugs chosen for a partial run. Ignored for a full one.
	Projects []string `json:"projects,omitempty"`
}

// IsPartial reports whether this scope narrows the source at all.
func (s Scope) IsPartial() bool { return s.Mode == ScopePartial }

// IncludedResource is a workplace resource a partial brings along, and why.
type IncludedResource struct {
	Kind string `json:"kind"` // project_role, workplace_role, group, service_account
	Key  string `json:"key"`  // role identifier or slug
	Name string `json:"name"`
	// Reasons say which project pulled it in. "Why is this here" is the first
	// question someone running a cautious test asks.
	Reasons []string `json:"reasons"`
}

// AddedProject is a project the resolver added to keep an inheritance intact.
type AddedProject struct {
	Project string `json:"project"`
	Because string `json:"because"`
}

// ScopeResolution is a scope worked out against the gathered source.
type ScopeResolution struct {
	Mode ScopeMode `json:"mode"`

	// Projects is everything in scope — chosen, carried over from an earlier
	// partial, and added for inheritance — sorted.
	Projects []string       `json:"projects"`
	Chosen   []string       `json:"chosen"`
	Added    []AddedProject `json:"added,omitempty"`

	Included []IncludedResource `json:"included,omitempty"`

	// What waits for the full run.
	DeferredProjects        int  `json:"deferred_projects"`
	DeferredGroups          int  `json:"deferred_groups"`
	DeferredRoles           int  `json:"deferred_roles"`
	DeferredServiceAccounts int  `json:"deferred_service_accounts"`
	DeferredPolicies        int  `json:"deferred_policies"`
	DefersWorkplaceSettings bool `json:"defers_workplace_settings"`

	// PoliciesAffected names change-request policies covering an in-scope
	// project. They are deferred, so until the full run those projects can be
	// changed on the target without review — which is worth saying out loud.
	PoliciesAffected []string `json:"policies_affected,omitempty"`

	TotalProjects int `json:"total_projects"`
	Configs       int `json:"configs"`
	Secrets       int `json:"secrets"`
}

// ResolveScope works out everything a scope includes.
//
// carried are projects an earlier partial already migrated. They are always in
// a later partial's scope: leaving one out would not remove it from the target,
// and a run that never looks at it again could not bring it up to date.
func ResolveScope(data *MigrationData, scope Scope, carried []string) (*ScopeResolution, error) {
	// Lists start empty rather than nil, so they encode as [] — the page reads
	// their length, and null crashed the cutover page the same way.
	res := &ScopeResolution{
		Mode: scope.Mode, TotalProjects: len(data.Projects),
		Projects: []string{}, Chosen: []string{},
	}

	known := map[string]*ProjectData{}
	for i := range data.Projects {
		known[data.Projects[i].Project.Slug] = &data.Projects[i]
	}

	if !scope.IsPartial() {
		res.Mode = ScopeFull
		for slug := range known {
			res.Projects = append(res.Projects, slug)
		}
		sort.Strings(res.Projects)
		res.Chosen = res.Projects
		res.countWeight(known)
		return res, nil
	}

	inScope := map[string]bool{}
	for _, slug := range append(append([]string(nil), scope.Projects...), carried...) {
		slug = strings.TrimSpace(slug)
		if slug == "" {
			continue
		}
		if _, ok := known[slug]; !ok {
			return nil, fmt.Errorf("the source has no project called %q", slug)
		}
		inScope[slug] = true
	}
	if len(inScope) == 0 {
		return nil, fmt.Errorf("a partial migration needs at least one project")
	}
	for slug := range inScope {
		res.Chosen = append(res.Chosen, slug)
	}
	sort.Strings(res.Chosen)

	// Inheritance can cross projects. A child config migrated without its parent
	// would lose the inheritance silently, so the parent's project comes too —
	// and says so, since this is the one dependency that grows the selection by
	// a whole project rather than a workplace resource.
	for changed := true; changed; {
		changed = false
		for _, slug := range sortedSet(inScope) {
			pd := known[slug]
			for _, cd := range pd.Configs {
				for _, parent := range cd.Config.Inherits {
					if inScope[parent.Project] || known[parent.Project] == nil {
						continue
					}
					inScope[parent.Project] = true
					res.Added = append(res.Added, AddedProject{
						Project: parent.Project,
						Because: fmt.Sprintf("%s/%s inherits from %s/%s", slug, cd.Config.Name, parent.Project, parent.Config),
					})
					changed = true
				}
			}
		}
	}
	res.Projects = sortedSet(inScope)

	res.resolveReferences(data, known)
	res.countWeight(known)

	res.DeferredProjects = len(data.Projects) - len(res.Projects)
	res.DefersWorkplaceSettings = data.Workplace != nil
	res.DeferredPolicies = len(data.ChangeRequestPolicies)
	for _, p := range data.ChangeRequestPolicies {
		if policyCovers(p, inScope) {
			res.PoliciesAffected = append(res.PoliciesAffected, p.Name)
		}
	}

	return res, nil
}

// resolveReferences finds the workplace resources in-scope members point at.
func (r *ScopeResolution) resolveReferences(data *MigrationData, known map[string]*ProjectData) {
	projectRoles := map[string]doppler.Role{}
	for _, role := range data.ProjectRoles {
		projectRoles[role.Identifier] = role
	}
	workplaceRoles := map[string]doppler.Role{}
	for _, role := range data.WorkplaceRoles {
		workplaceRoles[role.Identifier] = role
	}
	groups := map[string]doppler.Group{}
	for _, g := range data.Groups {
		groups[g.Group.Slug] = g.Group
	}
	accounts := map[string]doppler.ServiceAccount{}
	for _, sa := range data.ServiceAccounts {
		accounts[sa.Slug] = sa
	}

	included := map[string]*IncludedResource{}
	include := func(kind, key, name, reason string) {
		id := kind + ":" + key
		if res, ok := included[id]; ok {
			for _, existing := range res.Reasons {
				if existing == reason {
					return
				}
			}
			res.Reasons = append(res.Reasons, reason)
			return
		}
		included[id] = &IncludedResource{Kind: kind, Key: key, Name: name, Reasons: []string{reason}}
	}

	roleName := func(identifier string) string {
		if role, ok := projectRoles[identifier]; ok {
			return role.Name
		}
		return identifier
	}

	for _, slug := range r.Projects {
		for _, m := range known[slug].Members {
			role := m.Role.Identifier
			if pr, ok := projectRoles[role]; ok {
				include("project_role", pr.Identifier, pr.Name, fmt.Sprintf("used in %s", slug))
			}
			switch m.Type {
			case "group":
				if g, ok := groups[m.Slug]; ok {
					include("group", g.Slug, g.Name, fmt.Sprintf("member of %s, with %s", slug, roleName(role)))
				}
			case "service_account":
				if sa, ok := accounts[m.Slug]; ok {
					include("service_account", sa.Slug, sa.Name, fmt.Sprintf("member of %s, with %s", slug, roleName(role)))
				}
			}
		}
	}

	// A group or service account brings the custom roles it is given, or it
	// arrives on the target's default role — a privilege change in whichever
	// direction the default happens to differ.
	for _, res := range sortedIncluded(included) {
		var granted []*doppler.GroupDefaultRole
		switch res.Kind {
		case "group":
			g := groups[res.Key]
			granted = append(granted, g.WorkplaceRole, g.DefaultRole)
		case "service_account":
			granted = append(granted, accounts[res.Key].WorkplaceRole)
		default:
			continue
		}
		for _, gr := range granted {
			if gr == nil {
				continue
			}
			if wr, ok := workplaceRoles[gr.Identifier]; ok {
				include("workplace_role", wr.Identifier, wr.Name, fmt.Sprintf("the workplace role of %s", res.Name))
			}
			if pr, ok := projectRoles[gr.Identifier]; ok {
				include("project_role", pr.Identifier, pr.Name, fmt.Sprintf("the default project role of %s", res.Name))
			}
		}
	}

	r.Included = sortedIncluded(included)

	count := func(kind string) int {
		n := 0
		for _, res := range r.Included {
			if res.Kind == kind {
				n++
			}
		}
		return n
	}
	r.DeferredGroups = len(data.Groups) - count("group")
	r.DeferredServiceAccounts = len(data.ServiceAccounts) - count("service_account")
	r.DeferredRoles = len(data.ProjectRoles) + len(data.WorkplaceRoles) - count("project_role") - count("workplace_role")
}

func (r *ScopeResolution) countWeight(known map[string]*ProjectData) {
	for _, slug := range r.Projects {
		for _, cd := range known[slug].Configs {
			r.Configs++
			r.Secrets += cd.SecretCount
		}
	}
}

// has reports whether a resource of the given kind is included.
func (r *ScopeResolution) has(kind, key string) bool {
	for _, res := range r.Included {
		if res.Kind == kind && res.Key == key {
			return true
		}
	}
	return false
}

// ApplyScope narrows gathered data to a resolved scope.
//
// It returns a shallow copy: slices are rebuilt, but the records in them are
// shared with the original, so nothing here duplicates secret values. A full
// scope returns the data unchanged.
func ApplyScope(data *MigrationData, res *ScopeResolution) *MigrationData {
	if res == nil || res.Mode != ScopePartial {
		return data
	}

	inScope := map[string]bool{}
	for _, slug := range res.Projects {
		inScope[slug] = true
	}

	out := *data
	out.DeferWorkplaceLevel = true
	out.ChangeRequestPolicies = nil

	out.Projects = nil
	for _, pd := range data.Projects {
		if inScope[pd.Project.Slug] {
			out.Projects = append(out.Projects, pd)
		}
	}

	out.Groups = nil
	for _, g := range data.Groups {
		if res.has("group", g.Group.Slug) {
			out.Groups = append(out.Groups, g)
		}
	}
	out.ServiceAccounts = nil
	for _, sa := range data.ServiceAccounts {
		if res.has("service_account", sa.Slug) {
			out.ServiceAccounts = append(out.ServiceAccounts, sa)
		}
	}
	out.ProjectRoles = nil
	for _, role := range data.ProjectRoles {
		if res.has("project_role", role.Identifier) {
			out.ProjectRoles = append(out.ProjectRoles, role)
		}
	}
	out.WorkplaceRoles = nil
	for _, role := range data.WorkplaceRoles {
		if res.has("workplace_role", role.Identifier) {
			out.WorkplaceRoles = append(out.WorkplaceRoles, role)
		}
	}

	out.ServiceTokens = nil
	for _, st := range data.ServiceTokens {
		if inScope[st.Project] {
			out.ServiceTokens = append(out.ServiceTokens, st)
		}
	}
	out.ServiceAccountTokens = nil
	for _, t := range data.ServiceAccountTokens {
		if res.has("service_account", t.ServiceAccount) {
			out.ServiceAccountTokens = append(out.ServiceAccountTokens, t)
		}
	}
	out.ServiceAccountIdentities = nil
	for _, id := range data.ServiceAccountIdentities {
		if res.has("service_account", id.ServiceAccount) {
			out.ServiceAccountIdentities = append(out.ServiceAccountIdentities, id)
		}
	}
	out.RotatedSecrets = nil
	for _, rs := range data.RotatedSecrets {
		if inScope[rs.Project] {
			out.RotatedSecrets = append(out.RotatedSecrets, rs)
		}
	}
	out.PersonalConfigs = nil
	for _, pc := range data.PersonalConfigs {
		if inScope[pc.Project] {
			out.PersonalConfigs = append(out.PersonalConfigs, pc)
		}
	}

	// An integration is workplace-level but syncs per config, so it stays in the
	// manual steps only for the syncs that touch the scope.
	out.Integrations = nil
	for _, integ := range data.Integrations {
		var syncs []doppler.Sync
		for _, s := range integ.Syncs {
			if inScope[s.Project] {
				syncs = append(syncs, s)
			}
		}
		if len(syncs) > 0 {
			integ.Syncs = syncs
			out.Integrations = append(out.Integrations, integ)
		}
	}

	return &out
}

// WithProjects returns a copy of data holding only the listed projects, or,
// with exclude set, everything but them. Workplace resources are kept either
// way; the two stages of a full run after a partial split on projects alone.
func WithProjects(data *MigrationData, projects []string, exclude bool) *MigrationData {
	set := map[string]bool{}
	for _, slug := range projects {
		set[slug] = true
	}
	out := *data
	out.Projects = nil
	for _, pd := range data.Projects {
		if set[pd.Project.Slug] != exclude {
			out.Projects = append(out.Projects, pd)
		}
	}
	return &out
}

// ScopeSnapshot narrows a target snapshot to match scoped source data.
//
// The diff treats every target project absent from the source data as an
// orphan. Handed scoped data and an unscoped snapshot, it would report every
// project outside the scope as orphaned — including, in the second stage of a
// full run, the ones the first stage had just created. So source projects that
// are out of scope are dropped from the snapshot, and projects that exist
// nowhere on the source are kept: those are genuinely orphans either way.
func ScopeSnapshot(snap *TargetSnapshot, inScope []string, source *MigrationData) *TargetSnapshot {
	if snap == nil {
		return nil
	}
	keep := map[string]bool{}
	for _, slug := range inScope {
		keep[slug] = true
	}
	onSource := map[string]bool{}
	for _, pd := range source.Projects {
		onSource[pd.Project.Slug] = true
	}

	out := *snap
	out.Projects = make(map[string]ProjectSnapshot, len(snap.Projects))
	for slug, ps := range snap.Projects {
		if keep[slug] || !onSource[slug] {
			out.Projects[slug] = ps
		}
	}
	return &out
}

func policyCovers(p doppler.ChangeRequestPolicy, inScope map[string]bool) bool {
	if p.Targets.AllProjects {
		return true
	}
	for slug := range p.Targets.Projects {
		if inScope[slug] {
			return true
		}
	}
	return false
}

func sortedIncluded(m map[string]*IncludedResource) []IncludedResource {
	out := make([]IncludedResource, 0, len(m))
	for _, res := range m {
		out = append(out, *res)
	}
	order := map[string]int{"group": 0, "service_account": 1, "project_role": 2, "workplace_role": 3}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return order[out[i].Kind] < order[out[j].Kind]
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func sortedSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ScopeKey names the set of projects a first-stage run covers, for the
// checkpoint. A full run with nothing to step around keeps the empty key, so a
// checkpoint written before scopes existed still resumes.
func ScopeKey(mode ScopeMode, projects []string, excluded []string) string {
	switch {
	case mode == ScopePartial:
		p := append([]string(nil), projects...)
		sort.Strings(p)
		return "partial:" + strings.Join(p, ",")
	case len(excluded) > 0:
		x := append([]string(nil), excluded...)
		sort.Strings(x)
		return "full-except:" + strings.Join(x, ",")
	default:
		return ""
	}
}
