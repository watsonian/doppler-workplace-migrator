package migration

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/doppler/onprem-migrator/internal/doppler"
)

// Executor runs the 8-phase migration against the target instance.
type Executor struct {
	source               DopplerClient
	target               DopplerReadWriter
	data                 *MigrationData
	checkpoint           *CheckpointManager
	devMode              bool
	migrateWorkplaceName bool
	onProgress           func(MigrateProgress)

	// pauseMu guards paused and pauseCh together. An atomic flag alone was racy:
	// Pause and Resume each did a compare-and-swap and then touched pauseCh
	// separately, so a Pause immediately followed by a Resume could close an
	// already-closed channel and panic, and checkPause could read a stale channel.
	pauseMu sync.Mutex
	paused  bool
	pauseCh chan struct{} // non-nil while paused; closed on resume

	// idMaps caches cross-instance identifier translations. Built on first use and
	// shared by the phases that need it (policies in 7, members in 8).
	idMaps *identityMaps

	// errors collects every failure, grouped by cause. A migration of any size
	// produces hundreds of failures with a handful of causes, and the causes are
	// what can be acted on.
	errors *ErrorStore

	// sourceBaseURL and targetBaseURL let a failure be attributed to the instance
	// it actually came from, by matching the URL the request went to.
	sourceBaseURL string
	targetBaseURL string

	// stopOnNewErrorClass pauses the run the first time a kind of failure appears.
	stopOnNewErrorClass bool

	// phaseStates is where each phase stands, kept alongside the events so a
	// page that connected late — or missed a phase that finished in a
	// millisecond — can read what it did not see.
	phaseMu     sync.Mutex
	phaseStates map[string]PhaseState
}

// PhaseState is how a phase ended, or that it is running. Reason says why a
// phase was skipped: a phase with nothing to do is finished, but it did not
// run, and the two should not read the same.
type PhaseState struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

// PhaseStates returns where every phase reported so far stands.
func (e *Executor) PhaseStates() map[string]PhaseState {
	e.phaseMu.Lock()
	defer e.phaseMu.Unlock()
	out := make(map[string]PhaseState, len(e.phaseStates))
	for k, v := range e.phaseStates {
		out[k] = v
	}
	return out
}

// emitPhase records and publishes a phase-level status.
func (e *Executor) emitPhase(number int, name, status, reason, errMsg string, err error) {
	e.phaseMu.Lock()
	if e.phaseStates == nil {
		e.phaseStates = map[string]PhaseState{}
	}
	e.phaseStates[name] = PhaseState{Status: status, Reason: reason}
	e.phaseMu.Unlock()
	e.emit(MigrateProgress{Phase: number, Name: name, Status: status, Reason: reason, Error: errMsg, err: err})
}

