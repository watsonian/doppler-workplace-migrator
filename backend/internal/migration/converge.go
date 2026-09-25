package migration

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/doppleruniversity/workplace-migrator/internal/doppler"
)

// RunMode decides what a run is allowed to do, not what it is able to.
type RunMode string

const (
	// ModeTopUp creates what is missing and applies updates. Removals are
	// deferred unless explicitly turned on for the run. This is the mode you run
	// repeatedly.
	ModeTopUp RunMode = "top_up"
	// ModeCutOver does everything a top-up does and also the removals, plus the
	// trusted-IP mirror. Run once, at the end.
	ModeCutOver RunMode = "cut_over"
)

// DriftPolicy is what a run does about a secret somebody changed on the target
// after we wrote it.
type DriftPolicy string

const (
	// DriftOverwrite treats the source as correct. The default, and the reason
	// drift is surfaced before the run rather than after it.
	DriftOverwrite DriftPolicy = "overwrite"
	// DriftKeepTarget leaves the target's value alone and records it.
	DriftKeepTarget DriftPolicy = "keep_target"
)

// ConvergeOptions is everything a run needs to know beyond the diff itself.
type ConvergeOptions struct {
	Mode RunMode
	// DeleteOrphans removes what the source no longer has. Always allowed at
	// cutover; a per-run opt-in during a top-up.
	DeleteOrphans bool
	// DeleteOrphanConfigs is separate because deleting a config takes its
	// secrets with it, which is a bigger decision than deleting a secret.
	DeleteOrphanConfigs bool
	Drift               DriftPolicy
	// MirrorTrustedIPs applies the trusted-IP mirror. Cutover only: it is the
	// step that can lock the migrator out.
	MirrorTrustedIPs bool
	// MigratorCIDR is kept in every config's trusted list while the cutover
	// runs, and removed by the final step. Empty means no self-trust.
	MigratorCIDR string
}

// Deletes reports whether this run will remove anything.
func (o ConvergeOptions) Deletes() bool {
	return o.Mode == ModeCutOver || o.DeleteOrphans || o.DeleteOrphanConfigs
}

// DefaultConvergeOptions is what a top-up does when nobody chooses otherwise:
// create and update, delete nothing, take the source on a conflict, leave
// trusted IPs alone.
func DefaultConvergeOptions() ConvergeOptions {
	return ConvergeOptions{Mode: ModeTopUp, Drift: DriftOverwrite}
}

// ConvergeResult is what a converge run did.
type ConvergeResult struct {
	Created  int `json:"created"`
	Updated  int `json:"updated"`
	Deleted  int `json:"deleted"`
	Skipped  int `json:"skipped"`
	Failed   int `json:"failed"`
	Deferred int `json:"deferred"`

	// SecretsWritten totals the secrets actually sent, which is the number
	// people compare against their own expectations.
	SecretsWritten int `json:"secrets_written"`

	// Current counts what the comparison found already matching, and so left
	// alone: "19,301 already current" is what makes a small top-up believable.
	Current int `json:"current"`

	// Changes is what was written, by resource type, for the Runs table.
	Changes *RunChanges `json:"changes,omitempty"`

	// Writes are the configs whose secrets this run wrote, and when, for the
	// run history — so a later comparison looks for changes made on the
	// target since. Not sent anywhere.
	Writes []ConfigWrite `json:"-"`

	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
}

// Converger applies a diff to the target.
//
// Separate from Executor on purpose. The executor walks the source in
// dependency order and writes everything; this walks a diff and writes only
// what differs. They share the client and the error store, not the traversal —
// folding the two together would mean every phase growing a "but only if it
// changed" branch, which is how a migration path and a convergence path end up
// silently disagreeing about what they do.
type Converger struct {
	target     DopplerReadWriter
	data       *MigrationData
	diff       *Diff
	opts       ConvergeOptions
	errors     *ErrorStore
	onProgress func(MigrateProgress)

	// driftedSecrets is the set of scopes a keep-target policy protects.
	driftedScopes map[string]struct{}

	// ids translates source identities to target ones. It starts as the
	// diff's and grows as groups and accounts are created.
	ids *crossIndex

	// newGroups are groups created in this run, whose membership is written
	// once service accounts exist; newProjects are projects created whole,
	// whose webhooks and members have no differences of their own.
	newGroups   []GroupWithMembers
	newProjects []*ProjectData

	// changes collects what the run writes, for its entry in the run history.
	changes *RunChanges
}

