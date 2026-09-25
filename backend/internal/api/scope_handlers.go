package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"path/filepath"
	"sort"
	"time"

	"github.com/doppleruniversity/workplace-migrator/internal/doppler"
	"github.com/doppleruniversity/workplace-migrator/internal/migration"
)

// Migration scope: full or partial, and what a run over it will do.
//
// The server holds the choice rather than the browser, because four things
// read it — the plan, the comparison, the run and the cutover gate — and a
// choice held in one page's state is exactly how the Review page's options
// used to vanish before they reached the run.

const scopeHistoryFileName = "scope-history.json"

func scopeHistoryPath() string {
	return filepath.Join(filepath.Dir(checkpointPath()), scopeHistoryFileName)
}

// history loads the run history for the configured source and target. A
// history that cannot be read is reported empty: the cost is a missed "already
// migrated" hint, where refusing would strand the whole flow.
func (s *Server) history() *migration.ScopeHistory {
	s.mu.RLock()
	cfg := s.config
	s.mu.RUnlock()

	src, tgt := "", ""
	if cfg != nil {
		src, tgt = cfg.SourceHost, cfg.TargetHost
	}
	h, err := migration.LoadScopeHistory(scopeHistoryPath(), src, tgt)
	if err != nil {
		log.Printf("[warn] scope history unreadable, treating as empty: %v", err)
		h, _ = migration.LoadScopeHistory("", src, tgt)
	}
	return h
}

// currentScope is the stored choice, full when none has been made.
func (s *Server) currentScope() migration.Scope {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.scope == nil {
		return migration.Scope{Mode: migration.ScopeFull}
	}
	return *s.scope
}

// runPlan is how a run over the current scope splits into its two stages.
type runPlan struct {
	Resolution *migration.ScopeResolution `json:"resolution"`

	// Remaining are the in-scope projects no earlier run has covered. The
	// migration engine creates them, and everything else new.
	Remaining []string `json:"remaining"`

	// PreviouslyMigrated are in-scope projects an earlier run already moved.
	// The engine steps around them — it creates what is missing and never
	// updates, so re-running it would keep their old values and create every
	// webhook a second time — and the top-up brings them up to date instead.
	PreviouslyMigrated   []string   `json:"previously_migrated"`
	PreviouslyMigratedAt *time.Time `json:"previously_migrated_at,omitempty"`

	data   *migration.MigrationData
	scoped *migration.MigrationData
}

// planRun resolves the current scope against gathered data and history.
func (s *Server) planRun() (*runPlan, error) {
	s.mu.RLock()
	data := s.migrationData
	s.mu.RUnlock()
	if data == nil {
		return nil, errNotGathered
	}

	scope := s.currentScope()
	hist := s.history()

	var carried []string
	if scope.IsPartial() {
		for slug := range hist.Migrated() {
			carried = append(carried, slug)
		}
	}

	res, err := migration.ResolveScope(data, scope, carried)
	if err != nil {
		return nil, err
	}

	p := &runPlan{Resolution: res, data: data, scoped: migration.ApplyScope(data, res)}
	p.PreviouslyMigrated = hist.MigratedAmong(res.Projects)
	done := map[string]bool{}
	for _, slug := range p.PreviouslyMigrated {
		done[slug] = true
	}
	for _, slug := range res.Projects {
		if !done[slug] {
			p.Remaining = append(p.Remaining, slug)
		}
	}
	if lp := hist.LastPartial(); lp != nil && len(p.PreviouslyMigrated) > 0 {
		at := lp.At
		p.PreviouslyMigratedAt = &at
	}
	return p, nil
}