// NewExecutor creates a migration executor.
func NewExecutor(
	source DopplerClient,
	target DopplerReadWriter,
	data *MigrationData,
	checkpoint *CheckpointManager,
	devMode bool,
	onProgress func(MigrateProgress),
	opts ...ExecutorOption,
) *Executor {
	if onProgress == nil {
		onProgress = func(MigrateProgress) {}
	}
	e := &Executor{
		source:     source,
		target:     target,
		data:       data,
		checkpoint: checkpoint,
		devMode:    devMode,
		onProgress: onProgress,
		pauseCh:    make(chan struct{}),
		errors:     NewErrorStore(),
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// WithInstanceURLs tells the executor which API roots the two clients talk to, so
// a failure can be attributed to the instance it came from rather than guessed at.
func WithInstanceURLs(source, target string) ExecutorOption {
	return func(e *Executor) {
		e.sourceBaseURL = source
		e.targetBaseURL = target
	}
}

// WithStopOnNewErrorClass pauses the run the first time a kind of failure
// appears, so a bad token is noticed after one config rather than six hundred.
// Repeats of a class already seen never pause.
func WithStopOnNewErrorClass(enabled bool) ExecutorOption {
	return func(e *Executor) { e.stopOnNewErrorClass = enabled }
}

// WithErrorStore supplies an existing store, so errors survive an executor being
// rebuilt for a re-run.
func WithErrorStore(store *ErrorStore) ExecutorOption {
	return func(e *Executor) {
		if store != nil {
			e.errors = store
		}
	}
}

// ExecutorOption configures optional migration behaviour.
type ExecutorOption func(*Executor)

// WithWorkplaceName renames the target workplace to match the source. Off unless
// asked for: the target's name is usually chosen deliberately.
func WithWorkplaceName(enabled bool) ExecutorOption {
	return func(e *Executor) { e.migrateWorkplaceName = enabled }
}

// Pause pauses the executor. The current in-flight operation finishes,
// then the executor blocks until Resume is called.
func (e *Executor) Pause() {
	e.pauseMu.Lock()
	defer e.pauseMu.Unlock()
	if !e.paused {
		e.paused = true
		e.pauseCh = make(chan struct{})
	}
}

// Resume resumes a paused executor.
func (e *Executor) Resume() {
	e.pauseMu.Lock()
	defer e.pauseMu.Unlock()
	if e.paused {
		e.paused = false
		close(e.pauseCh)
		e.pauseCh = nil
	}
}

// IsPaused returns whether the executor is currently paused.
func (e *Executor) IsPaused() bool {
	e.pauseMu.Lock()
	defer e.pauseMu.Unlock()
	return e.paused
}

// checkPause blocks if paused. Returns ctx error if context is cancelled while paused.
func (e *Executor) checkPause(ctx context.Context) error {
	// Read the flag and the channel together, so the channel waited on is always
	// the one belonging to the pause that was observed.
	e.pauseMu.Lock()
	if !e.paused {
		e.pauseMu.Unlock()
		return nil
	}
	ch := e.pauseCh
	e.pauseMu.Unlock()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-ch:
		return nil
	}
}

// Execute runs all migration phases in order.
func (e *Executor) Execute(ctx context.Context) error {
	phases := []struct {
		name string
		fn   func(ctx context.Context) error
	}{
		{"workplace_settings_and_roles", e.phase1WorkplaceSettingsAndRoles},
		{"groups_and_service_accounts", e.phase2GroupsAndServiceAccounts},
		{"projects_and_environments", e.phase3ProjectsAndEnvironments},
		{"configs", e.phase4Configs},
		{"secrets_and_locks", e.phase5SecretsAndLocks},
		{"config_inheritance", e.phase6ConfigInheritance},
		{"webhooks", e.phase7Webhooks},
		{"members", e.phase8Members},
		// Policies name users, groups and service accounts as reviewers, so they
		// run once every one of those exists and is fully set up — after members,
		// not alongside webhooks.
		{"change_request_policies", e.phase9ChangeRequestPolicies},
		// Trusted IPs stay last: restricting a config locks the migrator out of it.
		{"trusted_ips", e.phase10TrustedIPs},
	}

	var phasesWithErrors []string

	for i, phase := range phases {
		if err := e.checkPause(ctx); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		if e.checkpoint.IsPhaseComplete(phase.name) {
			log.Printf("[executor] skipping completed phase %d: %s", i+1, phase.name)
			// Said, rather than left silent: a phase that reports nothing reads
			// as one that has not run yet.
			e.emitPhase(i+1, phase.name, "completed", "", "", nil)
			continue
		}

		e.checkpoint.InitPhase(phase.name)
		e.emitPhase(i+1, phase.name, "started", "", "", nil)

		if err := phase.fn(ctx); err != nil {
			// Only context cancellation / pause errors reach here now
			e.checkpoint.SetError(fmt.Sprintf("phase %d (%s): %s", i+1, phase.name, err.Error()))
			e.emitPhase(i+1, phase.name, "failed", "", err.Error(), err)
			return fmt.Errorf("phase %d (%s): %w", i+1, phase.name, err)
		}

		phaseStatus := "completed"
		reason := ""
		if e.checkpoint.PhaseHasFailures(phase.name) {
			phaseStatus = "completed_with_errors"
			phasesWithErrors = append(phasesWithErrors, phase.name)
		} else {
			// Only mark the phase complete if there were no item failures.
			// Phases with failures remain "in_progress" so re-runs will retry failed items.
			e.checkpoint.CompletePhase(phase.name)
			// The phase still runs when it has nothing to do — it is cheap, and
			// it means this judgement can only relabel a phase, never skip work.
			if reason = e.idleReason(phase.name); reason != "" {
				phaseStatus = "skipped"
			}
		}
		e.emitPhase(i+1, phase.name, phaseStatus, reason, "", nil)
	}

	e.checkpoint.SetCompleted()
	if len(phasesWithErrors) > 0 {
		e.checkpoint.SetError(fmt.Sprintf("completed with errors in phases: %s", strings.Join(phasesWithErrors, ", ")))
	}
	return nil
}

// phase1WorkplaceSettingsAndRoles migrates workplace settings and custom roles.
func (e *Executor) phase1WorkplaceSettingsAndRoles(ctx context.Context) error {
	phase := "workplace_settings_and_roles"

	// Update workplace settings. A partial run leaves them for the full one:
	// they change the whole target, not the projects being tried.
	if e.data.Workplace != nil && !e.data.DeferWorkplaceLevel {
		itemKey := "workplace_settings"
		if e.checkpoint.GetItemStatus(phase, itemKey) != StatusCompleted {
			e.checkpoint.SetItemStatus(phase, itemKey, StatusInProgress, "")
			if !e.devMode {
				_, err := e.target.UpdateWorkplace(ctx, e.data.Workplace, e.migrateWorkplaceName)
				if err != nil {
					e.checkpoint.SetItemStatus(phase, itemKey, StatusFailed, err.Error())
					log.Printf("[error] update workplace settings: %v", err)
				} else {
					e.checkpoint.SetItemStatus(phase, itemKey, StatusCompleted, "")
				}
			} else {
				log.Printf("[dev] skipping workplace settings update")
				e.checkpoint.SetItemStatus(phase, itemKey, StatusCompleted, "")
			}
		}
	}

	// Workplace roles
	total := len(e.data.WorkplaceRoles) + len(e.data.ProjectRoles)
	current := 0

	for _, role := range e.data.WorkplaceRoles {
		if err := e.checkPause(ctx); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		current++
		itemKey := "workplace_role:" + role.Name
		if e.checkpoint.GetItemStatus(phase, itemKey) == StatusCompleted {
			continue
		}

		e.emit(MigrateProgress{
			Phase:    1,
			Name:     phase,
			Resource: "workplace_roles",
			Item:     role.Name,
			Status:   "started",
			Current:  current,
			Total:    total,
		})

		e.checkpoint.SetItemStatus(phase, itemKey, StatusInProgress, "")
		if !e.devMode {
			_, err := e.target.CreateWorkplaceRole(ctx, role.Name, role.Permissions)
			if err != nil {
				if isAlreadyExistsError(err) {
					e.checkpoint.SetItemStatus(phase, itemKey, StatusSkipped, "already exists")
					e.emit(MigrateProgress{
						Phase: 1, Name: phase, Resource: "workplace_roles",
						Item: role.Name, Status: "skipped", Current: current, Total: total,
					})
					continue
				}
				e.checkpoint.SetItemStatus(phase, itemKey, StatusFailed, err.Error())
				log.Printf("[error] create workplace role %q: %v", role.Name, err)
				e.emit(MigrateProgress{
					Phase: 1, Name: phase, Resource: "workplace_roles",
					Item: role.Name, Status: "failed", ckPhase: phase, ckKey: itemKey, Error: err.Error(), err: err, Current: current, Total: total,
				})
				continue
			}
		} else {
			log.Printf("[dev] skipping create workplace role %q", role.Name)
		}
		e.checkpoint.SetItemStatus(phase, itemKey, StatusCompleted, "")
		e.emit(MigrateProgress{
			Phase: 1, Name: phase, Resource: "workplace_roles",
			Item: role.Name, Status: "completed", Current: current, Total: total,
		})
	}

	// Project roles
	for _, role := range e.data.ProjectRoles {
		if err := e.checkPause(ctx); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		current++
		itemKey := "project_role:" + role.Name
		if e.checkpoint.GetItemStatus(phase, itemKey) == StatusCompleted {
			continue
		}

		e.emit(MigrateProgress{
			Phase: 1, Name: phase, Resource: "project_roles",
			Item: role.Name, Status: "started", Current: current, Total: total,
		})

		e.checkpoint.SetItemStatus(phase, itemKey, StatusInProgress, "")
		if !e.devMode {
			_, err := e.target.CreateProjectRole(ctx, role.Name, role.Permissions)
			if err != nil {
				if isAlreadyExistsError(err) {
					e.checkpoint.SetItemStatus(phase, itemKey, StatusSkipped, "already exists")
					e.emit(MigrateProgress{
						Phase: 1, Name: phase, Resource: "project_roles",
						Item: role.Name, Status: "skipped", Current: current, Total: total,
					})
					continue
				}
				e.checkpoint.SetItemStatus(phase, itemKey, StatusFailed, err.Error())
				log.Printf("[error] create project role %q: %v", role.Name, err)
				e.emit(MigrateProgress{
					Phase: 1, Name: phase, Resource: "project_roles",
					Item: role.Name, Status: "failed", ckPhase: phase, ckKey: itemKey, Error: err.Error(), err: err, Current: current, Total: total,
				})
				continue
			}
		} else {
			log.Printf("[dev] skipping create project role %q", role.Name)
		}
		e.checkpoint.SetItemStatus(phase, itemKey, StatusCompleted, "")
		e.emit(MigrateProgress{
			Phase: 1, Name: phase, Resource: "project_roles",
			Item: role.Name, Status: "completed", Current: current, Total: total,
		})
	}

	return nil
}

// phase2GroupsAndServiceAccounts creates groups and service accounts.
func (e *Executor) phase2GroupsAndServiceAccounts(ctx context.Context) error {
	phase := "groups_and_service_accounts"

	total := len(e.data.Groups) + len(e.data.ServiceAccounts)
	current := 0

	for _, gwm := range e.data.Groups {
		if err := e.checkPause(ctx); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		current++
		itemKey := "group:" + gwm.Group.Slug
		if e.checkpoint.GetItemStatus(phase, itemKey) == StatusCompleted {
			continue
		}

		e.emit(MigrateProgress{
			Phase: 2, Name: phase, Resource: "groups",
			Item: gwm.Group.Name, Status: "started", Current: current, Total: total,
		})

		e.checkpoint.SetItemStatus(phase, itemKey, StatusInProgress, "")
		if !e.devMode {
			_, err := e.target.CreateGroup(ctx, &gwm.Group)
			if err != nil && !isAlreadyExistsError(err) {
				e.checkpoint.SetItemStatus(phase, itemKey, StatusFailed, err.Error())
				log.Printf("[error] create group %q: %v", gwm.Group.Name, err)
				e.emit(MigrateProgress{
					Phase: 2, Name: phase, Resource: "groups",
					Item: gwm.Group.Name, Status: "failed", ckPhase: phase, ckKey: itemKey, Error: err.Error(), err: err, Current: current, Total: total,
				})
				continue
			}
		} else {
			log.Printf("[dev] skipping create group %q", gwm.Group.Name)
		}
		e.checkpoint.SetItemStatus(phase, itemKey, StatusCompleted, "")
		e.emit(MigrateProgress{
			Phase: 2, Name: phase, Resource: "groups",
			Item: gwm.Group.Name, Status: "completed", Current: current, Total: total,
		})
	}

	for _, sa := range e.data.ServiceAccounts {
		if err := e.checkPause(ctx); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		current++
		itemKey := "service_account:" + sa.Slug
		if e.checkpoint.GetItemStatus(phase, itemKey) == StatusCompleted {
			continue
		}

		e.emit(MigrateProgress{
			Phase: 2, Name: phase, Resource: "service_accounts",
			Item: sa.Name, Status: "started", Current: current, Total: total,
		})

		e.checkpoint.SetItemStatus(phase, itemKey, StatusInProgress, "")
		if !e.devMode {
			_, err := e.target.CreateServiceAccount(ctx, &sa)
			if err != nil && !isAlreadyExistsError(err) {
				e.checkpoint.SetItemStatus(phase, itemKey, StatusFailed, err.Error())
				log.Printf("[error] create service account %q: %v", sa.Name, err)
				e.emit(MigrateProgress{
					Phase: 2, Name: phase, Resource: "service_accounts",
					Item: sa.Name, Status: "failed", ckPhase: phase, ckKey: itemKey, Error: err.Error(), err: err, Current: current, Total: total,
				})
				continue
			}
		} else {
			log.Printf("[dev] skipping create service account %q", sa.Name)
		}
		e.checkpoint.SetItemStatus(phase, itemKey, StatusCompleted, "")
		e.emit(MigrateProgress{
			Phase: 2, Name: phase, Resource: "service_accounts",
			Item: sa.Name, Status: "completed", Current: current, Total: total,
		})
	}

	return e.migrateServiceAccountIdentities(ctx, phase)
}

// migrateServiceAccountIdentities creates each service account's keyless auth
// identities. Runs after every service account exists, since identities are nested
// under one and the target assigns its own account slugs.
func (e *Executor) migrateServiceAccountIdentities(ctx context.Context, phase string) error {
	identities := e.data.ServiceAccountIdentities
	if len(identities) == 0 {
		return nil
	}

	// Map source account slug -> target account slug by name. Built here rather than
	// via getIdentityMaps because the accounts were only just created, and that cache
	// may not have been populated yet.
	targetSlugs := map[string]string{}
	if !e.devMode {
		targetSAs, err := e.target.ListServiceAccounts(ctx)
		if err != nil {
			for _, id := range identities {
				e.checkpoint.SetItemStatus(phase, "service_account_identity:"+id.ServiceAccount+"/"+id.Name,
					StatusFailed, "could not resolve service accounts on target: "+err.Error())
			}
			log.Printf("[error] list target service accounts for identities: %v", err)
			return nil
		}
		byName := make(map[string]string, len(targetSAs))
		for _, sa := range targetSAs {
			byName[sa.Name] = sa.Slug
		}
		for _, sa := range e.data.ServiceAccounts {
			if slug, ok := byName[sa.Name]; ok {
				targetSlugs[sa.Slug] = slug
			}
		}
	}

	// Source account slug -> name, for readable progress and skip reasons.
	sourceNames := make(map[string]string, len(e.data.ServiceAccounts))
	for _, sa := range e.data.ServiceAccounts {
		sourceNames[sa.Slug] = sa.Name
	}

	total := len(identities)
	for i, identity := range identities {
		if err := e.checkPause(ctx); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		current := i + 1
		accountName := sourceNames[identity.ServiceAccount]
		if accountName == "" {
			accountName = identity.ServiceAccount
		}
		label := accountName + "/" + identity.Name
		itemKey := "service_account_identity:" + identity.ServiceAccount + "/" + identity.Name

		if e.checkpoint.GetItemStatus(phase, itemKey) == StatusCompleted {
			continue
		}

		e.emit(MigrateProgress{
			Phase: 2, Name: phase, Resource: "service_account_identities",
			Item: label, Status: "started", Current: current, Total: total,
		})

		e.checkpoint.SetItemStatus(phase, itemKey, StatusInProgress, "")

		if e.devMode {
			log.Printf("[dev] skipping create service account identity %s", label)
			e.checkpoint.SetItemStatus(phase, itemKey, StatusCompleted, "")
			e.emit(MigrateProgress{
				Phase: 2, Name: phase, Resource: "service_account_identities",
				Item: label, Status: "completed", Current: current, Total: total,
			})
			continue
		}

		targetSlug, ok := targetSlugs[identity.ServiceAccount]
		if !ok {
			reason := "service account not found on target"
			e.checkpoint.SetItemStatus(phase, itemKey, StatusSkipped, reason)
			log.Printf("[skip] service account identity %s: %s", label, reason)
			e.emit(MigrateProgress{
				Phase: 2, Name: phase, Resource: "service_account_identities",
				Item: label, Status: "skipped", Current: current, Total: total,
			})
			continue
		}

		if _, err := e.target.CreateServiceAccountIdentity(ctx, targetSlug, &identity); err != nil {
			if isAlreadyExistsError(err) {
				e.checkpoint.SetItemStatus(phase, itemKey, StatusSkipped, "already exists")
				e.emit(MigrateProgress{
					Phase: 2, Name: phase, Resource: "service_account_identities",
					Item: label, Status: "skipped", Current: current, Total: total,
				})
				continue
			}
			e.checkpoint.SetItemStatus(phase, itemKey, StatusFailed, err.Error())
			log.Printf("[error] create service account identity %s: %v", label, err)
			e.emit(MigrateProgress{
				Phase: 2, Name: phase, Resource: "service_account_identities",
				Item: label, Status: "failed", ckPhase: phase, ckKey: itemKey, Error: err.Error(), err: err, Current: current, Total: total,
			})
			continue
		}

		e.checkpoint.SetItemStatus(phase, itemKey, StatusCompleted, "")
		e.emit(MigrateProgress{
			Phase: 2, Name: phase, Resource: "service_account_identities",
			Item: label, Status: "completed", Current: current, Total: total,
		})
	}

	return nil
}

// phase3ProjectsAndEnvironments creates projects and environments.
func (e *Executor) phase3ProjectsAndEnvironments(ctx context.Context) error {
	phase := "projects_and_environments"

	total := len(e.data.Projects)
	for _, pd := range e.data.Projects {
		total += len(pd.Environments)
	}
	current := 0

	for _, pd := range e.data.Projects {
		if err := e.checkPause(ctx); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		current++
		projKey := "project:" + pd.Project.Slug
		if e.checkpoint.GetItemStatus(phase, projKey) != StatusCompleted {
			e.emit(MigrateProgress{
				Phase: 3, Name: phase, Resource: "projects",
				Item: pd.Project.Name, Status: "started", Current: current, Total: total,
			})

			e.checkpoint.SetItemStatus(phase, projKey, StatusInProgress, "")
			projectFailed := false
			if !e.devMode {
				// Check if project already exists by slug
				_, err := e.target.GetProject(ctx, pd.Project.Slug)
				if err != nil {
					// Doesn't exist, create it
					_, err = e.target.CreateProject(ctx, pd.Project.Name, pd.Project.Description)
					if err != nil && !isAlreadyExistsError(err) {
						e.checkpoint.SetItemStatus(phase, projKey, StatusFailed, err.Error())
						log.Printf("[error] create project %q: %v", pd.Project.Slug, err)
						e.emit(MigrateProgress{
							Phase: 3, Name: phase, Resource: "projects",
							Item: pd.Project.Name, Status: "failed", ckPhase: phase, ckKey: projKey, Error: err.Error(), err: err, Current: current, Total: total,
						})
						projectFailed = true
					}
				}
			} else {
				log.Printf("[dev] skipping create project %q", pd.Project.Slug)
			}
			if !projectFailed {
				e.checkpoint.SetItemStatus(phase, projKey, StatusCompleted, "")
				e.emit(MigrateProgress{
					Phase: 3, Name: phase, Resource: "projects",
					Item: pd.Project.Name, Status: "completed", Current: current, Total: total,
				})
			}
		}

		// Environments for this project
		for _, env := range pd.Environments {
			if err := e.checkPause(ctx); err != nil {
				return err
			}

			current++
			envKey := fmt.Sprintf("env:%s/%s", pd.Project.Slug, env.Slug)
			if e.checkpoint.GetItemStatus(phase, envKey) == StatusCompleted {
				continue
			}

			e.emit(MigrateProgress{
				Phase: 3, Name: phase, Resource: "environments",
				Item: fmt.Sprintf("%s/%s", pd.Project.Slug, env.Name), Status: "started",
				Current: current, Total: total,
			})

			e.checkpoint.SetItemStatus(phase, envKey, StatusInProgress, "")
			if !e.devMode {
				_, err := e.target.CreateEnvironment(ctx, pd.Project.Slug, env.Name, env.Slug, env.PersonalConfigs)
				if err != nil && !isAlreadyExistsError(err) {
					e.checkpoint.SetItemStatus(phase, envKey, StatusFailed, err.Error())
					log.Printf("[error] create environment %s/%s: %v", pd.Project.Slug, env.Slug, err)
					e.emit(MigrateProgress{
						Phase: 3, Name: phase, Resource: "environments",
						Item: fmt.Sprintf("%s/%s", pd.Project.Slug, env.Name), Status: "failed", ckPhase: phase, ckKey: envKey, Error: err.Error(), err: err,
						Current: current, Total: total,
					})
					continue
				}
			} else {
				log.Printf("[dev] skipping create environment %s/%s", pd.Project.Slug, env.Slug)
			}
			e.checkpoint.SetItemStatus(phase, envKey, StatusCompleted, "")
			e.emit(MigrateProgress{
				Phase: 3, Name: phase, Resource: "environments",
				Item: fmt.Sprintf("%s/%s", pd.Project.Slug, env.Name), Status: "completed",
				Current: current, Total: total,
			})
		}

		// Environments are ordered, and the order is only meaningful once they all
		// exist — so set it after the create loop, using the source's ordering.
		if err := e.applyEnvironmentOrder(ctx, phase, pd); err != nil {
			return err
		}
	}

	return nil
}

// applyEnvironmentOrder reproduces the source project's environment ordering on the
// target. A single environment needs no ordering.
func (e *Executor) applyEnvironmentOrder(ctx context.Context, phase string, pd ProjectData) error {
	if len(pd.Environments) < 2 {
		return nil
	}

	itemKey := "env_order:" + pd.Project.Slug
	if e.checkpoint.GetItemStatus(phase, itemKey) == StatusCompleted {
		return nil
	}

	order := make([]string, 0, len(pd.Environments))
	for _, env := range pd.Environments {
		order = append(order, env.Slug)
	}

	e.emit(MigrateProgress{
		Phase: 3, Name: phase, Resource: "environment_order",
		Item: pd.Project.Slug, Status: "started",
	})

	e.checkpoint.SetItemStatus(phase, itemKey, StatusInProgress, "")
	if !e.devMode {
		if err := e.target.SetEnvironmentOrder(ctx, pd.Project.Slug, order); err != nil {
			e.checkpoint.SetItemStatus(phase, itemKey, StatusFailed, err.Error())
			log.Printf("[error] set environment order for %s: %v", pd.Project.Slug, err)
			e.emit(MigrateProgress{
				Phase: 3, Name: phase, Resource: "environment_order",
				Item: pd.Project.Slug, Status: "failed", ckPhase: phase, ckKey: itemKey, Error: err.Error(), err: err,
			})
			return nil
		}
	} else {
		log.Printf("[dev] skipping environment order for %s", pd.Project.Slug)
	}

	e.checkpoint.SetItemStatus(phase, itemKey, StatusCompleted, "")
	e.emit(MigrateProgress{
		Phase: 3, Name: phase, Resource: "environment_order",
		Item: pd.Project.Slug, Status: "completed",
	})
	return nil
}

// phase4Configs verifies root configs and creates branch configs.
func (e *Executor) phase4Configs(ctx context.Context) error {
	phase := "configs"

	total := 0
	for _, pd := range e.data.Projects {
		total += len(pd.Configs)
	}
	current := 0

	for _, pd := range e.data.Projects {
		for i := range pd.Configs {
			cd := &pd.Configs[i]
			if err := e.checkPause(ctx); err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}

			current++
			cfgKey := fmt.Sprintf("config:%s/%s", pd.Project.Slug, cd.Config.Name)
			if e.checkpoint.GetItemStatus(phase, cfgKey) == StatusCompleted {
				continue
			}

			e.emit(MigrateProgress{
				Phase: 4, Name: phase, Resource: "configs",
				Item: fmt.Sprintf("%s/%s", pd.Project.Slug, cd.Config.Name), Status: "started",
				Current: current, Total: total,
			})

			e.checkpoint.SetItemStatus(phase, cfgKey, StatusInProgress, "")
			configFailed := false
			if !e.devMode {
				if cd.Config.Root {
					// Root configs are auto-created with environments — just verify
					_, err := e.target.GetConfig(ctx, pd.Project.Slug, cd.Config.Name)
					if err != nil {
						e.checkpoint.SetItemStatus(phase, cfgKey, StatusFailed, "root config not found: "+err.Error())
						log.Printf("[error] verify root config %s/%s: %v", pd.Project.Slug, cd.Config.Name, err)
						e.emit(MigrateProgress{
							Phase: 4, Name: phase, Resource: "configs",
							Item: fmt.Sprintf("%s/%s", pd.Project.Slug, cd.Config.Name), Status: "failed", ckPhase: phase, ckKey: cfgKey,
							Error: err.Error(), err: err, Current: current, Total: total,
						})
						configFailed = true
					}
				} else {
					// Branch config — create
					_, err := e.target.CreateConfig(ctx, pd.Project.Slug, cd.Config.Environment, cd.Config.Name)
					if err != nil && !isAlreadyExistsError(err) {
						e.checkpoint.SetItemStatus(phase, cfgKey, StatusFailed, err.Error())
						log.Printf("[error] create config %s/%s: %v", pd.Project.Slug, cd.Config.Name, err)
						e.emit(MigrateProgress{
							Phase: 4, Name: phase, Resource: "configs",
							Item: fmt.Sprintf("%s/%s", pd.Project.Slug, cd.Config.Name), Status: "failed", ckPhase: phase, ckKey: cfgKey,
							Error: err.Error(), err: err, Current: current, Total: total,
						})
						configFailed = true
					}
				}
			} else {
				log.Printf("[dev] skipping config %s/%s (root=%v)", pd.Project.Slug, cd.Config.Name, cd.Config.Root)
			}
			if configFailed {
				continue
			}
			e.checkpoint.SetItemStatus(phase, cfgKey, StatusCompleted, "")
			e.emit(MigrateProgress{
				Phase: 4, Name: phase, Resource: "configs",
				Item: fmt.Sprintf("%s/%s", pd.Project.Slug, cd.Config.Name), Status: "completed",
				Current: current, Total: total,
			})
		}
	}

	return nil
}