// NewConverger creates a converger over a computed diff.
func NewConverger(
	target DopplerReadWriter,
	data *MigrationData,
	diff *Diff,
	opts ConvergeOptions,
	errors *ErrorStore,
	onProgress func(MigrateProgress),
) *Converger {
	if onProgress == nil {
		onProgress = func(MigrateProgress) {}
	}
	if errors == nil {
		errors = NewErrorStore()
	}

	drifted := map[string]struct{}{}
	if opts.Drift == DriftKeepTarget {
		for _, record := range diff.Drift {
			drifted[record.Scope] = struct{}{}
		}
	}

	ids := diff.ids
	if ids == nil {
		ids = buildCrossIndex(data, nil)
	}

	return &Converger{
		ids:           ids,
		target:        target,
		data:          data,
		diff:          diff,
		opts:          opts,
		errors:        errors,
		onProgress:    onProgress,
		driftedScopes: drifted,
	}
}

// Errors exposes the store, so triage works the same way it does for a run.
func (c *Converger) Errors() *ErrorStore { return c.errors }

// Run applies the diff.
func (c *Converger) Run(ctx context.Context) (*ConvergeResult, error) {
	result := &ConvergeResult{StartedAt: time.Now().UTC(), Current: c.diff.Counts().Match, Changes: &RunChanges{Groups: []ChangeGroup{}}}
	c.changes = result.Changes
	defer func() { result.FinishedAt = time.Now().UTC() }()

	// Order matters as much here as in a full migration: a secret cannot be
	// written to a config that does not exist yet, and a member cannot be given
	// a role that has not been created.
	stages := []struct {
		name string
		fn   func(context.Context, *ConvergeResult) error
	}{
		{"roles", c.convergeRoles},
		{"groups", c.convergeGroups},
		{"service_accounts", c.convergeServiceAccounts},
		// After accounts: a group can have one as a member.
		{"group_members", c.convergeGroupMembers},
		{"environments", c.convergeEnvironments},
		{"configs", c.convergeConfigs},
		{"secrets", c.convergeSecrets},
		{"webhooks", c.convergeWebhooks},
		{"members", c.convergeMembers},
		{"deletions", c.convergeDeletions},
	}

	for _, stage := range stages {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if err := stage.fn(ctx, result); err != nil {
			return result, fmt.Errorf("%s: %w", stage.name, err)
		}
	}

	return result, nil
}

func (c *Converger) convergeRoles(ctx context.Context, result *ConvergeResult) error {
	for _, diff := range c.forKinds(KindWorkplaceRole, KindProjectRole) {
		role, ok := c.findRole(diff)
		if !ok {
			continue
		}

		var err error
		if diff.Kind == KindWorkplaceRole {
			if diff.Disposition == DispositionCreate {
				_, err = c.target.CreateWorkplaceRole(ctx, role.Name, role.Permissions)
			} else {
				_, err = c.target.UpdateWorkplaceRole(ctx, role.Identifier, role.Name, role.Permissions)
			}
		} else {
			if diff.Disposition == DispositionCreate {
				_, err = c.target.CreateProjectRole(ctx, role.Name, role.Permissions)
			} else {
				_, err = c.target.UpdateProjectRole(ctx, role.Identifier, role.Name, role.Permissions)
			}
		}
		c.record(diff, "roles", err, result, 0)
	}
	return nil
}

func (c *Converger) convergeGroups(ctx context.Context, result *ConvergeResult) error {
	for _, diff := range c.forKinds(KindGroup) {
		gwm, ok := c.findGroup(diff.Name)
		if !ok {
			continue
		}

		var err error
		group := gwm.Group
		if diff.Disposition == DispositionCreate {
			var created *doppler.Group
			if created, err = c.target.CreateGroup(ctx, &group); err == nil && created != nil {
				c.ids.groups[gwm.Group.Slug] = created.Slug
				// A new group has no members difference to act on, so its
				// membership is written by the group members stage.
				c.newGroups = append(c.newGroups, gwm)
			}
		} else {
			_, err = c.target.UpdateGroup(ctx, diff.TargetID, &group)
		}
		c.record(diff, "groups", err, result, 0)
	}
	return nil
}