// scopedData is the gathered data narrowed to the current scope, or the whole
// of it when the scope cannot be resolved (which only happens before a gather).
func (s *Server) scopedData() *migration.MigrationData {
	if p, err := s.planRun(); err == nil {
		return p.scoped
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.migrationData
}

type errString string

func (e errString) Error() string { return string(e) }

const errNotGathered = errString("Nothing gathered yet. Call /api/v1/gather first.")

// scopeProject is one row of the picker.
type scopeProject struct {
	Slug       string     `json:"slug"`
	Name       string     `json:"name"`
	Configs    int        `json:"configs"`
	Secrets    int        `json:"secrets"`
	MigratedAt *time.Time `json:"migrated_at,omitempty"`
}

type scopeResponse struct {
	Scope    migration.Scope `json:"scope"`
	Run      *runPlan        `json:"run"`
	Projects []scopeProject  `json:"projects"`

	// CutoverBlocked is set while the target holds only part of the source.
	CutoverBlocked bool `json:"cutover_blocked"`

	UserCheck *migration.UserCheckResult `json:"user_check,omitempty"`

	// TopUp is set when the next full-scope run is a top-up: a full migration
	// has finished and cutover has not. Number is the top-up this would be;
	// Since is when the target was last brought up to date.
	TopUp *topUpInfo `json:"top_up,omitempty"`
}

type topUpInfo struct {
	Number int        `json:"number"`
	Since  *time.Time `json:"since,omitempty"`
}

func (s *Server) buildScopeResponse() (*scopeResponse, error) {
	p, err := s.planRun()
	if err != nil {
		return nil, err
	}
	hist := s.history()
	migrated := hist.Migrated()

	out := &scopeResponse{Scope: s.currentScope(), Run: p, CutoverBlocked: hist.CutoverBlocked(), Projects: []scopeProject{}}
	if out.Scope.Mode == migration.ScopeFull && hist.FullDone() && hist.CutoverAt == nil {
		info := &topUpInfo{Number: hist.TopUps() + 1}
		if last := hist.LastRun(); last != nil {
			at := last.At
			info.Since = &at
		}
		out.TopUp = info
	}
	for _, pd := range p.data.Projects {
		row := scopeProject{Slug: pd.Project.Slug, Name: pd.Project.Name, Configs: len(pd.Configs)}
		if row.Name == "" {
			row.Name = row.Slug
		}
		for _, cd := range pd.Configs {
			row.Secrets += cd.SecretCount
		}
		if at, ok := migrated[row.Slug]; ok {
			at := at
			row.MigratedAt = &at
		}
		out.Projects = append(out.Projects, row)
	}
	sort.Slice(out.Projects, func(i, j int) bool { return out.Projects[i].Name < out.Projects[j].Name })

	s.mu.RLock()
	if s.plan != nil {
		uc := s.plan.UserCheck
		out.UserCheck = &uc
	}
	s.mu.RUnlock()
	return out, nil
}

// HandleGetScope returns the current choice and what it resolves to.
func (s *Server) HandleGetScope() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		out, err := s.buildScopeResponse()
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, out)
	}
}

// HandleSetScope stores a choice, and rebuilds the plan for it.
func (s *Server) HandleSetScope() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req migration.Scope
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, http.StatusBadRequest, "Could not read the request: "+err.Error())
			return
		}
		if req.Mode != migration.ScopePartial {
			req = migration.Scope{Mode: migration.ScopeFull}
		}

		s.mu.RLock()
		data := s.migrationData
		cfg := s.config
		busy := s.migrating || s.comparing || s.converging
		s.mu.RUnlock()

		switch {
		case data == nil:
			jsonError(w, http.StatusBadRequest, errNotGathered.Error())
			return
		case busy:
			// The run, the comparison and the plan all read the scope. Changing
			// it underneath one of them would leave it reporting on a scope that
			// no longer exists.
			jsonError(w, http.StatusConflict, "A run is in progress; the scope cannot change until it finishes")
			return
		}

		// Validate before storing, so a bad choice never becomes the current one.
		var carried []string
		if req.IsPartial() {
			for slug := range s.history().Migrated() {
				carried = append(carried, slug)
			}
		}
		// A full migration waiting to cut over is topped up over the whole
		// source. A partial run now would narrow the next run to a few projects
		// of a migration that is otherwise finished.
		if req.IsPartial() && s.history().InFlight() {
			jsonError(w, http.StatusConflict, "A full migration is waiting to cut over. Top-ups cover the whole source; cut over before starting a partial migration.")
			return
		}
		res, err := migration.ResolveScope(data, req, carried)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		scoped := migration.ApplyScope(data, res)

		plan := migration.GeneratePlan(scoped)
		if cfg != nil && !cfg.DevMode && cfg.TargetHost != "" {
			target := doppler.NewClient(cfg.TargetHost, cfg.TargetToken, doppler.WithRateLimiter(s.newLimiter("target")))
			if uc, err := migration.CheckUsers(r.Context(), scoped, target); err == nil {
				plan.UserCheck = *uc
			} else {
				log.Printf("[warn] user check for scope: %v", err)
			}
		}

		s.mu.Lock()
		s.scope = &req
		s.plan = plan
		// A comparison of a different scope is not this scope's comparison.
		s.diff = nil
		s.snapshot = nil
		s.forgetFinishedRunLocked()
		s.mu.Unlock()

		out, err := s.buildScopeResponse()
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, out)
	}
}