// phase5SecretsAndLocks migrates secrets, then locks configs (locking prevents
// further writes). Trusted IPs deliberately do not happen here — see phase9TrustedIPs.
func (e *Executor) phase5SecretsAndLocks(ctx context.Context) error {
	phase := "secrets_and_locks"

	total := 0
	for _, pd := range e.data.Projects {
		total += len(pd.Configs) // secrets pass
	}
	current := 0

	for pi := range e.data.Projects {
		pd := &e.data.Projects[pi]
		// Secret notes live at the project level; track which have been written so
		// the same note is not re-sent for every config in the project.
		notesWritten := make(map[string]bool)
		for ci := range pd.Configs {
			cd := &pd.Configs[ci]
			if err := e.checkPause(ctx); err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}

			current++
			secretsKey := fmt.Sprintf("secrets:%s/%s", pd.Project.Slug, cd.Config.Name)
			if e.checkpoint.GetItemStatus(phase, secretsKey) != StatusCompleted {
				e.emit(MigrateProgress{
					Phase: 5, Name: phase, Resource: "secrets",
					Item: fmt.Sprintf("%s/%s", pd.Project.Slug, cd.Config.Name), Status: "started",
					Current: current, Total: total,
				})

				e.checkpoint.SetItemStatus(phase, secretsKey, StatusInProgress, "")
				secretsFailed := false
				if !e.devMode {
					// Secrets are wiped from memory after every attempt, success or
					// failure. A failed item therefore comes back here with nothing
					// left to send — writing nothing and reporting success would be
					// silent data loss on exactly the path the checkpoint exists for.
					if len(cd.Secrets) == 0 && cd.SecretCount > 0 {
						const reason = "secrets were cleared from memory after an earlier attempt; re-run Gather before retrying this migration"
						e.checkpoint.SetItemStatus(phase, secretsKey, StatusFailed, reason)
						log.Printf("[error] cannot retry secrets for %s/%s: %s", pd.Project.Slug, cd.Config.Name, reason)
						e.emit(MigrateProgress{
							Phase: 5, Name: phase, Resource: "secrets",
							Item: fmt.Sprintf("%s/%s", pd.Project.Slug, cd.Config.Name), Status: "failed", ckPhase: phase, ckKey: secretsKey,
							Error: reason, class: ClassStaleGather, Current: current, Total: total,
						})
						secretsFailed = true
					} else if len(cd.Secrets) > 0 {
						// Written through the change_requests format rather than the
						// plain secrets map, because that is the only write path that
						// carries visibility. Sending the plain map would land every
						// restricted or masked secret on the target at the default
						// visibility — a silent downgrade of the source's protections.
						//
						// Raw values are used so ${REFERENCE} syntax survives.
						changes := make([]doppler.SecretChangeRequest, 0, len(cd.Secrets))
						for name, secret := range cd.Secrets {
							value := secret.Raw
							change := doppler.SecretChangeRequest{
								Name:         name,
								OriginalName: nil, // nil == null: the secret is new on the target
								Value:        &value,
							}
							if secret.RawVisibility != "" {
								visibility := secret.RawVisibility
								change.Visibility = &visibility
							}
							// A secret declared as json, yaml, integer and so on keeps
							// that type; without it everything lands as a plain string.
							if secret.RawValueType != nil && secret.RawValueType.Type != "" {
								valueType := *secret.RawValueType
								change.ValueType = &valueType
							}
							changes = append(changes, change)
						}
						if err := e.target.UpdateSecretsWithMetadata(ctx, pd.Project.Slug, cd.Config.Name, changes); err != nil {
							e.checkpoint.SetItemStatus(phase, secretsKey, StatusFailed, err.Error())
							log.Printf("[error] update secrets %s/%s: %v", pd.Project.Slug, cd.Config.Name, err)
							e.emit(MigrateProgress{
								Phase: 5, Name: phase, Resource: "secrets",
								Item: fmt.Sprintf("%s/%s", pd.Project.Slug, cd.Config.Name), Status: "failed", ckPhase: phase, ckKey: secretsKey,
								Error: err.Error(), err: err, Current: current, Total: total,
							})
							secretsFailed = true
						} else {
							// Notes are project-scoped, so write each one once per
							// project rather than repeating it for every config that
							// happens to contain the same secret name.
							for name, secret := range cd.Secrets {
								if secret.Note == "" || notesWritten[name] {
									continue
								}
								notesWritten[name] = true
								if err := e.target.UpdateSecretNote(ctx, pd.Project.Slug, name, secret.Note); err != nil {
									log.Printf("[warn] failed to set note for %s/%s: %v", pd.Project.Slug, name, err)
								}
							}
						}
					}
				} else {
					log.Printf("[dev] skipping secrets for %s/%s (%d secrets)", pd.Project.Slug, cd.Config.Name, len(cd.Secrets))
				}
				if !secretsFailed {
					e.checkpoint.SetItemStatus(phase, secretsKey, StatusCompleted, "")
					e.emit(MigrateProgress{
						Phase: 5, Name: phase, Resource: "secrets",
						Item: fmt.Sprintf("%s/%s", pd.Project.Slug, cd.Config.Name), Status: "completed",
						Current: current, Total: total,
					})
				}
			}

			// Clear secrets from memory regardless of success/failure
			cd.Secrets = nil

			// Lock config if it was locked on source.
			// Skip root configs (Doppler API does not allow locking root configs).
			// Skip locking if secrets failed — locking would prevent re-run from writing secrets.
			secretsKey2 := fmt.Sprintf("secrets:%s/%s", pd.Project.Slug, cd.Config.Name)
			secretsOK := e.checkpoint.GetItemStatus(phase, secretsKey2) == StatusCompleted
			if cd.Config.Locked && !cd.Config.Root && secretsOK {
				lockKey := fmt.Sprintf("lock:%s/%s", pd.Project.Slug, cd.Config.Name)
				if e.checkpoint.GetItemStatus(phase, lockKey) != StatusCompleted {
					e.checkpoint.SetItemStatus(phase, lockKey, StatusInProgress, "")
					if !e.devMode {
						if err := e.target.LockConfig(ctx, pd.Project.Slug, cd.Config.Name); err != nil {
							e.checkpoint.SetItemStatus(phase, lockKey, StatusFailed, err.Error())
							log.Printf("[error] lock config %s/%s: %v", pd.Project.Slug, cd.Config.Name, err)
						} else {
							e.checkpoint.SetItemStatus(phase, lockKey, StatusCompleted, "")
						}
					} else {
						log.Printf("[dev] skipping lock config %s/%s", pd.Project.Slug, cd.Config.Name)
						e.checkpoint.SetItemStatus(phase, lockKey, StatusCompleted, "")
					}
				}
			} else if cd.Config.Locked && !cd.Config.Root && !secretsOK {
				lockKey := fmt.Sprintf("lock:%s/%s", pd.Project.Slug, cd.Config.Name)
				e.checkpoint.SetItemStatus(phase, lockKey, StatusFailed, "skipped locking: secrets failed for this config")
				log.Printf("[warn] skipping lock for %s/%s: secrets failed", pd.Project.Slug, cd.Config.Name)
			}
		}
	}

	return nil
}

