package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/doppleruniversity/workplace-migrator/internal/doppler"
	"github.com/doppleruniversity/workplace-migrator/internal/migration"
)

// compareResponse is the Compare step: both sides read, and what differs.
type compareResponse struct {
	Status string           `json:"status"`
	Counts migration.Counts `json:"counts"`

	// TargetEmpty says this is a first migration, so the interface can present
	// it as one rather than as a diff in which everything happens to be new.
	TargetEmpty bool `json:"target_empty"`

	Differences []migration.Difference  `json:"differences"`
	Matched     map[string]int          `json:"matched"`
	Drift       []migration.DriftRecord `json:"drift,omitempty"`
	// DriftUnavailable is set when drift could not be determined. Distinct from
	// an empty Drift, which means it was checked and there was none.
	DriftUnavailable string `json:"drift_unavailable,omitempty"`

	Error string `json:"error,omitempty"`
}

// HandleCompare reads the target and diffs it against the gathered source.
func (s *Server) HandleCompare() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		cfg := s.config
		busy := s.comparing
		// A run clears secret values as it writes them, and a comparison reloads
		// them. Letting the two overlap would race on the same configs.
		running := s.migrating || s.converging || s.rerunning
		s.mu.Unlock()

		// The comparison covers the chosen scope, so Review's counts are the
		// counts for the projects this run will actually touch.
		rp, planErr := s.planRun()

		switch {
		case cfg == nil:
			jsonError(w, http.StatusBadRequest, "Configuration not set. Call /api/v1/configure first.")
			return
		case planErr != nil:
			jsonError(w, http.StatusBadRequest, planErr.Error())
			return
		case busy:
			jsonError(w, http.StatusConflict, "A comparison is already running")
			return
		case running:
			jsonError(w, http.StatusConflict, "A run is in progress; compare once it has finished")
			return
		case cfg.DevMode || cfg.TargetHost == "":
			// With no target there is nothing to compare against, so every
			// difference is a create — which is what a dry run should show.
			diff := migration.ComputeDiff(rp.scoped, nil)
			s.storeDiff(diff, nil)
			jsonResponse(w, http.StatusOK, buildCompareResponse(diff, true))
			return
		}

		s.mu.Lock()
		s.comparing = true
		s.mu.Unlock()

		jsonResponse(w, http.StatusAccepted, map[string]string{"status": "started"})

		go s.runCompare(cfg, rp)
	}
}

func (s *Server) runCompare(cfg *migration.MigrationConfig, rp *runPlan) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer func() {
		s.mu.Lock()
		s.comparing = false
		s.mu.Unlock()
	}()

	limiter := s.newLimiter("target")
	target := doppler.NewClient(cfg.TargetHost, cfg.TargetToken, doppler.WithRateLimiter(limiter))

	reader := migration.NewSnapshotReader(target, func(p migration.GatherProgress) {
		s.SSE.Publish(SSEEvent{Type: "compare_progress", Data: p})
	})

	snap, err := reader.Read(ctx)
	if err != nil {
		log.Printf("[error] compare: %v", err)
		s.SSE.Publish(SSEEvent{Type: "compare_complete", Data: map[string]any{
			"status": "failed", "error": err.Error(),
		}})
		return
	}

	// A config migrated earlier in this session has had its values dropped.
	// Diffing it as-is would call every secret on the target "removed from the
	// source", so its values are read again first.
	if err := s.reloadSourceSecrets(ctx, cfg, rp.scoped, rp.Resolution.Projects); err != nil {
		log.Printf("[error] compare: %v", err)
		s.SSE.Publish(SSEEvent{Type: "compare_complete", Data: map[string]any{
			"status": "failed", "error": err.Error(),
		}})
		return
	}

	// Narrowed with the data, or every source project outside the scope would
	// read as an orphan on the target.
	snap = migration.ScopeSnapshot(snap, rp.Resolution.Projects, rp.data)
	diff := migration.ComputeDiff(rp.scoped, snap)

	// Drift is read from the instance's own logs, against the configs an earlier
	// run recorded writing. A first migration has no earlier run and therefore
	// no drift to find, which is the empty case rather than an error.
	//
	// Every config the migrator has written, from the run history, and the
	// current checkpoint's for a run the history has not recorded yet — only
	// those still on the target, since a missing config has no log to read.
	var fromCheckpoint []migration.ConfigWrite
	if cp := s.checkpointData(); cp != nil {
		fromCheckpoint = migration.WritesFromCheckpoint(*cp)
	}
	writes := migration.WritesOnTarget(migration.MergeWrites(s.history().AllWrites(), fromCheckpoint), snap)
	if len(writes) > 0 {
		detector := migration.NewDriftDetector(target, s.selfIdentities(ctx, target)...)
		diff.Drift, diff.DriftUnavailable = detector.Detect(ctx, writes)
	}

	s.storeDiff(diff, snap)
	s.SSE.Publish(SSEEvent{Type: "compare_complete", Data: buildCompareResponse(diff, snap.IsEmpty())})
}

