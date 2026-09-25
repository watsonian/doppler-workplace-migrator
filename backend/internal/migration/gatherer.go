package migration

import (
	"context"
	"fmt"
	"sync"

	"github.com/doppler/onprem-migrator/internal/doppler"
)

// Gatherer reads all migratable resources from a source Doppler instance.
type Gatherer struct {
	source     DopplerClient
	onProgress func(GatherProgress)

	// unavailable collects what the source token was refused, from the
	// concurrent project and config reads as well as the workplace ones.
	unavailableMu sync.Mutex
	unavailable   map[string]*UnavailableResource
}

// NewGatherer creates a new Gatherer with the given source client and progress callback.
func NewGatherer(source DopplerClient, onProgress func(GatherProgress)) *Gatherer {
	if onProgress == nil {
		onProgress = func(GatherProgress) {}
	}
	return &Gatherer{
		source:     source,
		onProgress: onProgress,
	}
}

// GatherAll reads all migratable resources from the source instance.
func (g *Gatherer) GatherAll(ctx context.Context) (*MigrationData, error) {
	data := &MigrationData{}

	// 1. Workplace info
	g.onProgress(GatherProgress{Resource: "workplace", Message: "Fetching workplace info..."})
	wp, err := g.source.GetWorkplace(ctx)
	if err != nil {
		return nil, fmt.Errorf("gather workplace: %w", err)
	}
	data.Workplace = wp
	g.onProgress(GatherProgress{Resource: "workplace", Current: 1, Total: 1, Message: "Workplace info gathered"})

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// 2. Workplace users
	g.onProgress(GatherProgress{Resource: "users", Message: "Fetching workplace users..."})
	users, err := g.source.ListWorkplaceUsers(ctx)
	if err != nil {
		return nil, fmt.Errorf("gather workplace users: %w", err)
	}
	data.WorkplaceUsers = users
	g.onProgress(GatherProgress{Resource: "users", Current: len(users), Total: len(users), Message: fmt.Sprintf("%d users found", len(users))})

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// 3. Workplace roles (filter to custom only)
	g.onProgress(GatherProgress{Resource: "workplace_roles", Message: "Fetching workplace roles..."})
	allWpRoles, err := g.source.ListWorkplaceRoles(ctx)
	if err != nil {
		return nil, fmt.Errorf("gather workplace roles: %w", err)
	}
	data.WorkplaceRoles = filterCustomRoles(allWpRoles)
	g.onProgress(GatherProgress{
		Resource: "workplace_roles",
		Current:  len(data.WorkplaceRoles),
		Total:    len(data.WorkplaceRoles),
		Message:  fmt.Sprintf("%d custom workplace roles found", len(data.WorkplaceRoles)),
	})

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// 4. Project roles (filter to custom only)
	g.onProgress(GatherProgress{Resource: "project_roles", Message: "Fetching project roles..."})
	allProjRoles, err := g.source.ListProjectRoles(ctx)
	if err != nil {
		return nil, fmt.Errorf("gather project roles: %w", err)
	}
	data.ProjectRoles = filterCustomRoles(allProjRoles)
	g.onProgress(GatherProgress{
		Resource: "project_roles",
		Current:  len(data.ProjectRoles),
		Total:    len(data.ProjectRoles),
		Message:  fmt.Sprintf("%d custom project roles found", len(data.ProjectRoles)),
	})

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// 5. Groups with members
	g.onProgress(GatherProgress{Resource: "groups", Message: "Fetching groups..."})
	groups, err := g.source.ListGroups(ctx)
	if err != nil && !g.refused(ResourceGroups, "", err) {
		return nil, fmt.Errorf("gather groups: %w", err)
	}
	data.Groups = make([]GroupWithMembers, 0, len(groups))
	for i, grp := range groups {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		members, err := g.source.GetGroupMembers(ctx, grp.Slug)
		if err != nil {
			return nil, fmt.Errorf("gather group members for %q: %w", grp.Name, err)
		}
		data.Groups = append(data.Groups, GroupWithMembers{Group: grp, Members: members})
		g.onProgress(GatherProgress{
			Resource: "groups",
			Current:  i + 1,
			Total:    len(groups),
			Message:  fmt.Sprintf("Group %q (%d members)", grp.Name, len(members)),
		})
	}
	g.onProgress(GatherProgress{
		Resource: "groups",
		Current:  len(groups),
		Total:    len(groups),
		Message:  fmt.Sprintf("%d groups found", len(groups)),
	})

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// 6. Service accounts
	g.onProgress(GatherProgress{Resource: "service_accounts", Message: "Fetching service accounts..."})
	serviceAccounts, err := g.source.ListServiceAccounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("gather service accounts: %w", err)
	}
	data.ServiceAccounts = serviceAccounts
	g.onProgress(GatherProgress{
		Resource: "service_accounts",
		Current:  len(serviceAccounts),
		Total:    len(serviceAccounts),
		Message:  fmt.Sprintf("%d service accounts found", len(serviceAccounts)),
	})

	// Tokens (report-only) and identities (migrated) hang off each service account.
	for i, sa := range serviceAccounts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		tokens, err := g.source.ListServiceAccountTokens(ctx, sa.Slug)
		if err != nil && !g.refused(ResourceServiceAccountTokens, sa.Name, err) {
			return nil, fmt.Errorf("gather tokens for service account %q: %w", sa.Name, err)
		}
		data.ServiceAccountTokens = append(data.ServiceAccountTokens, tokens...)

		identities, err := g.source.ListServiceAccountIdentities(ctx, sa.Slug)
		if err != nil && !g.refused(ResourceServiceAccountIdentities, sa.Name, err) {
			return nil, fmt.Errorf("gather identities for service account %q: %w", sa.Name, err)
		}
		data.ServiceAccountIdentities = append(data.ServiceAccountIdentities, identities...)

		g.onProgress(GatherProgress{
			Resource: "service_accounts",
			Current:  i + 1,
			Total:    len(serviceAccounts),
			Message:  fmt.Sprintf("Service account %q (%d tokens, %d identities)", sa.Name, len(tokens), len(identities)),
		})
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// 7. Integrations (for manual steps report)
	g.onProgress(GatherProgress{Resource: "integrations", Message: "Fetching integrations..."})
	integrations, err := g.source.ListIntegrations(ctx)
	if err != nil && !g.refused(ResourceIntegrations, "", err) {
		return nil, fmt.Errorf("gather integrations: %w", err)
	}
	data.Integrations = integrations
	g.onProgress(GatherProgress{
		Resource: "integrations",
		Current:  len(integrations),
		Total:    len(integrations),
		Message:  fmt.Sprintf("%d integrations found", len(integrations)),
	})

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// 8. Change request policies (workplace-scoped)
	g.onProgress(GatherProgress{Resource: "change_request_policies", Message: "Fetching change request policies..."})
	policies, err := g.source.ListChangeRequestPolicies(ctx)
	if err != nil && !g.refused(ResourceChangeRequestPolicies, "", err) {
		return nil, fmt.Errorf("gather change request policies: %w", err)
	}
	data.ChangeRequestPolicies = policies
	g.onProgress(GatherProgress{
		Resource: "change_request_policies",
		Current:  len(policies),
		Total:    len(policies),
		Message:  fmt.Sprintf("%d change request policies found", len(policies)),
	})

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// 9. Projects (paginated) with sub-resources
	g.onProgress(GatherProgress{Resource: "projects", Message: "Fetching projects..."})
	projects, err := g.source.ListProjects(ctx)
	if err != nil {
		return nil, fmt.Errorf("gather projects: %w", err)
	}
	g.onProgress(GatherProgress{
		Resource: "projects",
		Current:  0,
		Total:    len(projects),
		Message:  fmt.Sprintf("%d projects found", len(projects)),
	})

	gathered, err := g.gatherProjects(ctx, projects)
	if err != nil {
		return nil, err
	}
	data.Projects = gathered.projects
	data.ServiceTokens = gathered.serviceTokens
	data.RotatedSecrets = append(data.RotatedSecrets, gathered.rotatedSecrets...)
	data.PersonalConfigs = append(data.PersonalConfigs, gathered.personalConfigs...)

	// Everything above reports as it goes, but the per-project resources are
	// gathered inside the project loop and never get a line of their own. Emit a
	// final count for each so the UI can show every resource type it migrates —
	// including the ones that came back empty, which is information too: "no
	// change request policies" is a different statement from silence.
	g.reportTotals(data)

	data.Unavailable = g.unavailableList()
	return data, nil
}