// convergeGroupMembers replaces each group's membership. Replace, not
// reconcile: the server works out the difference.
func (c *Converger) convergeGroupMembers(ctx context.Context, result *ConvergeResult) error {
	write := func(diff Difference, gwm GroupWithMembers) {
		slug, ok := c.ids.groups[gwm.Group.Slug]
		if !ok {
			c.fail(diff, "groups", ClassSubjectNotFound, fmt.Sprintf("group %q is not on the target", gwm.Group.Name))
			result.Failed++
			return
		}
		members, _ := c.ids.translateMembers(gwm.Members)
		c.record(diff, "groups", c.target.ReplaceGroupMembers(ctx, slug, members), result, 0)
	}

	for _, diff := range c.forKinds(KindGroupMembers) {
		if gwm, ok := c.findGroup(diff.Name); ok {
			write(diff, gwm)
		}
	}
	for _, gwm := range c.newGroups {
		if len(gwm.Members) == 0 {
			continue
		}
		write(Difference{Kind: KindGroupMembers, Disposition: DispositionCreate, Name: gwm.Group.Name}, gwm)
	}
	return nil
}

func (c *Converger) convergeServiceAccounts(ctx context.Context, result *ConvergeResult) error {
	for _, diff := range c.forKinds(KindServiceAcct) {
		account, ok := c.findServiceAccount(diff.Name)
		if !ok {
			continue
		}
		var err error
		if diff.Disposition == DispositionCreate {
			var created *doppler.ServiceAccount
			if created, err = c.target.CreateServiceAccount(ctx, &account); err == nil && created != nil {
				c.ids.accounts[account.Slug] = created.Slug
			}
		} else {
			_, err = c.target.UpdateServiceAccount(ctx, diff.TargetID, &account)
		}
		c.record(diff, "service_accounts", err, result, 0)
	}
	return nil
}

func (c *Converger) convergeEnvironments(ctx context.Context, result *ConvergeResult) error {
	// A new project brings its environments and configs with it, so those are
	// created before anything beneath them is compared.
	for _, diff := range c.forKinds(KindProject) {
		if diff.Disposition != DispositionCreate {
			continue
		}
		pd, ok := c.findProject(diff.Project)
		if !ok {
			continue
		}
		err := c.createWholeProject(ctx, pd, result)
		c.record(diff, "projects", err, result, 0)
	}

	for _, diff := range c.forKinds(KindEnvironment) {
		pd, ok := c.findProject(diff.Project)
		if !ok {
			continue
		}
		env, ok := findEnvironment(pd, diff.Name)
		if !ok {
			continue
		}
		var err error
		if diff.Disposition == DispositionCreate {
			_, err = c.target.CreateEnvironment(ctx, pd.Project.Slug, env.Name, env.Slug, env.PersonalConfigs)
		} else {
			_, err = c.target.UpdateEnvironment(ctx, pd.Project.Slug, env.Slug, env.Name)
		}
		c.record(diff, "environments", err, result, 0)
	}
	return nil
}

// createWholeProject builds a project that is not on the target at all, along
// with its environments and configs, so the stages after this one have
// somewhere to write.
func (c *Converger) createWholeProject(ctx context.Context, pd *ProjectData, result *ConvergeResult) error {
	if _, err := c.target.CreateProject(ctx, pd.Project.Name, pd.Project.Description); err != nil && !isAlreadyExistsError(err) {
		return err
	}
	for _, env := range pd.Environments {
		if _, err := c.target.CreateEnvironment(ctx, pd.Project.Slug, env.Name, env.Slug, env.PersonalConfigs); err != nil && !isAlreadyExistsError(err) {
			log.Printf("[warn] converge: create environment %s/%s: %v", pd.Project.Slug, env.Slug, err)
		}
	}
	for i := range pd.Configs {
		cd := &pd.Configs[i]
		if cd.Config.Root {
			continue
		}
		if _, err := c.target.CreateConfig(ctx, pd.Project.Slug, cd.Config.Environment, cd.Config.Name); err != nil && !isAlreadyExistsError(err) {
			log.Printf("[warn] converge: create config %s/%s: %v", pd.Project.Slug, cd.Config.Name, err)
		}
	}
	// Its webhooks and members have no differences of their own; the stages
	// for those write them from here.
	c.newProjects = append(c.newProjects, pd)
	_ = result
	return nil
}