// phase6ConfigInheritance marks configs inheritable, then wires up which configs
// inherit from which. Both passes run here because every config has to exist first,
// and the inheritable flags have to be set before anything can inherit from them.
func (e *Executor) phase6ConfigInheritance(ctx context.Context) error {
	if err := e.setInheritableFlags(ctx); err != nil {
		return err
	}
	return e.setInheritanceRelationships(ctx)
}

// setInheritableFlags marks configs as available to inherit from.
func (e *Executor) setInheritableFlags(ctx context.Context) error {
	phase := "config_inheritance"

	for _, pd := range e.data.Projects {
		for _, cd := range pd.Configs {
			if !cd.Config.Inheritable {
				continue
			}

			if err := e.checkPause(ctx); err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}

			itemKey := fmt.Sprintf("inheritable:%s/%s", pd.Project.Slug, cd.Config.Name)
			if e.checkpoint.GetItemStatus(phase, itemKey) == StatusCompleted {
				continue
			}

			e.emit(MigrateProgress{
				Phase: 6, Name: phase, Resource: "config_inheritance",
				Item: fmt.Sprintf("%s/%s", pd.Project.Slug, cd.Config.Name), Status: "started",
			})

			e.checkpoint.SetItemStatus(phase, itemKey, StatusInProgress, "")
			if !e.devMode {
				if err := e.target.SetConfigInheritable(ctx, pd.Project.Slug, cd.Config.Name, true); err != nil {
					e.checkpoint.SetItemStatus(phase, itemKey, StatusFailed, err.Error())
					log.Printf("[error] set inheritable %s/%s: %v", pd.Project.Slug, cd.Config.Name, err)
					e.emit(MigrateProgress{
						Phase: 6, Name: phase, Resource: "config_inheritance",
						Item: fmt.Sprintf("%s/%s", pd.Project.Slug, cd.Config.Name), Status: "failed", ckPhase: phase, ckKey: itemKey, Error: err.Error(), err: err,
					})
					continue
				}
			} else {
				log.Printf("[dev] skipping set inheritable %s/%s", pd.Project.Slug, cd.Config.Name)
			}
			e.checkpoint.SetItemStatus(phase, itemKey, StatusCompleted, "")
			e.emit(MigrateProgress{
				Phase: 6, Name: phase, Resource: "config_inheritance",
				Item: fmt.Sprintf("%s/%s", pd.Project.Slug, cd.Config.Name), Status: "completed",
			})
		}
	}

	return nil
}

