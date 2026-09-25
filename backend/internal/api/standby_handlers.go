package api

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/doppler/onprem-migrator/internal/migration"
)

// Waiting to cut over: a full migration has run, the source is still in use,
// and the target is topped up until the teams are ready to switch. The state
// lives in the run history, so it survives a restart; this is what the
// waiting page, Welcome and the summary read.

// standbyRun is one row of the waiting page's Runs table.
type standbyRun struct {
	Kind     string    `json:"kind"` // full, partial, top_up
	Number   int       `json:"number,omitempty"`
	At       time.Time `json:"at"`
	Projects int       `json:"projects"`
	Created  int       `json:"created"`
	Updated  int       `json:"updated"`
	Failed   int       `json:"failed"`
	// Changes is what the run wrote, for the row's expanded view.
	Changes *migration.RunChanges `json:"changes,omitempty"`
}

type standbyManual struct {
	Category string `json:"category"`
	Title    string `json:"title"`
	Count    int    `json:"count"`
}

type standbyResponse struct {
	// Configured is false before Configure has run, when only the history on
	// disk is known — enough for Welcome to offer to carry on.
	Configured bool `json:"configured"`
	// Gathered says the source has been read since the server started.
	// Cutover plans from it, so without it Cut Over gathers first.
	Gathered   bool   `json:"gathered"`
	SourceHost string `json:"source_host,omitempty"`
	TargetHost string `json:"target_host,omitempty"`

	FullDone bool `json:"full_done"`
	// InFlight is true from the end of the full run until cutover. While it
	// is, this migration is the only one: the index opens the waiting page,
	// and a partial run or other instances are refused.
	InFlight bool `json:"in_flight"`
	Waiting  bool `json:"waiting"`
	// Unfinished is true once any run has finished and until cutover, partial
	// runs included: Welcome offers to carry on rather than start again.
	Unfinished bool `json:"unfinished"`
	// ProjectsMigrated counts the projects any run has covered.
	ProjectsMigrated int        `json:"projects_migrated"`
	WaitingSince     *time.Time `json:"waiting_since,omitempty"`
	CutoverAt        *time.Time `json:"cutover_at,omitempty"`

	FullAt       *time.Time `json:"full_at,omitempty"`
	LastSyncedAt *time.Time `json:"last_synced_at,omitempty"`
	TopUps       int        `json:"top_ups"`
	// LastRun is the most recent run, for a summary deciding how to read.
	LastRun *standbyRun  `json:"last_run,omitempty"`
	Runs    []standbyRun `json:"runs"`

	// What is left before cutover. Tokens block it; errors and hand-work do
	// not, but are worth seeing before the last step rather than at it.
	TokensPending int `json:"tokens_pending"`
	// TokensDeferred are the part of TokensPending set aside for later.
	TokensDeferred int             `json:"tokens_deferred"`
	OpenErrors     int             `json:"open_errors"`
	Manual         []standbyManual `json:"manual"`
}

func (s *Server) buildStandby() standbyResponse {
	s.mu.RLock()
	cfg := s.config
	store := s.errors
	plan := s.plan
	gathered := s.migrationData != nil
	s.mu.RUnlock()

	out := standbyResponse{Runs: []standbyRun{}, Manual: []standbyManual{}, Gathered: gathered}

	var hist *migration.ScopeHistory
	if cfg == nil {
		h, err := migration.PeekScopeHistory(scopeHistoryPath())
		if err != nil {
			log.Printf("[warn] scope history unreadable: %v", err)
			h = &migration.ScopeHistory{}
		}
		hist = h
		out.SourceHost, out.TargetHost = h.SourceHost, h.TargetHost
	} else {
		hist = s.history()
		out.Configured = true
		out.SourceHost, out.TargetHost = cfg.SourceHost, cfg.TargetHost
	}

	out.FullDone = hist.FullDone()
	out.InFlight = hist.InFlight()
	out.Waiting = hist.Waiting()
	out.Unfinished = hist.Unfinished()
	out.ProjectsMigrated = len(hist.Migrated())
	out.WaitingSince = hist.WaitingSince
	out.CutoverAt = hist.CutoverAt
	out.TopUps = hist.TopUps()

	topUp := 0
	for _, run := range hist.Runs {
		row := standbyRun{
			Kind: string(run.Mode), At: run.At, Projects: len(run.Projects),
			Created: run.Created, Updated: run.Updated, Failed: run.Failed,
			Changes: run.Changes,
		}
		if run.Kind == migration.RunKindTopUp {
			topUp++
			row.Kind, row.Number = migration.RunKindTopUp, topUp
		} else if run.Mode == migration.ScopeFull {
			at := run.At
			out.FullAt = &at
		}
		at := run.At
		out.LastSyncedAt = &at
		out.Runs = append(out.Runs, row)
	}
	if n := len(out.Runs); n > 0 {
		last := out.Runs[n-1]
		out.LastRun = &last
	}
	// Newest first, the way the table reads.
	for i, j := 0, len(out.Runs)-1; i < j; i, j = i+1, j-1 {
		out.Runs[i], out.Runs[j] = out.Runs[j], out.Runs[i]
	}

	if cfg != nil {
		if l, err := s.ledger(); err == nil {
			c := l.Counts()
			out.TokensPending = c.Pending + c.Deferred
			out.TokensDeferred = c.Deferred
		}
	}
	if store != nil {
		_, out.OpenErrors, _ = store.Counts()
	}
	if plan != nil {
		for _, ms := range plan.ManualSteps {
			if n := len(ms.Items); n > 0 {
				out.Manual = append(out.Manual, standbyManual{Category: ms.Category, Title: ms.Title, Count: n})
			}
		}
	}
	return out
}