func (c *Converger) convergeConfigs(ctx context.Context, result *ConvergeResult) error {
	for _, diff := range c.forKinds(KindConfig) {
		if diff.Disposition != DispositionCreate {
			continue
		}
		pd, ok := c.findProject(diff.Project)
		if !ok {
			continue
		}
		cd, ok := findConfig(pd, diff.Config)
		if !ok || cd.Config.Root {
			continue
		}
		_, err := c.target.CreateConfig(ctx, pd.Project.Slug, cd.Config.Environment, cd.Config.Name)
		if err != nil && isAlreadyExistsError(err) {
			err = nil
		}
		c.record(diff, "configs", err, result, 0)
	}
	return nil
}

func (c *Converger) convergeSecrets(ctx context.Context, result *ConvergeResult) error {
	// Grouped per config: the write endpoint takes a batch, so a config with
	// forty changed secrets is one request rather than forty.
	byConfig := map[string][]Difference{}
	for _, diff := range c.forKinds(KindSecret) {
		if diff.Disposition == DispositionOrphan {
			continue // handled by the deletions stage
		}
		byConfig[diff.Project+"/"+diff.Config] = append(byConfig[diff.Project+"/"+diff.Config], diff)
	}

	for scope, diffs := range byConfig {
		if err := ctx.Err(); err != nil {
			return err
		}

		// A config somebody else changed, under a keep-target policy, is left
		// exactly as it is — including its unrelated secrets, because the policy
		// is about not fighting whoever owns that config.
		if _, protected := c.driftedScopes[scope]; protected {
			result.Skipped += len(diffs)
			for _, diff := range diffs {
				c.emit(diff, "secrets", "skipped", "kept the target's version")
			}
			continue
		}

		project, config, _ := strings.Cut(scope, "/")
		pd, ok := c.findProject(project)
		if !ok {
			continue
		}
		cd, ok := findConfig(pd, config)
		if !ok {
			continue
		}

		changes := make([]doppler.SecretChangeRequest, 0, len(diffs))
		for _, diff := range diffs {
			secret, ok := cd.Secrets[diff.Name]
			if !ok {
				// The value was wiped by an earlier attempt. Writing nothing and
				// reporting success would be silent data loss.
				c.fail(diff, "secrets", ClassStaleGather,
					"secret values were cleared after an earlier attempt; re-run Compare before retrying")
				result.Failed++
				continue
			}
			value := secret.Raw
			change := doppler.SecretChangeRequest{
				Name:         diff.Name,
				OriginalName: nil,
				Value:        &value,
			}
			if diff.Disposition == DispositionUpdate {
				name := diff.Name
				change.OriginalName = &name
			}
			if secret.RawVisibility != "" {
				visibility := secret.RawVisibility
				change.Visibility = &visibility
			}
			if secret.RawValueType != nil && secret.RawValueType.Type != "" {
				valueType := *secret.RawValueType
				change.ValueType = &valueType
			}
			changes = append(changes, change)
		}

		if len(changes) == 0 {
			continue
		}

		// Taken before the write: slightly early means a change of ours could
		// read as someone else's, which is visible, where late would hide one.
		at := time.Now().UTC()
		err := c.target.UpdateSecretsWithMetadata(ctx, project, config, changes)
		for _, diff := range diffs {
			c.record(diff, "secrets", err, result, 1)
		}
		if err == nil {
			result.SecretsWritten += len(changes)
			result.Writes = append(result.Writes, ConfigWrite{Project: project, Config: config, WrittenAt: at})
		}
	}

	// A project created whole in this run has no secret differences — the
	// diff counts it as one create — so its secrets are written from here.
	// Without this it arrived with every config empty.
	for _, pd := range c.newProjects {
		for i := range pd.Configs {
			if err := ctx.Err(); err != nil {
				return err
			}
			c.writeAllSecrets(ctx, pd, &pd.Configs[i], result)
		}
	}
	return nil
}