// phase7Webhooks creates webhooks with URL-match idempotency.
func (e *Executor) phase7Webhooks(ctx context.Context) error {
	phase := "webhooks"

	total := 0
	for _, pd := range e.data.Projects {
		total += len(pd.Webhooks)
	}
	current := 0

	for _, pd := range e.data.Projects {
		// Get existing webhooks on target for idempotency check
		var existingURLs map[string]bool
		if !e.devMode && len(pd.Webhooks) > 0 {
			existing, err := e.target.ListWebhooks(ctx, pd.Project.Slug)
			if err != nil {
				// Non-fatal: proceed without idempotency check
				log.Printf("[warn] failed to list existing webhooks for %s: %v", pd.Project.Slug, err)
			} else {
				existingURLs = make(map[string]bool, len(existing))
				for _, wh := range existing {
					existingURLs[wh.URL] = true
				}
			}
		}

		for _, wh := range pd.Webhooks {
			if err := e.checkPause(ctx); err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}

			current++
			itemKey := fmt.Sprintf("webhook:%s/%s", pd.Project.Slug, wh.URL)
			if e.checkpoint.GetItemStatus(phase, itemKey) == StatusCompleted {
				continue
			}

			e.emit(MigrateProgress{
				Phase: 7, Name: phase, Resource: "webhooks",
				Item: fmt.Sprintf("%s: %s", pd.Project.Slug, wh.URL), Status: "started",
				Current: current, Total: total,
			})

			// URL-match idempotency
			if existingURLs != nil && existingURLs[wh.URL] {
				e.checkpoint.SetItemStatus(phase, itemKey, StatusSkipped, "URL already exists")
				e.emit(MigrateProgress{
					Phase: 7, Name: phase, Resource: "webhooks",
					Item: fmt.Sprintf("%s: %s", pd.Project.Slug, wh.URL), Status: "skipped",
					Current: current, Total: total,
				})
				continue
			}

			e.checkpoint.SetItemStatus(phase, itemKey, StatusInProgress, "")
			if !e.devMode {
				webhook := &doppler.Webhook{
					URL:            wh.URL,
					Secret:         wh.Secret,
					Enabled:        wh.Enabled,
					EnabledConfigs: wh.EnabledConfigs,
				}
				_, err := e.target.CreateWebhook(ctx, pd.Project.Slug, webhook)
				if err != nil {
					e.checkpoint.SetItemStatus(phase, itemKey, StatusFailed, err.Error())
					log.Printf("[error] create webhook %s for %s: %v", wh.URL, pd.Project.Slug, err)
					e.emit(MigrateProgress{
						Phase: 7, Name: phase, Resource: "webhooks",
						Item: fmt.Sprintf("%s: %s", pd.Project.Slug, wh.URL), Status: "failed", ckPhase: phase, ckKey: itemKey, Error: err.Error(), err: err,
						Current: current, Total: total,
					})
					continue
				}
			} else {
				log.Printf("[dev] skipping create webhook %s for %s", wh.URL, pd.Project.Slug)
			}
			e.checkpoint.SetItemStatus(phase, itemKey, StatusCompleted, "")
			e.emit(MigrateProgress{
				Phase: 7, Name: phase, Resource: "webhooks",
				Item: fmt.Sprintf("%s: %s", pd.Project.Slug, wh.URL), Status: "completed",
				Current: current, Total: total,
			})
		}
	}

	return nil
}

// identityMaps translates source-instance identifiers to their target-instance
// equivalents. Groups, service accounts and users all get fresh slugs on the target,
// so anything referencing them by slug — project members, group members, change
// request policy reviewers — has to be remapped through here.
type identityMaps struct {
	// targetEmailSet holds the emails of users present on the target. Email is the
	// only identifier stable across instances.
	targetEmailSet map[string]bool
	// emailToTargetID maps a user's email to their target workplace user id.
	emailToTargetID map[string]string
	// sourceSlugToEmail maps any source user identifier (id, username, email) to
	// that user's canonical email.
	sourceSlugToEmail map[string]string
	// groupSlugs and serviceAccountSlugs map source slug → target slug, matched by
	// name since that is what survives migration.
	groupSlugs          map[string]string
	serviceAccountSlugs map[string]string
}

// getIdentityMaps builds the maps on first use and caches them for later phases.
// In dev mode nothing is read from the target, so the maps are empty.
func (e *Executor) getIdentityMaps(ctx context.Context) (*identityMaps, error) {
	if e.idMaps != nil {
		return e.idMaps, nil
	}

	m := &identityMaps{
		targetEmailSet:      map[string]bool{},
		emailToTargetID:     map[string]string{},
		sourceSlugToEmail:   map[string]string{},
		groupSlugs:          map[string]string{},
		serviceAccountSlugs: map[string]string{},
	}

	// Map all source user identifiers (ID, username, email) to their canonical email.
	for _, u := range e.data.WorkplaceUsers {
		m.sourceSlugToEmail[u.ID] = u.User.Email
		m.sourceSlugToEmail[u.User.Username] = u.User.Email
		m.sourceSlugToEmail[u.User.Email] = u.User.Email
	}

	if e.devMode {
		e.idMaps = m
		return m, nil
	}

	targetUsers, err := e.target.ListWorkplaceUsers(ctx)
	if err != nil {
		return nil, fmt.Errorf("list target users: %w", err)
	}
	for _, u := range targetUsers {
		if u.User.Email != "" {
			m.targetEmailSet[u.User.Email] = true
			m.emailToTargetID[u.User.Email] = u.ID
		}
	}

	// Source groups have UUID slugs; target groups get different ones when created
	// by name, so match on name.
	if len(e.data.Groups) > 0 {
		targetGroups, err := e.target.ListGroups(ctx)
		if err != nil {
			log.Printf("[warn] could not list target groups for slug mapping: %v", err)
		} else {
			byName := make(map[string]string, len(targetGroups))
			for _, g := range targetGroups {
				byName[g.Name] = g.Slug
			}
			for _, gwm := range e.data.Groups {
				if targetSlug, ok := byName[gwm.Group.Name]; ok {
					m.groupSlugs[gwm.Group.Slug] = targetSlug
				}
			}
		}
	}

	// Same pattern for service accounts.
	if len(e.data.ServiceAccounts) > 0 {
		targetSAs, err := e.target.ListServiceAccounts(ctx)
		if err != nil {
			log.Printf("[warn] could not list target service accounts for slug mapping: %v", err)
		} else {
			byName := make(map[string]string, len(targetSAs))
			for _, sa := range targetSAs {
				byName[sa.Name] = sa.Slug
			}
			for _, sa := range e.data.ServiceAccounts {
				if targetSlug, ok := byName[sa.Name]; ok {
					m.serviceAccountSlugs[sa.Slug] = targetSlug
				}
			}
		}
	}

	e.idMaps = m
	return m, nil
}

