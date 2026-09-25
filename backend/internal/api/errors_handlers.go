package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/doppler/workplace-migrator/internal/doppler"
	"github.com/doppler/workplace-migrator/internal/migration"
)

// apiRoot normalizes a host the way the Doppler client does, so a failure's URL
// can be matched back to the instance it went to.
func apiRoot(host string) string {
	host = strings.TrimRight(strings.TrimSpace(host), "/")
	if host == "" {
		return ""
	}
	if !strings.HasSuffix(host, "/v3") {
		host += "/v3"
	}
	return host
}

// errorsResponse is the triage overview: every class, worst first.
type errorsResponse struct {
	Classes    []migration.ClassSummary `json:"classes"`
	Total      int                      `json:"total"`
	Open       int                      `json:"open"`
	ClassCount int                      `json:"class_count"`
	// FixableOpen is how many open failures belong to a class that can be cleared
	// from here. It is what the header band counts, because "252 of 269 clear with
	// two fixes" is the useful sentence, not the raw total.
	FixableOpen int `json:"fixable_open"`
}

// HandleErrors returns every error class recorded in this run.
func (s *Server) HandleErrors() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		store := s.errorStore()
		if store == nil {
			jsonResponse(w, http.StatusOK, errorsResponse{Classes: []migration.ClassSummary{}})
			return
		}

		classes := store.Classes()
		total, open, classCount := store.Counts()

		fixable := 0
		for _, c := range classes {
			if c.Remedy.Kind != migration.RemedyNone {
				fixable += c.OpenCount
			}
		}

		jsonResponse(w, http.StatusOK, errorsResponse{
			Classes:     classes,
			Total:       total,
			Open:        open,
			ClassCount:  classCount,
			FixableOpen: fixable,
		})
	}
}

// errorClassResponse is one class with the instances behind it.
type errorClassResponse struct {
	Class   *migration.ClassSummary `json:"class"`
	Records []migration.ErrorRecord `json:"records"`
	Total   int                     `json:"total"`
	// Truncated says the list was cut short. A class can hold thousands of
	// instances and nobody scrolls those; the filter is how you get to the ones
	// you want.
	Truncated bool `json:"truncated"`
}

// maxRecordsPerResponse bounds how many instances one request returns.
const maxRecordsPerResponse = 500

// HandleErrorClass returns the instances of one class, optionally filtered.
func (s *Server) HandleErrorClass() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		store := s.errorStore()
		if store == nil {
			jsonError(w, http.StatusNotFound, "No errors have been recorded")
			return
		}

		key := migration.ErrorClassKey(r.PathValue("class"))
		var summary *migration.ClassSummary
		for _, c := range store.Classes() {
			if c.Key == key {
				found := c
				summary = &found
				break
			}
		}
		if summary == nil {
			jsonError(w, http.StatusNotFound, fmt.Sprintf("No errors of class %q in this run", key))
			return
		}

		query := r.URL.Query()
		filter := migration.RecordFilter{
			Class:    key,
			Projects: splitCSV(query.Get("project")),
			OpenOnly: query.Get("open") == "1",
		}

		records := store.Records(filter)
		resp := errorClassResponse{Class: summary, Total: len(records), Records: records}
		if len(records) > maxRecordsPerResponse {
			resp.Records = records[:maxRecordsPerResponse]
			resp.Truncated = true
		}
		jsonResponse(w, http.StatusOK, resp)
	}
}