// reportTotals emits a closing progress event per resource type, so the UI has a
// complete picture rather than only the resources that happened to be non-empty.
func (g *Gatherer) reportTotals(data *MigrationData) {
	envCount, configCount, secretCount, ipCount, webhookCount, memberCount := 0, 0, 0, 0, 0, 0
	inheritableCount, inheritsCount := 0, 0
	for _, p := range data.Projects {
		envCount += len(p.Environments)
		configCount += len(p.Configs)
		webhookCount += len(p.Webhooks)
		memberCount += len(p.Members)
		for _, c := range p.Configs {
			secretCount += c.SecretCount
			ipCount += len(c.TrustedIPs)
			if c.Config.Inheritable {
				inheritableCount++
			}
			if len(c.Config.Inherits) > 0 {
				inheritsCount++
			}
		}
	}

	totals := []struct {
		resource string
		count    int
		noun     string
	}{
		{"environments", envCount, "environments"},
		{"configs", configCount, "configs"},
		{"secrets", secretCount, "secrets"},
		{"config_inheritance", inheritableCount + inheritsCount, "inheritance relationships"},
		{"trusted_ips", ipCount, "trusted IPs"},
		{"webhooks", webhookCount, "webhooks"},
		{"project_members", memberCount, "project members"},
		{"service_account_tokens", len(data.ServiceAccountTokens), "service account tokens"},
		{"service_account_identities", len(data.ServiceAccountIdentities), "service account identities"},
		{"service_tokens", len(data.ServiceTokens), "service tokens"},
		{"rotated_secrets", len(data.RotatedSecrets), "rotated secrets"},
		{"personal_configs", len(data.PersonalConfigs), "personal configs (skipped)"},
	}

	for _, t := range totals {
		g.onProgress(GatherProgress{
			Resource: t.resource,
			Current:  t.count,
			Total:    t.count,
			Message:  fmt.Sprintf("%d %s found", t.count, t.noun),
		})
	}
}