// email resolves any source user identifier to an email, falling back to the
// identifier itself when the user wasn't in the gathered workplace list.
func (m *identityMaps) email(sourceSlug string) string {
	if email := m.sourceSlugToEmail[sourceSlug]; email != "" {
		return email
	}
	return sourceSlug
}

// phase8Members adds group members and project members.
// Missing users are skipped (not errors) since users must be provisioned via SCIM.
func (e *Executor) phase8Members(ctx context.Context) error {
	phase := "members"

	maps, err := e.getIdentityMaps(ctx)
	if err != nil {
		return err
	}
	targetEmailSet := maps.targetEmailSet
	targetEmailToSlug := maps.emailToTargetID
	sourceEmailMap := maps.sourceSlugToEmail
	groupSlugMap := maps.groupSlugs
	serviceAccountSlugMap := maps.serviceAccountSlugs

	// Group members
	for _, gwm := range e.data.Groups {
		// Resolve the target group slug
		targetGroupSlug := gwm.Group.Slug
		if mapped, ok := groupSlugMap[gwm.Group.Slug]; ok {
			targetGroupSlug = mapped
		}

		for _, member := range gwm.Members {
			if err := e.checkPause(ctx); err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}

			itemKey := fmt.Sprintf("group_member:%s/%s", gwm.Group.Slug, member.Slug)
			if e.checkpoint.GetItemStatus(phase, itemKey) == StatusCompleted ||
				e.checkpoint.GetItemStatus(phase, itemKey) == StatusSkipped {
				continue
			}

			// Check if user exists on target (skip if workplace_user and missing)
			if member.Type == "workplace_user" && !e.devMode {
				email := sourceEmailMap[member.Slug]
				if email == "" {
					email = member.Slug
				}
				if !targetEmailSet[email] {
					e.checkpoint.SetItemStatus(phase, itemKey, StatusSkipped, "user not found on target")
					log.Printf("[skip] group member %s/%s: user not on target", gwm.Group.Slug, member.Slug)
					continue
				}
			}

			// Resolve member slug to target instance ID
			memberSlug := member.Slug
			if member.Type == "workplace_user" {
				email := sourceEmailMap[member.Slug]
				if targetSlug, ok := targetEmailToSlug[email]; ok {
					memberSlug = targetSlug
				}
			} else if member.Type == "service_account" {
				if mapped, ok := serviceAccountSlugMap[member.Slug]; ok {
					memberSlug = mapped
				}
			}

			e.checkpoint.SetItemStatus(phase, itemKey, StatusInProgress, "")
			if !e.devMode {
				err := e.target.AddGroupMember(ctx, targetGroupSlug, member.Type, memberSlug)
				if err != nil && !isAlreadyExistsError(err) {
					e.checkpoint.SetItemStatus(phase, itemKey, StatusFailed, err.Error())
					log.Printf("[error] add group member %s to %s: %v", memberSlug, targetGroupSlug, err)
					continue
				}
			} else {
				log.Printf("[dev] skipping add group member %s to %s", member.Slug, gwm.Group.Slug)
			}
			e.checkpoint.SetItemStatus(phase, itemKey, StatusCompleted, "")
		}
	}

	// Project members
	for _, pd := range e.data.Projects {
		for _, member := range pd.Members {
			if err := e.checkPause(ctx); err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}

			itemKey := fmt.Sprintf("project_member:%s/%s", pd.Project.Slug, member.Slug)
			if e.checkpoint.GetItemStatus(phase, itemKey) == StatusCompleted ||
				e.checkpoint.GetItemStatus(phase, itemKey) == StatusSkipped {
				continue
			}

			// Check if user exists on target
			if member.Type == "workplace_user" && !e.devMode {
				email := sourceEmailMap[member.Slug]
				if email == "" {
					email = member.Slug
				}
				if !targetEmailSet[email] {
					e.checkpoint.SetItemStatus(phase, itemKey, StatusSkipped, "user not found on target")
					log.Printf("[skip] project member %s/%s: user not on target", pd.Project.Slug, member.Slug)
					continue
				}
			}

			// Resolve member slug to target instance ID
			memberSlug := member.Slug
			switch member.Type {
			case "group":
				if mapped, ok := groupSlugMap[member.Slug]; ok {
					memberSlug = mapped
				}
			case "workplace_user":
				email := sourceEmailMap[member.Slug]
				if targetSlug, ok := targetEmailToSlug[email]; ok {
					memberSlug = targetSlug
				}
			case "service_account":
				if mapped, ok := serviceAccountSlugMap[member.Slug]; ok {
					memberSlug = mapped
				}
			}

			e.checkpoint.SetItemStatus(phase, itemKey, StatusInProgress, "")
			if !e.devMode {
				err := e.target.AddProjectMember(ctx, pd.Project.Slug, member.Type, memberSlug, member.Role.Identifier, member.Environments)
				if err != nil && !isAlreadyExistsError(err) {
					if isAdminAccessError(err) {
						e.checkpoint.SetItemStatus(phase, itemKey, StatusSkipped, "user has admin access to all projects")
						log.Printf("[skip] project member %s in %s: user has admin access to all projects", memberSlug, pd.Project.Slug)
						continue
					}
					e.checkpoint.SetItemStatus(phase, itemKey, StatusFailed, err.Error())
					log.Printf("[error] add project member %s to %s: %v", memberSlug, pd.Project.Slug, err)
					continue
				}
			} else {
				log.Printf("[dev] skipping add project member %s to %s", member.Slug, pd.Project.Slug)
			}
			e.checkpoint.SetItemStatus(phase, itemKey, StatusCompleted, "")
		}
	}

	return e.migrateWorkplaceUserAccess(ctx, phase, maps)
}

// migrateWorkplaceUserAccess applies each user's workplace access level on the
// target. Users are provisioned out of band (SCIM or invite) and land at whatever
// default the instance gives them, so their role — which may be a custom workplace
// role this migration created — has to be set explicitly.
//
// This runs at the very end of the migration on purpose. Access changes are the one
// write that can revoke the migrating token's own permissions, so nothing else
// depends on the token by the time they happen. The token's own user is skipped
// outright, which is the case that would otherwise fail mid-loop and strand the
// remaining users.
func (e *Executor) migrateWorkplaceUserAccess(ctx context.Context, phase string, maps *identityMaps) error {
	users := e.data.WorkplaceUsers
	if e.data.DeferWorkplaceLevel {
		// A user's workplace role applies to the whole target, not to the
		// projects a partial run is trying out.
		return nil
	}
	if len(users) == 0 || e.devMode {
		if e.devMode && len(users) > 0 {
			log.Printf("[dev] skipping workplace user access for %d users", len(users))
		}
		return nil
	}

	// Identify the account running the migration so its own access is left alone.
	selfSlug := ""
	if info, err := e.target.GetTokenInfo(ctx); err != nil {
		log.Printf("[warn] could not identify the migrating token: %v", err)
	} else if info.Principal.Type == "workplace_user" {
		selfSlug = info.Principal.Slug
	}

	total := len(users)
	for i, user := range users {
		if err := e.checkPause(ctx); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		current := i + 1
		email := user.User.Email
		if email == "" {
			continue
		}

		itemKey := "user_access:" + email
		if e.checkpoint.GetItemStatus(phase, itemKey) == StatusCompleted ||
			e.checkpoint.GetItemStatus(phase, itemKey) == StatusSkipped {
			continue
		}

		if user.Access == "" {
			e.checkpoint.SetItemStatus(phase, itemKey, StatusSkipped, "no access level on source")
			continue
		}

		targetSlug, ok := maps.emailToTargetID[email]
		if !ok {
			e.checkpoint.SetItemStatus(phase, itemKey, StatusSkipped, "user not found on target")
			log.Printf("[skip] user access %s: not on target", email)
			continue
		}

		if targetSlug == selfSlug {
			e.checkpoint.SetItemStatus(phase, itemKey, StatusSkipped,
				"this is the account running the migration; set its access manually")
			log.Printf("[skip] user access %s: own account", email)
			continue
		}

		e.emit(MigrateProgress{
			Phase: 8, Name: phase, Resource: "user_access",
			Item: email, Status: "started", Current: current, Total: total,
		})

		e.checkpoint.SetItemStatus(phase, itemKey, StatusInProgress, "")
		if err := e.target.UpdateWorkplaceUserAccess(ctx, targetSlug, user.Access); err != nil {
			e.checkpoint.SetItemStatus(phase, itemKey, StatusFailed, err.Error())
			log.Printf("[error] set access for %s: %v", email, err)
			e.emit(MigrateProgress{
				Phase: 8, Name: phase, Resource: "user_access",
				Item: email, Status: "failed", ckPhase: phase, ckKey: itemKey, Error: err.Error(), err: err, Current: current, Total: total,
			})
			continue
		}

		e.checkpoint.SetItemStatus(phase, itemKey, StatusCompleted, "")
		e.emit(MigrateProgress{
			Phase: 8, Name: phase, Resource: "user_access",
			Item: email, Status: "completed", Current: current, Total: total,
		})
	}

	return nil
}