// writeAllSecrets writes every secret of a config the target did not have.
func (c *Converger) writeAllSecrets(ctx context.Context, pd *ProjectData, cd *ConfigData, result *ConvergeResult) {
	scope := pd.Project.Slug + "/" + cd.Config.Name
	if len(cd.Secrets) == 0 {
		if cd.SecretCount > 0 {
			// The values were wiped by an earlier write in this session and not
			// reloaded. Writing nothing and reporting success would be data loss.
			c.fail(Difference{Kind: KindConfig, Project: pd.Project.Slug, Config: cd.Config.Name, Name: cd.Config.Name, Scope: scope},
				"secrets", ClassStaleGather, "secret values were cleared after an earlier attempt; re-run Compare before retrying")
			result.Failed++
		}
		return
	}

	names := make([]string, 0, len(cd.Secrets))
	for name := range cd.Secrets {
		names = append(names, name)
	}
	sort.Strings(names)

	changes := make([]doppler.SecretChangeRequest, 0, len(names))
	for _, name := range names {
		secret := cd.Secrets[name]
		value := secret.Raw
		change := doppler.SecretChangeRequest{Name: name, Value: &value}
		if secret.RawVisibility != "" {
			visibility := secret.RawVisibility
			change.Visibility = &visibility
		}
		if secret.RawValueType != nil && secret.RawValueType.Type != "" {
			valueType := *secret.RawValueType
			change.ValueType = &valueType
		}
		changes = append(changes, change)
	}

	at := time.Now().UTC()
	err := c.target.UpdateSecretsWithMetadata(ctx, pd.Project.Slug, cd.Config.Name, changes)
	if err == nil {
		result.Writes = append(result.Writes, ConfigWrite{Project: pd.Project.Slug, Config: cd.Config.Name, WrittenAt: at})
	}
	for _, name := range names {
		c.record(Difference{
			Kind: KindSecret, Disposition: DispositionCreate,
			Project: pd.Project.Slug, Config: cd.Config.Name, Name: name, Scope: scope, Weight: 1,
		}, "secrets", err, result, 1)
	}
	if err == nil {
		result.SecretsWritten += len(changes)
	}
}

func (c *Converger) convergeWebhooks(ctx context.Context, result *ConvergeResult) error {
	for _, diff := range c.forKinds(KindWebhook) {
		pd, ok := c.findProject(diff.Project)
		if !ok {
			continue
		}
		wh, ok := findWebhook(pd, diff.Name)
		if !ok {
			continue
		}
		var err error
		hook := wh
		if diff.Disposition == DispositionCreate {
			_, err = c.target.CreateWebhook(ctx, pd.Project.Slug, &hook)
		} else {
			// The target's own ID: the source's addresses nothing there.
			_, err = c.target.UpdateWebhook(ctx, pd.Project.Slug, diff.TargetID, &hook)
		}
		c.record(diff, "webhooks", err, result, 0)
	}
	for _, pd := range c.newProjects {
		for _, wh := range pd.Webhooks {
			hook := wh
			diff := Difference{Kind: KindWebhook, Disposition: DispositionCreate, Project: pd.Project.Slug, Name: wh.URL, Scope: pd.Project.Slug}
			_, err := c.target.CreateWebhook(ctx, pd.Project.Slug, &hook)
			c.record(diff, "webhooks", err, result, 0)
		}
	}
	return nil
}

func (c *Converger) convergeMembers(ctx context.Context, result *ConvergeResult) error {
	add := func(diff Difference, pd *ProjectData, member doppler.ProjectMember) {
		// Resolved now rather than taken from the diff alone: a group or
		// account created earlier in this run had no target slug when the
		// diff was made.
		slug := diff.TargetID
		if slug == "" {
			var ok bool
			if slug, ok = c.ids.member(member.Type, member.Slug); !ok {
				c.fail(diff, "members", ClassSubjectNotFound, fmt.Sprintf("%s is not on the target", c.ids.label(member.Type, member.Slug)))
				result.Failed++
				return
			}
		}
		var err error
		if diff.Disposition == DispositionUpdate {
			err = c.target.UpdateProjectMember(ctx, pd.Project.Slug, member.Type, slug, member.Role.Identifier, member.Environments)
		} else {
			err = c.target.AddProjectMember(ctx, pd.Project.Slug, member.Type, slug, member.Role.Identifier, member.Environments)
		}
		// A member with admin access to every project — a workplace owner or
		// admin, or an account with such a role — already has this one, and the
		// target refuses a project role on top. Nothing is missing, so it is a
		// skip, as it is in a first migration. It shows up whenever a project is
		// created here, because the source lists such members like any other.
		if isAdminAccessError(err) {
			result.Skipped++
			c.emit(diff, "members", "skipped", "has admin access to all projects")
			return
		}
		c.record(diff, "members", err, result, 0)
	}

	for _, diff := range c.forKinds(KindProjectMember) {
		pd, ok := c.findProject(diff.Project)
		if !ok {
			continue
		}
		memberType, memberSlug, _ := strings.Cut(diff.Name, ":")
		member, ok := findMember(pd, memberType, memberSlug)
		if !ok {
			continue
		}
		add(diff, pd, member)
	}
	for _, pd := range c.newProjects {
		for _, m := range pd.Members {
			if _, ok := c.ids.member(m.Type, m.Slug); !ok && m.Type == "workplace_user" {
				continue // not provisioned on the target; the user check says so
			}
			add(Difference{
				Kind: KindProjectMember, Disposition: DispositionCreate, Project: pd.Project.Slug,
				Name: m.Type + ":" + m.Slug, Scope: pd.Project.Slug, Label: c.ids.label(m.Type, m.Slug),
			}, pd, m)
		}
	}
	return nil
}