// HandleErrorsExport serves every error, evidence included, as a spreadsheet.
func (s *Server) HandleErrorsExport() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		store := s.errorStore()
		if store == nil {
			jsonError(w, http.StatusNotFound, "No errors have been recorded")
			return
		}
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="migration-errors.csv"`)
		// The body carries token hints and response bodies. Not secret, but not
		// something to leave in a shared cache either.
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte(store.CSV())); err != nil {
			log.Printf("[warn] writing error export: %v", err)
		}
	}
}

// resolveRequest is a remedy applied to a selection.
type resolveRequest struct {
	Remedy   migration.RemedyKind `json:"remedy"`
	Token    string               `json:"token,omitempty"`
	PaceMS   int                  `json:"pace_ms,omitempty"`
	Choice   string               `json:"choice,omitempty"`
	Projects []string             `json:"projects,omitempty"`
	IDs      []string             `json:"ids,omitempty"`
	// DryRun works out the scope without changing anything, so the interface can
	// say what a fix will touch before it is agreed to.
	DryRun bool `json:"dry_run,omitempty"`
}

// resolveResponse reports what a remedy will do, or has started doing.
type resolveResponse struct {
	Status     string                  `json:"status"` // "planned", "started", "resolved"
	Class      migration.ErrorClassKey `json:"class"`
	ItemCount  int                     `json:"item_count"`
	Weight     int                     `json:"weight"`
	Regather   int                     `json:"regather_configs,omitempty"`
	Resolution string                  `json:"resolution"`
	Resolved   int                     `json:"resolved,omitempty"`
}

// HandleErrorClassResolve applies a class's remedy to a selection of its failures.
//
// A dry run reports the scope and changes nothing. A real one re-reads whatever
// has to be re-read, walks the checkpoint back for exactly the selected items and
// runs the migration again — which retries those and skips everything else.
func (s *Server) HandleErrorClassResolve() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req resolveRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err.Error() != "EOF" {
			jsonError(w, http.StatusBadRequest, "Could not read the request: "+err.Error())
			return
		}

		key := migration.ErrorClassKey(r.PathValue("class"))

		s.mu.Lock()
		exec := s.executor
		cfg := s.config
		cm := s.checkpoint
		busy := s.migrating || s.rerunning
		s.mu.Unlock()

		switch {
		case exec == nil:
			jsonError(w, http.StatusBadRequest, "No migration has run, so there is nothing to re-run")
			return
		case busy:
			jsonError(w, http.StatusConflict, "A run is already in progress. Wait for it to stop, then apply the fix.")
			return
		case cfg == nil:
			jsonError(w, http.StatusBadRequest, "Configuration not set")
			return
		}

		rerun := migration.RerunRequest{
			Class:    key,
			Remedy:   req.Remedy,
			Token:    req.Token,
			PaceMS:   req.PaceMS,
			Choice:   req.Choice,
			Projects: req.Projects,
			IDs:      req.IDs,
		}

		// A dry run answers "what would this touch?" and must leave the migration
		// exactly as it found it.
		plan, err := exec.PlanRerun(rerun)
		if err == nil && !req.DryRun {
			plan, err = exec.PrepareRerun(rerun)
		}
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}

		resp := resolveResponse{
			Class:      plan.Class,
			ItemCount:  plan.ItemCount,
			Weight:     plan.Weight,
			Regather:   len(plan.RegatherConfigs),
			Resolution: plan.Resolution,
		}

		if req.DryRun {
			resp.Status = "planned"
			jsonResponse(w, http.StatusOK, resp)
			return
		}

		// Nothing to run: the remedy is settled by recording the decision.
		if !plan.NeedsRun {
			resp.Status = "resolved"
			resp.Resolved = exec.FinishRerun(plan)
			s.SSE.Publish(SSEEvent{Type: "errors_changed", Data: map[string]any{"class": plan.Class}})
			jsonResponse(w, http.StatusOK, resp)
			return
		}

		// Apply the infrastructure half of the remedy: a replacement client for a
		// token, a floor on request pacing for a rate limit.
		if err := s.applyRemedy(plan, req, cfg); err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}

		s.mu.Lock()
		s.rerunning = true
		s.mu.Unlock()

		resp.Status = "started"
		jsonResponse(w, http.StatusAccepted, resp)

		go s.runRemedy(exec, cm, plan)
	}
}

// applyRemedy swaps in whatever the remedy needs before the re-run.
func (s *Server) applyRemedy(plan *migration.RerunPlan, req resolveRequest, cfg *migration.MigrationConfig) error {
	switch plan.Remedy {
	case migration.RemedyToken:
		if !isValidTokenPrefix(req.Token) {
			return fmt.Errorf("that does not look like a Doppler token — it should start with dp.ct., dp.pt. or dp.sa.")
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if plan.Side == migration.SideSource {
			limiter := s.sharedLimiterLocked("source")
			s.executor.ReplaceSource(doppler.NewClient(cfg.SourceHost, req.Token, doppler.WithRateLimiter(limiter)))
			cfg.SourceToken = req.Token
		} else {
			limiter := s.sharedLimiterLocked("target")
			s.executor.ReplaceTarget(doppler.NewClient(cfg.TargetHost, req.Token, doppler.WithRateLimiter(limiter)))
			cfg.TargetToken = req.Token
		}

	case migration.RemedyPace:
		pace := durationMS(req.PaceMS)
		s.mu.RLock()
		source, target := s.sourceLimiter, s.targetLimiter
		s.mu.RUnlock()
		// Pace both sides. A 429 on writes is usually accompanied by pressure on
		// reads, and the point of this remedy is to stop pushing.
		for _, limiter := range []*doppler.RateLimiter{source, target} {
			if limiter == nil {
				continue
			}
			limiter.SetMinInterval(doppler.RateLimitRead, pace)
			limiter.SetMinInterval(doppler.RateLimitSecretRead, pace)
			limiter.SetMinInterval(doppler.RateLimitWrite, pace)
		}
	}
	return nil
}

// runRemedy re-reads what has to be re-read, then runs the migration again. The
// executor skips everything already complete, so only the reset items are tried.
func (s *Server) runRemedy(exec *migration.Executor, cm *migration.CheckpointManager, plan *migration.RerunPlan) {
	ctx, cancel := context.WithCancel(context.Background())

	s.mu.Lock()
	s.migrationCancel = cancel
	s.mu.Unlock()

	defer func() {
		cancel()
		s.mu.Lock()
		s.rerunning = false
		s.migrationCancel = nil
		s.mu.Unlock()
	}()

	// The debounced checkpoint writer was stopped when the first run finished.
	cm.Start()
	defer cm.Stop()

	if len(plan.RegatherConfigs) > 0 {
		s.SSE.Publish(SSEEvent{Type: "rerun_progress", Data: map[string]any{
			"class": plan.Class, "stage": "regather", "configs": len(plan.RegatherConfigs),
		}})
		if err := exec.RegatherConfigs(ctx, plan.RegatherConfigs); err != nil {
			log.Printf("[error] re-gather before re-run: %v", err)
			s.SSE.Publish(SSEEvent{Type: "rerun_complete", Data: map[string]any{
				"class": plan.Class, "status": "failed", "error": err.Error(),
			}})
			return
		}
	}

	s.SSE.Publish(SSEEvent{Type: "rerun_progress", Data: map[string]any{
		"class": plan.Class, "stage": "running", "items": plan.ItemCount,
	}})

	runErr := exec.Execute(ctx)
	resolved := exec.FinishRerun(plan)

	status := "completed"
	errText := ""
	if runErr != nil {
		status = "failed"
		errText = runErr.Error()
	}

	s.SSE.Publish(SSEEvent{Type: "rerun_complete", Data: map[string]any{
		"class":      plan.Class,
		"status":     status,
		"error":      errText,
		"resolved":   resolved,
		"still_open": plan.ItemCount - resolved,
	}})
	s.SSE.Publish(SSEEvent{Type: "errors_changed", Data: map[string]any{"class": plan.Class}})
}

// errorStore returns the store for the current or most recent run.
func (s *Server) errorStore() *migration.ErrorStore {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.errors
}

func splitCSV(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// durationMS converts a millisecond gap into a duration, clamped to something
// sane so a typo cannot stall a run for an hour between requests.
func durationMS(ms int) time.Duration {
	if ms < 0 {
		return 0
	}
	if ms > 60_000 {
		ms = 60_000
	}
	return time.Duration(ms) * time.Millisecond
}