// isAdminAccessError checks if an API error indicates the user already has
// workplace-level admin access to all projects, making a project-level role
// assignment unnecessary.
func isAdminAccessError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "admin access to all projects")
}

// isAlreadyExistsError checks if an API error indicates the resource already exists.
func isAlreadyExistsError(err error) bool {
	if err == nil {
		return false
	}
	// Check for APIError with 409 status code directly
	var apiErr *doppler.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == 409 {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "already exists") ||
		strings.Contains(msg, "already taken") || // covers "is already taken"
		strings.Contains(msg, "already been taken") // covers "has already been taken" (Rails convention)
}

// migrateChangeRequestPolicies creates workplace change request policies on the
// target, matched by name for idempotency.
//
// Policy rules name their reviewers by slug, and every slug changes between
// instances, so each subject is translated through the identity maps. A policy with
// a reviewer that does not exist on the target is skipped rather than created
// without them — silently dropping a required reviewer would weaken the policy while
// reporting success.
func (e *Executor) phase9ChangeRequestPolicies(ctx context.Context) error {
	phase := "change_request_policies"
	policies := e.data.ChangeRequestPolicies
	if len(policies) == 0 {
		return nil
	}

	var maps *identityMaps
	if !e.devMode {
		m, err := e.getIdentityMaps(ctx)
		if err != nil {
			// Without the maps no subject can be resolved. Record every policy as
			// failed and let the phase finish so the failures are reported together.
			for _, p := range policies {
				e.checkpoint.SetItemStatus(phase, "change_request_policy:"+p.Name, StatusFailed,
					"could not resolve reviewers: "+err.Error())
			}
			log.Printf("[error] change request policies: %v", err)
			return nil
		}
		maps = m
	}

	// Name-match idempotency against whatever is already on the target.
	existingNames := map[string]bool{}
	if !e.devMode {
		existing, err := e.target.ListChangeRequestPolicies(ctx)
		if err != nil {
			// Non-fatal: proceed without the check and let creates fail individually.
			log.Printf("[warn] could not list target change request policies: %v", err)
		} else {
			for _, p := range existing {
				existingNames[p.Name] = true
			}
		}
	}

	total := len(policies)
	for i, policy := range policies {
		if err := e.checkPause(ctx); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		current := i + 1
		itemKey := "change_request_policy:" + policy.Name
		if e.checkpoint.GetItemStatus(phase, itemKey) == StatusCompleted {
			continue
		}

		e.emit(MigrateProgress{
			Phase: 9, Name: phase, Resource: "change_request_policies",
			Item: policy.Name, Status: "started", Current: current, Total: total,
		})

		if existingNames[policy.Name] {
			e.checkpoint.SetItemStatus(phase, itemKey, StatusSkipped, "already exists")
			e.emit(MigrateProgress{
				Phase: 9, Name: phase, Resource: "change_request_policies",
				Item: policy.Name, Status: "skipped", Current: current, Total: total,
			})
			continue
		}

		e.checkpoint.SetItemStatus(phase, itemKey, StatusInProgress, "")

		if e.devMode {
			log.Printf("[dev] skipping create change request policy %q", policy.Name)
			e.checkpoint.SetItemStatus(phase, itemKey, StatusCompleted, "")
			e.emit(MigrateProgress{
				Phase: 9, Name: phase, Resource: "change_request_policies",
				Item: policy.Name, Status: "completed", Current: current, Total: total,
			})
			continue
		}

		remapped, unresolved := remapPolicySubjects(policy, maps)
		if len(unresolved) > 0 {
			reason := "reviewers not found on target: " + strings.Join(unresolved, ", ")
			e.checkpoint.SetItemStatus(phase, itemKey, StatusSkipped, reason)
			log.Printf("[skip] change request policy %q: %s", policy.Name, reason)
			e.emit(MigrateProgress{
				Phase: 9, Name: phase, Resource: "change_request_policies",
				Item: policy.Name, Status: "skipped", Current: current, Total: total,
			})
			continue
		}

		if _, err := e.target.CreateChangeRequestPolicy(ctx, &remapped); err != nil {
			if isAlreadyExistsError(err) {
				e.checkpoint.SetItemStatus(phase, itemKey, StatusSkipped, "already exists")
				e.emit(MigrateProgress{
					Phase: 9, Name: phase, Resource: "change_request_policies",
					Item: policy.Name, Status: "skipped", Current: current, Total: total,
				})
				continue
			}
			e.checkpoint.SetItemStatus(phase, itemKey, StatusFailed, err.Error())
			log.Printf("[error] create change request policy %q: %v", policy.Name, err)
			e.emit(MigrateProgress{
				Phase: 9, Name: phase, Resource: "change_request_policies",
				Item: policy.Name, Status: "failed", ckPhase: phase, ckKey: itemKey, Error: err.Error(), err: err, Current: current, Total: total,
			})
			continue
		}

		e.checkpoint.SetItemStatus(phase, itemKey, StatusCompleted, "")
		e.emit(MigrateProgress{
			Phase: 9, Name: phase, Resource: "change_request_policies",
			Item: policy.Name, Status: "completed", Current: current, Total: total,
		})
	}

	return nil
}

// remapPolicySubjects rewrites a policy's rule subjects from source slugs to target
// slugs. It returns the rewritten policy and a list of subjects that could not be
// resolved, described well enough to act on.
//
// Targets are left as-is: projects, environments and configs keep their slugs and
// names through migration, so they carry across directly.
func remapPolicySubjects(policy doppler.ChangeRequestPolicy, maps *identityMaps) (doppler.ChangeRequestPolicy, []string) {
	var unresolved []string

	rules := make([]doppler.ChangeRequestPolicyRule, 0, len(policy.Rules))
	for _, rule := range policy.Rules {
		if rule.Type != doppler.RuleRequiredReviewer || len(rule.Subjects) == 0 {
			rules = append(rules, rule)
			continue
		}

		subjects := make([]doppler.ChangeRequestPolicySubject, 0, len(rule.Subjects))
		for _, subject := range rule.Subjects {
			label := subject.Name
			if label == "" {
				label = subject.Slug
			}

			targetSlug, ok := resolveSubjectSlug(subject, maps)
			if !ok {
				unresolved = append(unresolved, fmt.Sprintf("%s %q", subject.Type, label))
				continue
			}
			subjects = append(subjects, doppler.ChangeRequestPolicySubject{
				Type: subject.Type,
				Slug: targetSlug,
			})
		}

		rule.Subjects = subjects
		rules = append(rules, rule)
	}

	policy.ID = ""
	policy.Rules = rules
	return policy, unresolved
}

// resolveSubjectSlug maps one subject's source slug to its target slug.
func resolveSubjectSlug(subject doppler.ChangeRequestPolicySubject, maps *identityMaps) (string, bool) {
	switch subject.Type {
	case doppler.SubjectWorkplaceUser:
		email := maps.email(subject.Slug)
		if !maps.targetEmailSet[email] {
			return "", false
		}
		if id, ok := maps.emailToTargetID[email]; ok {
			return id, true
		}
		return subject.Slug, true
	case doppler.SubjectGroup:
		if slug, ok := maps.groupSlugs[subject.Slug]; ok {
			return slug, true
		}
		return "", false
	case doppler.SubjectServiceAccount:
		if slug, ok := maps.serviceAccountSlugs[subject.Slug]; ok {
			return slug, true
		}
		return "", false
	default:
		// Unknown subject type — pass through rather than dropping the policy.
		return subject.Slug, true
	}
}