// HandlePreviewScope resolves a choice without storing it.
//
// The picker shows what a selection brings along as it changes. Storing on
// every tick would rebuild the plan and re-run the user check against the
// target each time, so the preview only resolves.
func (s *Server) HandlePreviewScope() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req migration.Scope
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, http.StatusBadRequest, "Could not read the request: "+err.Error())
			return
		}

		s.mu.RLock()
		data := s.migrationData
		s.mu.RUnlock()
		if data == nil {
			jsonError(w, http.StatusBadRequest, errNotGathered.Error())
			return
		}

		var carried []string
		if req.IsPartial() {
			for slug := range s.history().Migrated() {
				carried = append(carried, slug)
			}
		}
		res, err := migration.ResolveScope(data, req, carried)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]any{"resolution": res})
	}
}

// runOptionsRequest carries the Review page's choices to the run.
type runOptionsRequest struct {
	DeleteOrphans       bool   `json:"delete_orphans"`
	DeleteOrphanConfigs bool   `json:"delete_orphan_configs"`
	Drift               string `json:"drift"`
}

// HandleSetRunOptions stores the choices made on the Review page.
//
// They used to travel as navigation state to a page that never read them, and
// the run went ahead on defaults. Held here, they reach the run whichever way
// the browser gets there.
func (s *Server) HandleSetRunOptions() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req runOptionsRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, http.StatusBadRequest, "Could not read the request: "+err.Error())
			return
		}

		opts := migration.DefaultConvergeOptions()
		opts.DeleteOrphans = req.DeleteOrphans
		opts.DeleteOrphanConfigs = req.DeleteOrphanConfigs
		if req.Drift == string(migration.DriftKeepTarget) {
			opts.Drift = migration.DriftKeepTarget
		}

		s.mu.Lock()
		s.runOptions = &opts
		// Saved on the way from Review to Migrate, so the next run is about to
		// start: the Migrate page must not mistake the last one's result for it.
		s.forgetFinishedRunLocked()
		s.mu.Unlock()

		jsonResponse(w, http.StatusOK, map[string]any{"status": "saved"})
	}
}

// currentRunOptions is what the next run's top-up applies.
func (s *Server) currentRunOptions() migration.ConvergeOptions {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.runOptions == nil {
		return migration.DefaultConvergeOptions()
	}
	return *s.runOptions
}

// runInfo is what the summary reports about the last run.
type runInfo struct {
	Mode               migration.ScopeMode        `json:"mode"`
	Resolution         *migration.ScopeResolution `json:"resolution"`
	Remaining          int                        `json:"remaining"`
	PreviouslyMigrated []string                   `json:"previously_migrated,omitempty"`
	// PreviouslyMigratedAt is when the earlier partial ran, for the summary's
	// "brought the projects from the Sep 21 partial up to date".
	PreviouslyMigratedAt *time.Time `json:"previously_migrated_at,omitempty"`
	// Reintroduced are earlier projects no longer on the target, migrated
	// again in stage one rather than brought up to date in stage two.
	Reintroduced []string `json:"reintroduced,omitempty"`

	// Stage is where the run is: remaining, previously_migrated or done.
	Stage string                    `json:"stage"`
	TopUp *migration.ConvergeResult `json:"top_up,omitempty"`
	// TopUpError is set when the second stage could not run or did not finish.
	TopUpError string `json:"top_up_error,omitempty"`
}