// HandleGetStandby reports where a migration stands between its full run and
// cutover.
func (s *Server) HandleGetStandby() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, http.StatusOK, s.buildStandby())
	}
}

type standbyRequest struct {
	// Action is "wait" (keep the source in use and top up), "cut_over" (the
	// next step is cutover), or "finish" (cutover had nothing to change, so
	// the migration is marked done without one).
	Action string `json:"action"`
}

// HandleSetStandby records the choice made at the end of a full run or a
// top-up.
func (s *Server) HandleSetStandby() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req standbyRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, http.StatusBadRequest, "Could not read the request: "+err.Error())
			return
		}

		s.mu.RLock()
		cfg := s.config
		busy := s.migrating || s.converging || s.rerunning
		planned := s.cutoverPlanned
		s.mu.RUnlock()

		hist := s.history()
		switch {
		case cfg == nil:
			jsonError(w, http.StatusBadRequest, "Configuration not set. Call /api/v1/configure first.")
			return
		case busy:
			jsonError(w, http.StatusConflict, "A run is in progress")
			return
		case !hist.FullDone():
			jsonError(w, http.StatusConflict, "A full migration has not finished yet")
			return
		}

		now := time.Now().UTC()
		var err error
		switch req.Action {
		case "wait":
			err = hist.SetWaiting(true, now)
		case "cut_over":
			err = hist.SetWaiting(false, now)
		case "finish":
			// Only when cutover has nothing to do: otherwise finishing without it
			// would leave the target's trusted IPs unlike the source's.
			if planned == nil || !planned.Empty() {
				jsonError(w, http.StatusConflict, "Cutover still has changes to apply; run it instead")
				return
			}
			if l, lerr := s.ledger(); lerr == nil && l.Counts().Pending+l.Counts().Deferred > 0 {
				jsonError(w, http.StatusConflict, "Service tokens are still undecided. Issue or skip them first.")
				return
			}
			err = hist.RecordCutover(now)
		default:
			jsonError(w, http.StatusBadRequest, `Action must be "wait", "cut_over" or "finish"`)
			return
		}
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, s.buildStandby())
	}
}

// HandleStandbyCheck records a top-up that found nothing to change. It went no
// further than Review, so no run recorded it, but "checked at 16:30, nothing
// had changed" is what tells the next person the target is current.
func (s *Server) HandleStandbyCheck() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.RLock()
		cfg := s.config
		diff := s.diff
		s.mu.RUnlock()

		hist := s.history()
		switch {
		case cfg == nil:
			jsonError(w, http.StatusBadRequest, "Configuration not set. Call /api/v1/configure first.")
			return
		case !hist.InFlight():
			jsonError(w, http.StatusConflict, "Not waiting to cut over")
			return
		case diff == nil:
			jsonError(w, http.StatusConflict, "No comparison has run")
			return
		}
		if c := diff.Counts(); c.Create > 0 || c.Update > 0 {
			jsonError(w, http.StatusConflict, "The comparison found changes; run the top-up instead")
			return
		}
		if cfg.DevMode {
			jsonResponse(w, http.StatusOK, s.buildStandby())
			return
		}
		var projects []string
		if rp, err := s.planRun(); err == nil {
			projects = rp.Resolution.Projects
		}
		if err := hist.Record(migration.ScopeRun{Mode: migration.ScopeFull, Projects: projects, At: time.Now().UTC()}); err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, s.buildStandby())
	}
}