// convergeDeletions removes what the source no longer has, when the run is
// allowed to. Otherwise it counts them as deferred so the summary can say so.
func (c *Converger) convergeDeletions(ctx context.Context, result *ConvergeResult) error {
	orphans := c.diff.Orphans()
	if len(orphans) == 0 {
		return nil
	}

	deleteSecrets := c.opts.Mode == ModeCutOver || c.opts.DeleteOrphans
	deleteConfigs := c.opts.Mode == ModeCutOver || c.opts.DeleteOrphanConfigs

	// Batched per config for the same reason writes are.
	secretsByConfig := map[string][]Difference{}
	for _, diff := range orphans {
		switch diff.Kind {
		case KindSecret:
			if !deleteSecrets {
				result.Deferred++
				continue
			}
			secretsByConfig[diff.Project+"/"+diff.Config] = append(secretsByConfig[diff.Project+"/"+diff.Config], diff)
		case KindConfig:
			if !deleteConfigs {
				result.Deferred++
				continue
			}
			err := c.target.DeleteConfig(ctx, diff.Project, diff.Config)
			c.recordDelete(diff, "configs", err, result)
		default:
			// Projects and everything else are never deleted by this tool. The
			// blast radius is too large to be a checkbox, and the summary says
			// they were left.
			result.Deferred++
		}
	}

	for scope, diffs := range secretsByConfig {
		if err := ctx.Err(); err != nil {
			return err
		}
		project, config, _ := strings.Cut(scope, "/")
		names := make([]string, 0, len(diffs))
		for _, diff := range diffs {
			names = append(names, diff.Name)
		}
		err := c.target.DeleteSecrets(ctx, project, config, names)
		for _, diff := range diffs {
			c.recordDelete(diff, "secrets", err, result)
		}
	}
	return nil
}

// --- bookkeeping ---

func (c *Converger) forKinds(kinds ...ResourceKind) []Difference {
	want := map[ResourceKind]struct{}{}
	for _, k := range kinds {
		want[k] = struct{}{}
	}
	out := []Difference{}
	for _, diff := range c.diff.Differences {
		if _, ok := want[diff.Kind]; !ok {
			continue
		}
		if diff.Disposition == DispositionOrphan && diff.Kind != KindConfig {
			continue
		}
		if diff.Disposition == DispositionOrphan {
			continue
		}
		out = append(out, diff)
	}
	return out
}

func (c *Converger) record(diff Difference, resource string, err error, result *ConvergeResult, weight int) {
	if err != nil {
		if isAlreadyExistsError(err) && diff.Disposition == DispositionCreate {
			result.Skipped++
			c.emit(diff, resource, "skipped", "already exists")
			return
		}
		c.failErr(diff, resource, err)
		result.Failed++
		return
	}
	if diff.Disposition == DispositionCreate {
		result.Created++
		c.change(resource, "created", diff)
	} else {
		result.Updated++
		c.change(resource, "updated", diff)
	}
	_ = weight
	c.emit(diff, resource, "completed", "")
}

// change notes one write for the run history.
func (c *Converger) change(resource, action string, diff Difference) {
	if c.changes == nil {
		return
	}
	label := convergeResourceLabels[resource]
	if label == "" {
		label = resourceLabel(resource)
	}
	item := itemName(diff)
	if diff.Scope != "" && diff.Kind != KindProject && diff.Kind != KindGroup && diff.Kind != KindGroupMembers && diff.Kind != KindServiceAcct {
		item = diff.Scope + " · " + item
	}
	c.changes.Add(label, action, item)
}