// selfIdentities are the names this migrator's own writes appear under. Without
// them every config it touched reads as drifted.
func (s *Server) selfIdentities(ctx context.Context, target *doppler.Client) []string {
	var ids []string
	if info, err := target.GetTokenInfo(ctx); err == nil {
		ids = append(ids, info.Name, info.Principal.Slug)
	}
	return ids
}

func buildCompareResponse(diff *migration.Diff, targetEmpty bool) compareResponse {
	matched := map[string]int{}
	for kind, n := range diff.Matched {
		matched[string(kind)] = n
	}
	differences := diff.Differences
	if differences == nil {
		differences = []migration.Difference{}
	}
	return compareResponse{
		Status:           "complete",
		Counts:           diff.Counts(),
		TargetEmpty:      targetEmpty,
		Differences:      differences,
		Matched:          matched,
		Drift:            diff.Drift,
		DriftUnavailable: diff.DriftUnavailable,
	}
}

func (s *Server) storeDiff(diff *migration.Diff, snap *migration.TargetSnapshot) {
	s.mu.Lock()
	s.diff = diff
	s.snapshot = snap
	s.mu.Unlock()
}

// HandleCompareStatus is the fallback for a client that missed the SSE event.
func (s *Server) HandleCompareStatus() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.RLock()
		comparing := s.comparing
		diff := s.diff
		snap := s.snapshot
		s.mu.RUnlock()

		if comparing {
			jsonResponse(w, http.StatusOK, map[string]any{"status": "running"})
			return
		}
		if diff == nil {
			jsonResponse(w, http.StatusOK, map[string]any{"status": "pending"})
			return
		}
		jsonResponse(w, http.StatusOK, buildCompareResponse(diff, snap == nil || snap.IsEmpty()))
	}
}

// convergeRequest starts a run against the computed diff.
type convergeRequest struct {
	Mode                string `json:"mode"`
	DeleteOrphans       bool   `json:"delete_orphans"`
	DeleteOrphanConfigs bool   `json:"delete_orphan_configs"`
	Drift               string `json:"drift"`
}

// HandleConverge applies the diff to the target.
func (s *Server) HandleConverge() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req convergeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err.Error() != "EOF" {
			jsonError(w, http.StatusBadRequest, "Could not read the request: "+err.Error())
			return
		}

		s.mu.Lock()
		cfg := s.config
		data := s.migrationData
		diff := s.diff
		busy := s.migrating || s.rerunning || s.converging
		s.mu.Unlock()

		switch {
		case cfg == nil || data == nil:
			jsonError(w, http.StatusBadRequest, "Nothing gathered yet")
			return
		case diff == nil:
			jsonError(w, http.StatusBadRequest, "No comparison has run. Call /api/v1/compare first.")
			return
		case busy:
			jsonError(w, http.StatusConflict, "A run is already in progress")
			return
		}

		opts := migration.DefaultConvergeOptions()
		if req.Mode == string(migration.ModeCutOver) {
			opts.Mode = migration.ModeCutOver
		}
		opts.DeleteOrphans = req.DeleteOrphans
		opts.DeleteOrphanConfigs = req.DeleteOrphanConfigs
		if req.Drift == string(migration.DriftKeepTarget) {
			opts.Drift = migration.DriftKeepTarget
		}

		s.mu.Lock()
		s.converging = true
		if s.errors == nil {
			s.errors = migration.NewErrorStore()
		}
		errorStore := s.errors
		s.mu.Unlock()

		jsonResponse(w, http.StatusAccepted, map[string]any{
			"status": "started",
			"counts": diff.Counts(),
		})

		go s.runConverge(cfg, data, diff, opts, errorStore)
	}
}