// setInheritanceRelationships wires up which configs each config inherits from.
//
// This runs after every inheritable flag is set, because a config can only be
// inherited from once it is marked inheritable. References carry across unchanged —
// they name a project slug and config name, both of which survive migration — but a
// reference to something outside the migration is skipped rather than sent, since
// the target would reject it with a less useful error.
func (e *Executor) setInheritanceRelationships(ctx context.Context) error {
	phase := "config_inheritance"

	// Every config being migrated, for validating references.
	known := make(map[string]bool)
	for _, pd := range e.data.Projects {
		for _, cd := range pd.Configs {
			known[pd.Project.Slug+"/"+cd.Config.Name] = true
		}
	}

	total := 0
	for _, pd := range e.data.Projects {
		for _, cd := range pd.Configs {
			if len(cd.Config.Inherits) > 0 {
				total++
			}
		}
	}
	if total == 0 {
		return nil
	}

	current := 0
	for _, pd := range e.data.Projects {
		for _, cd := range pd.Configs {
			if len(cd.Config.Inherits) == 0 {
				continue
			}

			if err := e.checkPause(ctx); err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}

			current++
			label := fmt.Sprintf("%s/%s", pd.Project.Slug, cd.Config.Name)
			itemKey := "inherits:" + label
			if e.checkpoint.GetItemStatus(phase, itemKey) == StatusCompleted {
				continue
			}

			e.emit(MigrateProgress{
				Phase: 6, Name: phase, Resource: "config_inherits",
				Item: label, Status: "started", Current: current, Total: total,
			})

			var missing []string
			for _, ref := range cd.Config.Inherits {
				if !known[ref.Project+"/"+ref.Config] {
					missing = append(missing, ref.Project+"/"+ref.Config)
				}
			}
			if len(missing) > 0 {
				reason := "inherits configs outside this migration: " + strings.Join(missing, ", ")
				e.checkpoint.SetItemStatus(phase, itemKey, StatusSkipped, reason)
				log.Printf("[skip] config inheritance %s: %s", label, reason)
				e.emit(MigrateProgress{
					Phase: 6, Name: phase, Resource: "config_inherits",
					Item: label, Status: "skipped", Current: current, Total: total,
				})
				continue
			}

			e.checkpoint.SetItemStatus(phase, itemKey, StatusInProgress, "")
			if !e.devMode {
				if err := e.target.SetConfigInherits(ctx, pd.Project.Slug, cd.Config.Name, cd.Config.Inherits); err != nil {
					e.checkpoint.SetItemStatus(phase, itemKey, StatusFailed, err.Error())
					log.Printf("[error] set inheritance for %s: %v", label, err)
					e.emit(MigrateProgress{
						Phase: 6, Name: phase, Resource: "config_inherits",
						Item: label, Status: "failed", ckPhase: phase, ckKey: itemKey, Error: err.Error(), err: err, Current: current, Total: total,
					})
					continue
				}
			} else {
				log.Printf("[dev] skipping set inheritance for %s", label)
			}

			e.checkpoint.SetItemStatus(phase, itemKey, StatusCompleted, "")
			e.emit(MigrateProgress{
				Phase: 6, Name: phase, Resource: "config_inherits",
				Item: label, Status: "completed", Current: current, Total: total,
			})
		}
	}

	return nil
}

// allowAllCIDR is the trusted IP entry a config is created with. A config whose
// source list does not contain it must have it removed, or the migrated
// restrictions sit alongside a rule that still permits everything.
const allowAllCIDR = "0.0.0.0/0"

// phase9TrustedIPs applies each config's trusted IP restrictions.
//
// This runs last, and that ordering is load-bearing. Restricting a config's trusted
// IPs takes effect immediately, and the machine running the migration is almost
// never inside the customer's allowed ranges — so once a config is restricted, the
// migrator's own subsequent calls to it are rejected. Every other phase that touches
// a config (secrets, locks, inheritable, inherits) has finished by now.
func (e *Executor) phase10TrustedIPs(ctx context.Context) error {
	phase := "trusted_ips"

	total := 0
	for _, pd := range e.data.Projects {
		for _, cd := range pd.Configs {
			if len(cd.TrustedIPs) > 0 {
				total++
			}
		}
	}
	if total == 0 {
		return nil
	}

	current := 0
	for _, pd := range e.data.Projects {
		for _, cd := range pd.Configs {
			if len(cd.TrustedIPs) == 0 {
				// No restrictions on the source: leave the target's default alone.
				continue
			}

			if err := e.checkPause(ctx); err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}

			current++
			label := fmt.Sprintf("%s/%s", pd.Project.Slug, cd.Config.Name)
			itemKey := "trusted_ips:" + label
			if e.checkpoint.GetItemStatus(phase, itemKey) == StatusCompleted {
				continue
			}

			e.emit(MigrateProgress{
				Phase: 10, Name: phase, Resource: "trusted_ips",
				Item: label, Status: "started", Current: current, Total: total,
			})

			e.checkpoint.SetItemStatus(phase, itemKey, StatusInProgress, "")

			if e.devMode {
				log.Printf("[dev] skipping trusted IPs for %s", label)
				e.checkpoint.SetItemStatus(phase, itemKey, StatusCompleted, "")
				e.emit(MigrateProgress{
					Phase: 10, Name: phase, Resource: "trusted_ips",
					Item: label, Status: "completed", Current: current, Total: total,
				})
				continue
			}

			// Add the source's ranges first, so the config is never left with no way in.
			sourceHasAllowAll := false
			failed := false
			for _, ip := range cd.TrustedIPs {
				if ip.IP == allowAllCIDR {
					sourceHasAllowAll = true
				}
				if err := e.target.AddTrustedIP(ctx, pd.Project.Slug, cd.Config.Name, ip.IP); err != nil {
					if isAlreadyExistsError(err) {
						continue
					}
					e.checkpoint.SetItemStatus(phase, itemKey, StatusFailed, err.Error())
					log.Printf("[error] add trusted IP %s for %s: %v", ip.IP, label, err)
					failed = true
					break
				}
			}
			if failed {
				e.emit(MigrateProgress{
					Phase: 10, Name: phase, Resource: "trusted_ips",
					Item: label, Status: "failed", ckPhase: phase, ckKey: itemKey, Current: current, Total: total,
				})
				continue
			}

			// Then drop the allow-all default the target created the config with.
			// Skipping this leaves the config open while looking migrated.
			if !sourceHasAllowAll {
				if err := e.target.RemoveTrustedIP(ctx, pd.Project.Slug, cd.Config.Name, allowAllCIDR); err != nil {
					// A 404 means it was already gone, which is the desired state.
					var apiErr *doppler.APIError
					if !errors.As(err, &apiErr) || apiErr.StatusCode != 404 {
						reason := fmt.Sprintf("added source ranges but could not remove %s: %v", allowAllCIDR, err)
						e.checkpoint.SetItemStatus(phase, itemKey, StatusFailed, reason)
						log.Printf("[error] %s: %s", label, reason)
						e.emit(MigrateProgress{
							Phase: 10, Name: phase, Resource: "trusted_ips",
							Item: label, Status: "failed", ckPhase: phase, ckKey: itemKey, Error: reason, err: err, Current: current, Total: total,
						})
						continue
					}
				}
			}

			e.checkpoint.SetItemStatus(phase, itemKey, StatusCompleted, "")
			e.emit(MigrateProgress{
				Phase: 10, Name: phase, Resource: "trusted_ips",
				Item: label, Status: "completed", Current: current, Total: total,
			})
		}
	}

	return nil
}

// idleReason says why a phase had nothing to do, or "" if it had work. A
// partial run leaves the workplace-wide pieces for the full one, and says so,
// since "nothing to migrate" would be untrue there: there is, just not yet.
func (e *Executor) idleReason(phase string) string {
	d := e.data
	// A phase with nothing because the source token could not read it says
	// that, rather than "nothing to migrate", which would be untrue.
	refused := map[string]bool{}
	for _, u := range d.Unavailable {
		refused[u.Resource] = true
	}
	const notRead = "Not readable with the source token"
	deferred := "Left for the full migration"
	const none = "Nothing to migrate"

	configs := 0
	inheritance := false
	webhooks := 0
	projectMembers := 0
	for _, pd := range d.Projects {
		configs += len(pd.Configs)
		webhooks += len(pd.Webhooks)
		projectMembers += len(pd.Members)
		for _, cd := range pd.Configs {
			if cd.Config.Inheritable || len(cd.Config.Inherits) > 0 {
				inheritance = true
			}
		}
	}
	groupMembers := 0
	for _, g := range d.Groups {
		groupMembers += len(g.Members)
	}

	switch phase {
	case "workplace_settings_and_roles":
		if len(d.WorkplaceRoles)+len(d.ProjectRoles) > 0 {
			return ""
		}
		if d.DeferWorkplaceLevel {
			return deferred
		}
		if d.Workplace == nil {
			return none
		}
	case "groups_and_service_accounts":
		if len(d.Groups)+len(d.ServiceAccounts) == 0 {
			return none
		}
	case "projects_and_environments":
		if len(d.Projects) == 0 {
			return none
		}
	case "configs", "secrets_and_locks", "trusted_ips":
		if configs == 0 {
			return none
		}
	case "config_inheritance":
		if !inheritance {
			return none
		}
	case "webhooks":
		if webhooks == 0 {
			if refused[ResourceWebhooks] {
				return notRead
			}
			return none
		}
	case "members":
		// Workplace user access levels are the part a partial run leaves.
		if d.DeferWorkplaceLevel && groupMembers+projectMembers == 0 {
			return deferred
		}
	case "change_request_policies":
		if len(d.ChangeRequestPolicies) > 0 {
			return ""
		}
		if d.DeferWorkplaceLevel {
			return deferred
		}
		if refused[ResourceChangeRequestPolicies] {
			return notRead
		}
		return none
	}
	return ""
}