// gatherProject gathers all sub-resources for a single project.
// gatherConcurrency bounds how many requests the project stage has in flight.
//
// The stage is almost all waiting: four calls per project and four more per
// config, each a round trip. One at a time, a workplace with 640 configs is
// nearly three thousand requests end to end. Six at once cuts that wall time
// several-fold while staying polite to the source, and the rate limiter still
// governs pace — this only stops the stage idling between responses.
const gatherConcurrency = 6

// gatheredProjects is the project stage's output, in source order.
type gatheredProjects struct {
	projects        []ProjectData
	serviceTokens   []doppler.ServiceToken
	rotatedSecrets  []doppler.RotatedSecret
	personalConfigs []doppler.ConfigDescriptor
}

// projectShell is a project's own resources, read before its configs are.
type projectShell struct {
	pd       ProjectData
	configs  []doppler.Config // migratable ones, in source order
	personal []doppler.ConfigDescriptor
}

// configResult is everything read for one config.
type configResult struct {
	cd      ConfigData
	tokens  []doppler.ServiceToken
	rotated []doppler.RotatedSecret
}

// gatherProjects reads every project and config, concurrently.
//
// Two passes over one bounded pool: each project's structure first
// (environments, configs, webhooks, members), then every config across every
// project. Pooling configs rather than projects matters, because projects vary
// enormously — one with two hundred configs would otherwise hold a single
// worker for most of the run while the rest sat idle.
//
// The result is assembled in source order, so it is identical to what a
// sequential gather produces. The first error stops the stage, as before.
func (g *Gatherer) gatherProjects(ctx context.Context, projects []doppler.Project) (*gatheredProjects, error) {
	shells := make([]projectShell, len(projects))

	// Progress arrives from several workers. Counting and emitting happen under
	// one lock, together: counted under the lock but emitted after it, two
	// workers can swap order on the way out and the UI's counter runs backwards.
	// The lock also means the callback never has to be safe for concurrent use.
	var progressMu sync.Mutex
	projectsDone := 0
	finishProjectLocked := func() {
		projectsDone++
		g.onProgress(GatherProgress{
			Resource: "projects",
			Current:  projectsDone,
			Total:    len(projects),
			Message:  fmt.Sprintf("%d of %d projects", projectsDone, len(projects)),
		})
	}

	err := runPool(ctx, len(projects), gatherConcurrency, func(ctx context.Context, i int) error {
		shell, err := g.gatherProjectShell(ctx, projects[i])
		if err != nil {
			return err
		}
		shells[i] = *shell
		if len(shell.configs) == 0 {
			progressMu.Lock()
			finishProjectLocked()
			progressMu.Unlock()
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Flatten every config into one queue, remembering where each belongs.
	type job struct{ project, config int }
	var jobs []job
	results := make([][]configResult, len(projects))
	pending := make([]int, len(projects))
	for p := range shells {
		results[p] = make([]configResult, len(shells[p].configs))
		pending[p] = len(shells[p].configs)
		for c := range shells[p].configs {
			jobs = append(jobs, job{p, c})
		}
	}

	configsDone := 0
	err = runPool(ctx, len(jobs), gatherConcurrency, func(ctx context.Context, i int) error {
		j := jobs[i]
		slug := projects[j.project].Slug
		res, err := g.gatherConfigAll(ctx, slug, shells[j.project].configs[j.config])
		if err != nil {
			return err
		}
		results[j.project][j.config] = *res

		progressMu.Lock()
		defer progressMu.Unlock()
		configsDone++
		g.onProgress(GatherProgress{
			Resource: "configs",
			Current:  configsDone,
			Total:    len(jobs),
			Message:  fmt.Sprintf("%d of %d configs read", configsDone, len(jobs)),
		})
		if pending[j.project]--; pending[j.project] == 0 {
			finishProjectLocked()
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	out := &gatheredProjects{projects: make([]ProjectData, 0, len(projects))}
	for p := range shells {
		pd := shells[p].pd
		pd.Configs = make([]ConfigData, 0, len(results[p]))
		for _, r := range results[p] {
			pd.Configs = append(pd.Configs, r.cd)
			out.serviceTokens = append(out.serviceTokens, r.tokens...)
			out.rotatedSecrets = append(out.rotatedSecrets, r.rotated...)
		}
		out.projects = append(out.projects, pd)
		out.personalConfigs = append(out.personalConfigs, shells[p].personal...)
	}
	return out, nil
}

// gatherProjectShell reads a project's own resources and its config list.
func (g *Gatherer) gatherProjectShell(ctx context.Context, proj doppler.Project) (*projectShell, error) {
	shell := &projectShell{pd: ProjectData{Project: proj}}

	envs, err := g.source.ListEnvironments(ctx, proj.Slug)
	if err != nil {
		return nil, fmt.Errorf("gather environments for %q: %w", proj.Name, err)
	}
	shell.pd.Environments = envs

	configs, err := g.source.ListConfigs(ctx, proj.Slug)
	if err != nil {
		return nil, fmt.Errorf("gather configs for %q: %w", proj.Name, err)
	}
	for _, cfg := range configs {
		// Personal configs belong to one user and cannot be migrated. Set aside
		// before any secret is read — those are that user's own overrides.
		if doppler.IsPersonalConfig(cfg.Name) {
			shell.personal = append(shell.personal, doppler.ConfigDescriptor{Project: proj.Slug, Config: cfg.Name})
			continue
		}
		shell.configs = append(shell.configs, cfg)
	}

	webhooks, err := g.source.ListWebhooks(ctx, proj.Slug)
	if err != nil && !g.refused(ResourceWebhooks, proj.Slug, err) {
		return nil, fmt.Errorf("gather webhooks for %q: %w", proj.Name, err)
	}
	shell.pd.Webhooks = webhooks

	members, err := g.source.ListProjectMembers(ctx, proj.Slug)
	if err != nil && !g.refused(ResourceProjectMembers, proj.Slug, err) {
		return nil, fmt.Errorf("gather project members for %q: %w", proj.Name, err)
	}
	shell.pd.Members = members

	return shell, nil
}

// gatherConfigAll reads one config: its secrets and trusted IPs, and the
// report-only service tokens and rotated secrets hanging off it.
func (g *Gatherer) gatherConfigAll(ctx context.Context, project string, cfg doppler.Config) (*configResult, error) {
	cd, err := g.gatherConfig(ctx, project, cfg)
	if err != nil {
		return nil, err
	}

	tokens, err := g.source.ListServiceTokens(ctx, project, cfg.Name)
	if err != nil && !g.refused(ResourceServiceTokens, project+"/"+cfg.Name, err) {
		return nil, fmt.Errorf("gather service tokens for %s/%s: %w", project, cfg.Name, err)
	}

	// Report-only: recreating a rotated secret needs its backing integration and
	// that integration's credentials.
	rotated, err := g.source.ListRotatedSecrets(ctx, project, cfg.Name)
	if err != nil && !g.refused(ResourceRotatedSecrets, project+"/"+cfg.Name, err) {
		return nil, fmt.Errorf("gather rotated secrets for %s/%s: %w", project, cfg.Name, err)
	}

	return &configResult{cd: *cd, tokens: tokens, rotated: rotated}, nil
}

// runPool calls fn for every index in [0, n) on at most workers goroutines.
//
// The first error cancels the rest and is the one returned — the later ones
// are almost always the cancellation it caused, and reporting those instead
// would bury the actual cause.
func runPool(ctx context.Context, n, workers int, fn func(context.Context, int) error) error {
	if n == 0 {
		return ctx.Err()
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		once     sync.Once
		firstErr error
		wg       sync.WaitGroup
	)
	indices := make(chan int)

	for w := 0; w < min(workers, n); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range indices {
				if err := fn(ctx, i); err != nil {
					once.Do(func() {
						firstErr = err
						cancel()
					})
				}
			}
		}()
	}

feed:
	for i := 0; i < n; i++ {
		select {
		case indices <- i:
		case <-ctx.Done():
			break feed
		}
	}
	close(indices)
	wg.Wait()

	if firstErr != nil {
		return firstErr
	}
	// A cancellation from outside is still a reason to stop.
	return ctx.Err()
}

// gatherConfig gathers secrets and trusted IPs for a single config.
func (g *Gatherer) gatherConfig(ctx context.Context, project string, cfg doppler.Config) (*ConfigData, error) {
	cd := &ConfigData{Config: cfg}

	secrets, err := readSourceSecrets(ctx, g.source, project, cfg.Name)
	if err != nil {
		return nil, fmt.Errorf("gather secrets for %s/%s: %w", project, cfg.Name, err)
	}
	cd.Secrets = secrets
	cd.SecretCount = len(secrets)

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Trusted IPs
	ips, err := g.source.ListTrustedIPs(ctx, project, cfg.Name)
	if err != nil && !g.refused(ResourceTrustedIPs, project+"/"+cfg.Name, err) {
		return nil, fmt.Errorf("gather trusted IPs for %s/%s: %w", project, cfg.Name, err)
	}
	cd.TrustedIPs = ips

	return cd, nil
}

// filterCustomRoles returns only roles where IsCustomRole is true.
func filterCustomRoles(roles []doppler.Role) []doppler.Role {
	custom := make([]doppler.Role, 0)
	for _, r := range roles {
		if r.IsCustomRole {
			custom = append(custom, r)
		}
	}
	return custom
}