func (s *Server) runConverge(
	cfg *migration.MigrationConfig,
	data *migration.MigrationData,
	diff *migration.Diff,
	opts migration.ConvergeOptions,
	errorStore *migration.ErrorStore,
) {
	ctx, cancel := context.WithCancel(context.Background())

	s.mu.Lock()
	s.migrationCancel = cancel
	s.mu.Unlock()

	defer func() {
		cancel()
		s.mu.Lock()
		s.converging = false
		s.migrationCancel = nil
		s.mu.Unlock()
	}()

	limiter := s.newLimiter("target")
	target := doppler.NewClient(cfg.TargetHost, cfg.TargetToken, doppler.WithRateLimiter(limiter))

	converger := migration.NewConverger(target, data, diff, opts, errorStore, func(p migration.MigrateProgress) {
		s.SSE.Publish(SSEEvent{Type: "converge_progress", Data: p})
	})

	result, err := converger.Run(ctx)

	payload := map[string]any{"status": "completed", "result": result}
	if err != nil {
		payload["status"] = "failed"
		payload["error"] = err.Error()
	}
	s.SSE.Publish(SSEEvent{Type: "converge_complete", Data: payload})
	s.SSE.Publish(SSEEvent{Type: "errors_changed", Data: map[string]any{}})

	s.mu.Lock()
	s.convergeResult = result
	dev := s.config != nil && s.config.DevMode
	s.mu.Unlock()

	if result != nil && !dev {
		if herr := s.history().RecordWrites(result.Writes); herr != nil {
			log.Printf("[error] could not record the configs this run wrote: %v", herr)
		}
	}
}

// cutoverRequest is the final step.
type cutoverRequest struct {
	// MigratorCIDR keeps this migrator's own address trusted for the duration
	// and removes it at the end. Empty applies the ranges as they are.
	MigratorCIDR string `json:"migrator_cidr"`
	// DryRun reports the plan without doing any of it.
	DryRun bool `json:"dry_run"`
}