func (s *Server) setRunStage(stage string) {
	s.mu.Lock()
	if s.lastRun != nil {
		s.lastRun.Stage = stage
	}
	s.mu.Unlock()
	s.SSE.Publish(SSEEvent{Type: "migration_stage", Data: map[string]any{"stage": stage}})
}

// topUp is the second stage of a run: bring what an earlier run moved into
// line with the source, writing only what differs.
//
// It reuses the converger, which updates where the migration engine only
// creates. Drift is taken from the comparison the person reviewed rather than
// recomputed, so the choice they made on the Review page is applied to the
// configs it was made about.
func (s *Server) topUp(
	ctx context.Context,
	target migration.DopplerReadWriter,
	p *runPlan,
	projects []string,
	opts migration.ConvergeOptions,
	errorStore *migration.ErrorStore,
) (*migration.ConvergeResult, error) {
	reader := migration.NewSnapshotReader(target, func(g migration.GatherProgress) {
		s.SSE.Publish(SSEEvent{Type: "compare_progress", Data: g})
	})
	snap, err := reader.Read(ctx)
	if err != nil {
		return nil, err
	}

	data := migration.WithProjects(p.scoped, projects, false)

	// These are exactly the projects an earlier run wrote — so, if that run was
	// in this session, the ones whose values were dropped when it wrote them.
	// Without this the top-up would update nothing, and a run asked to delete
	// what the source no longer has would delete every secret they hold.
	s.mu.RLock()
	cfg := s.config
	s.mu.RUnlock()
	if err := s.reloadSourceSecrets(ctx, cfg, data, projects); err != nil {
		return nil, err
	}

	diff := migration.ComputeDiff(data, migration.ScopeSnapshot(snap, projects, p.data))

	s.mu.RLock()
	reviewed := s.diff
	s.mu.RUnlock()
	if reviewed != nil {
		in := map[string]bool{}
		for _, slug := range projects {
			in[slug] = true
		}
		for _, d := range reviewed.Drift {
			if in[d.Project] {
				diff.Drift = append(diff.Drift, d)
			}
		}
	}

	converger := migration.NewConverger(target, data, diff, opts, errorStore, func(mp migration.MigrateProgress) {
		s.SSE.Publish(SSEEvent{Type: "converge_progress", Data: mp})
	})
	return converger.Run(ctx)
}

// reloadSourceSecrets re-reads source values dropped after an earlier write, so
// a diff sees what the source actually holds.
func (s *Server) reloadSourceSecrets(ctx context.Context, cfg *migration.MigrationConfig, data *migration.MigrationData, projects []string) error {
	if cfg == nil {
		return errString("nothing configured yet")
	}
	source := doppler.NewClient(cfg.SourceHost, cfg.SourceToken, doppler.WithRateLimiter(s.sharedLimiter("source")))
	n, err := migration.ReloadSecrets(ctx, source, data, projects)
	if err != nil {
		return err
	}
	if n > 0 {
		log.Printf("[info] re-read source secrets for %d configs migrated earlier this session", n)
	}
	return nil
}

// forgetFinishedRunLocked drops the result of a run that has finished, so the
// Migrate page starts the next run rather than showing the last one as done.
// A run still going is left alone. The executor stays, for triage's re-runs
// of the last run's failures; status stops reporting its phases once the
// result is gone. Callers hold s.mu.
func (s *Server) forgetFinishedRunLocked() {
	if s.migrating {
		return
	}
	s.migrationResult = nil
}