func (c *Converger) recordDelete(diff Difference, resource string, err error, result *ConvergeResult) {
	if err != nil {
		c.failErr(diff, resource, err)
		result.Failed++
		return
	}
	result.Deleted++
	c.change(resource, "deleted", diff)
	c.emit(diff, resource, "completed", "deleted")
}

func (c *Converger) emit(diff Difference, resource, status, note string) {
	item := diff.Name
	if diff.Scope != "" && (diff.Kind == KindSecret || diff.Label != "") {
		item = diff.Scope + " · " + itemName(diff)
	} else if diff.Scope != "" {
		item = diff.Scope
	}
	c.onProgress(MigrateProgress{
		Name:     "converge",
		Resource: resource,
		Item:     item,
		Status:   status,
		Error:    note,
	})
}

func (c *Converger) failErr(diff Difference, resource string, err error) {
	c.errors.Record(RecordInput{
		Err:           err,
		Side:          SideTarget,
		PhaseName:     "converge",
		Resource:      resource,
		ResourceLabel: resourceLabel(resource),
		Item:          diff.Scope + " · " + itemName(diff),
		Project:       diff.Project,
		Config:        diff.Config,
		Weight:        diff.Weight,
	})
	c.change(resource, "failed", diff)
	c.emit(diff, resource, "failed", err.Error())
}

func (c *Converger) fail(diff Difference, resource string, class ErrorClassKey, message string) {
	c.errors.Record(RecordInput{
		Side:          SideTarget,
		PhaseName:     "converge",
		Resource:      resource,
		ResourceLabel: resourceLabel(resource),
		Item:          diff.Scope + " · " + itemName(diff),
		Project:       diff.Project,
		Config:        diff.Config,
		Class:         class,
		Message:       message,
	})
	c.change(resource, "failed", diff)
	c.emit(diff, resource, "failed", message)
}

// --- lookups back into the gathered source ---

func (c *Converger) findRole(diff Difference) (doppler.Role, bool) {
	roles := c.data.WorkplaceRoles
	if diff.Kind == KindProjectRole {
		roles = c.data.ProjectRoles
	}
	for _, role := range roles {
		if role.Name == diff.Name {
			return role, true
		}
	}
	return doppler.Role{}, false
}

func (c *Converger) findGroup(name string) (GroupWithMembers, bool) {
	for _, gwm := range c.data.Groups {
		if gwm.Group.Name == name {
			return gwm, true
		}
	}
	return GroupWithMembers{}, false
}

func (c *Converger) findServiceAccount(name string) (doppler.ServiceAccount, bool) {
	for _, account := range c.data.ServiceAccounts {
		if account.Name == name {
			return account, true
		}
	}
	return doppler.ServiceAccount{}, false
}

func (c *Converger) findProject(slug string) (*ProjectData, bool) {
	for i := range c.data.Projects {
		if c.data.Projects[i].Project.Slug == slug {
			return &c.data.Projects[i], true
		}
	}
	return nil, false
}

func findConfig(pd *ProjectData, name string) (*ConfigData, bool) {
	for i := range pd.Configs {
		if pd.Configs[i].Config.Name == name {
			return &pd.Configs[i], true
		}
	}
	return nil, false
}

func findEnvironment(pd *ProjectData, slug string) (doppler.Environment, bool) {
	for _, env := range pd.Environments {
		if env.Slug == slug {
			return env, true
		}
	}
	return doppler.Environment{}, false
}

func findWebhook(pd *ProjectData, url string) (doppler.Webhook, bool) {
	for _, wh := range pd.Webhooks {
		if wh.URL == url {
			return wh, true
		}
	}
	return doppler.Webhook{}, false
}

func findMember(pd *ProjectData, memberType, slug string) (doppler.ProjectMember, bool) {
	for _, m := range pd.Members {
		if m.Type == memberType && m.Slug == slug {
			return m, true
		}
	}
	return doppler.ProjectMember{}, false
}

// itemName is how a difference's item reads: its label where the name is an
// identifier nobody would recognise.
func itemName(diff Difference) string {
	if diff.Label != "" {
		return diff.Label
	}
	return diff.Name
}