// HandleCutover plans or runs the cutover.
func (s *Server) HandleCutover() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req cutoverRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err.Error() != "EOF" {
			jsonError(w, http.StatusBadRequest, "Could not read the request: "+err.Error())
			return
		}

		s.mu.Lock()
		cfg := s.config
		data := s.migrationData
		snap := s.snapshot
		busy := s.migrating || s.rerunning || s.converging
		s.mu.Unlock()

		switch {
		case cfg == nil || data == nil:
			jsonError(w, http.StatusBadRequest, "Nothing gathered yet")
			return
		case busy:
			jsonError(w, http.StatusConflict, "A run is already in progress")
			return
		}

		cidr := strings.TrimSpace(req.MigratorCIDR)
		if cidr != "" {
			if err := validateCIDR(cidr); err != nil {
				jsonError(w, http.StatusBadRequest, err.Error())
				return
			}
		}

		// Read fresh: the snapshot is from Compare, and a migration has run
		// since. Planned from it, every project the migration created reads as
		// having no ranges, and the plan offers to add the default it already has.
		// It is all the plan reads from the target, so no comparison is needed
		// first — which matters while waiting to cut over, when the last one
		// may be days old or gone with a restart.
		if !cfg.DevMode && cfg.TargetHost != "" {
			target := doppler.NewClient(cfg.TargetHost, cfg.TargetToken, doppler.WithRateLimiter(s.sharedLimiter("target")))
			snap = migration.RefreshTrustedIPs(r.Context(), target, snap, data)
		} else if snap == nil {
			snap = &migration.TargetSnapshot{Projects: map[string]migration.ProjectSnapshot{}}
		}

		plan := migration.PlanCutover(data, snap, cidr)
		s.mu.Lock()
		s.cutoverPlanned = plan
		s.mu.Unlock()

		// Cutover removes this migrator's write access to the configs it closes,
		// and issuing a service token needs exactly that access. Once this has
		// run there is no way back — a second cutover finds the ranges already
		// mirrored and does nothing — so an undecided token is a decision that
		// can never be made again. The dry run reports the gate; the real one
		// refuses to pass it.
		ledger, ledgerErr := s.ledger()
		var gate tokenGate
		if ledgerErr == nil {
			gate = tokenGateFor(ledger)
		}

		// After a partial run the target holds only part of the source. Cutover
		// would mirror trusted IPs across a workplace that is mostly not there
		// yet, so it waits for a full run.
		partialOnly := s.history().CutoverBlocked()

		if req.DryRun {
			jsonResponse(w, http.StatusOK, map[string]any{
				"status": "planned", "plan": plan, "token_gate": gate, "partial_only": partialOnly,
			})
			return
		}

		if partialOnly {
			jsonError(w, http.StatusConflict,
				"The target holds only the projects a partial migration moved. Run a full migration before cutting over.")
			return
		}

		if gate.Blocked {
			jsonError(w, http.StatusConflict, gate.Reason+
				". Issuing a token needs the config write access this step removes, and it cannot be restored afterwards — issue or skip them first.")
			return
		}

		s.mu.Lock()
		s.converging = true
		if s.errors == nil {
			s.errors = migration.NewErrorStore()
		}
		errorStore := s.errors
		s.mu.Unlock()

		jsonResponse(w, http.StatusAccepted, map[string]any{"status": "started", "plan": plan})

		go s.runCutover(cfg, plan, errorStore)
	}
}

func (s *Server) runCutover(cfg *migration.MigrationConfig, plan *migration.CutoverPlan, errorStore *migration.ErrorStore) {
	ctx, cancel := context.WithCancel(context.Background())

	s.mu.Lock()
	s.migrationCancel = cancel
	s.mu.Unlock()

	defer func() {
		cancel()
		s.mu.Lock()
		s.converging = false
		s.migrationCancel = nil
		s.mu.Unlock()
	}()

	limiter := s.newLimiter("target")
	target := doppler.NewClient(cfg.TargetHost, cfg.TargetToken, doppler.WithRateLimiter(limiter))

	cutover := migration.NewCutover(target, plan, errorStore, func(p migration.MigrateProgress) {
		s.SSE.Publish(SSEEvent{Type: "cutover_progress", Data: p})
	})

	result, err := cutover.Run(ctx)

	payload := map[string]any{"status": "completed", "result": result}
	if err != nil {
		payload["status"] = "failed"
		payload["error"] = err.Error()
	}
	// A cutover that could not remove its own address has not finished, however
	// well the rest of it went. Saying so here is what stops the summary
	// reporting a migration as complete while a hole is still open in it.
	if result != nil && !result.Complete() {
		payload["status"] = "incomplete"
	}

	s.mu.Lock()
	s.cutoverResult = result
	dev := s.config != nil && s.config.DevMode
	s.mu.Unlock()

	// Finished for good: nothing to wait for, and the waiting page gives way
	// to the summary. Only a cutover that left nothing behind counts.
	if err == nil && result != nil && result.Complete() && !dev {
		if herr := s.history().RecordCutover(time.Now().UTC()); herr != nil {
			log.Printf("[error] could not record the cutover: %v", herr)
		}
	}

	s.SSE.Publish(SSEEvent{Type: "cutover_complete", Data: payload})
	s.SSE.Publish(SSEEvent{Type: "errors_changed", Data: map[string]any{}})
}

// checkpointData returns the checkpoint of an earlier run, if there is one.
func (s *Server) checkpointData() *migration.Checkpoint {
	s.mu.RLock()
	cm := s.checkpoint
	s.mu.RUnlock()

	if cm == nil {
		cm = migration.NewCheckpointManager(s.checkpointPath)
		if err := cm.Load(); err != nil {
			return nil
		}
	}
	data := cm.Data()
	return &data
}
