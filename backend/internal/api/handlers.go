package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/doppler/onprem-migrator/internal/doppler"
	"github.com/doppler/onprem-migrator/internal/migration"
	"github.com/doppler/onprem-migrator/internal/preflight"
)

const (
	// defaultCheckpointDir is used when CHECKPOINT_DIR is unset. The Docker image
	// sets CHECKPOINT_DIR=/data explicitly and mounts a volume there.
	defaultCheckpointDir = "./data"
	checkpointFileName   = "migration-checkpoint.json"
)

// checkpointPath returns the checkpoint file location, honoring CHECKPOINT_DIR.
// The directory itself is created when the checkpoint is first written.
func checkpointPath() string {
	dir := strings.TrimSpace(os.Getenv("CHECKPOINT_DIR"))
	if dir == "" {
		dir = defaultCheckpointDir
	}
	return filepath.Join(dir, checkpointFileName)
}

// devModeForced reports whether DEV_MODE is set in the environment, which pins the
// session to a dry run: pre-flight checks against the target are skipped and no
// writes are performed. scripts/dev.sh sets it. docker-compose.yml deliberately does
// not — see docker-compose.dev.yml.
func devModeForced() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("DEV_MODE")))
	return v == "1" || v == "true" || v == "yes"
}

// Server holds shared state for all API handlers.
type Server struct {
	SSE             *SSEBroker
	mu              sync.RWMutex
	config          *migration.MigrationConfig
	migrationData   *migration.MigrationData
	plan            *migration.MigrationPlan
	gathering       bool // true while a gather is in progress
	migrating       bool // true while migration is in progress
	executor        *migration.Executor
	checkpoint      *migration.CheckpointManager
	migrationResult *migrateResponse // set when async migration completes
	gatherResult    *gatherResponse  // set when async gather completes
	checkpointPath  string           // resolved once at startup
	devModeForced   bool             // resolved once at startup, from DEV_MODE

	// envSourceToken and envTargetToken come from SOURCE_DOPPLER_TOKEN and
	// TARGET_DOPPLER_TOKEN, read once at startup. They stand in for a token
	// nobody has typed yet, so a migrator started with them opens with both
	// fields filled; anything configured since takes precedence. Held only
	// here — the page is sent a hint, never the value.
	envSourceToken  string
	envTargetToken  string
	migrationCancel context.CancelFunc

	// errors outlives the executor. Triage is most useful after a run has
	// finished, and the executor handle is cleared the moment it stops.
	errors *migration.ErrorStore

	// tokens records what has been decided about each service token. It holds no
	// values — only whether one was issued here or is yours to rebuild — and it
	// is what cutover consults before it removes the access issuing needs.
	tokens *migration.TokenLedger

	// scope is the full-or-partial choice from the Scope page; nil means full.
	// runOptions are the Review page's choices, applied by a run's top-up.
	// lastRun is what the most recent run covered and did, for the summary.
	scope      *migration.Scope
	runOptions *migration.ConvergeOptions
	lastRun    *runInfo

	// Rate limiters are held here rather than left inside the clients, because
	// "re-run slower" pacing is applied to them from outside a run.
	sourceLimiter *doppler.RateLimiter
	targetLimiter *doppler.RateLimiter

	// rateWaits are the rate-limit pauses in progress, for the UI.
	rateWaits rateWaits
	// rateStatus is where each token stands against its rate limits.
	rateStatus rateStatusStore

	// rerunning guards a remedy's re-run the way migrating guards the first one.
	rerunning bool

	// comparing and converging guard the repeatable-run path the same way.
	comparing  bool
	converging bool

	// diff and snapshot are the most recent comparison. The snapshot is kept
	// because a cutover is planned against what the target actually held, not
	// against what the source says it should.
	diff     *migration.Diff
	snapshot *migration.TargetSnapshot

	convergeResult *migration.ConvergeResult
	cutoverResult  *migration.CutoverResult
	// cutoverPlanned is the last cutover plan worked out, so the final report
	// can tell "not needed" (nothing to mirror) from "not run".
	cutoverPlanned *migration.CutoverPlan
}

// validateCIDR checks an address before it is written into every restricted
// config. A malformed one would be silently useless: the server stores what it
// is given and only ever compares, so a typo reads as "no address matched" at
// the moment the migrator needs one to.
func validateCIDR(cidr string) error {
	if _, _, err := net.ParseCIDR(cidr); err != nil {
		if ip := net.ParseIP(cidr); ip != nil {
			return fmt.Errorf("%s needs a prefix length — use %s/32 for a single address", cidr, cidr)
		}
		return fmt.Errorf("%q is not an address range; expected something like 203.0.113.47/32", cidr)
	}
	return nil
}

// Shutdown stops an in-flight migration and flushes its checkpoint.
//
// The checkpoint writer debounces to once a second, so without this the last
// second of progress is lost on exit — and that progress is exactly what a resume
// reads. Cancelling first stops the executor from issuing further writes while the
// process is on its way down.
func (s *Server) Shutdown() {
	s.mu.RLock()
	cancel := s.migrationCancel
	cm := s.checkpoint
	s.mu.RUnlock()

	if cancel != nil {
		cancel()
	}
	if cm != nil {
		// Save rather than Stop: the migration goroutine owns Stop, and calling it
		// twice closes an already-closed channel.
		if err := cm.Save(); err != nil {
			log.Printf("[warn] final checkpoint save failed: %v", err)
		}
	}
}

// NewServer creates a new API server with initialized components.
func NewServer() *Server {
	return &Server{
		SSE:            NewSSEBroker(),
		checkpointPath: checkpointPath(),
		devModeForced:  devModeForced(),
		envSourceToken: strings.TrimSpace(os.Getenv("SOURCE_DOPPLER_TOKEN")),
		envTargetToken: strings.TrimSpace(os.Getenv("TARGET_DOPPLER_TOKEN")),
	}
}

// jsonResponse writes a JSON response with the given status code.
func jsonResponse(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if data != nil {
		json.NewEncoder(w).Encode(data)
	}
}

// jsonError writes a JSON error response.
func jsonError(w http.ResponseWriter, status int, message string) {
	jsonResponse(w, status, map[string]string{"error": message})
}

// configureRequest is the JSON body for POST /api/v1/configure.
//
// The token fields are pointers so an omitted field is distinguishable from an
// empty one: omitted means "keep the token already held", which is how the UI
// re-submits the form without the token values ever being sent back to it.
type configureRequest struct {
	SourceHost  string  `json:"source_host"`
	SourceToken *string `json:"source_token"`
	TargetHost  string  `json:"target_host"`
	TargetToken *string `json:"target_token"`
	DevMode     bool    `json:"dev_mode"`
	// MigrateWorkplaceName is off unless explicitly enabled.
	MigrateWorkplaceName bool `json:"migrate_workplace_name"`
	// StopOnNewErrorClass is on unless explicitly disabled, so a pointer is
	// needed to tell "the client said false" from "the client said nothing".
	StopOnNewErrorClass *bool `json:"stop_on_new_error_class"`
}

// configureResponse is returned from POST /api/v1/configure.
type configureResponse struct {
	Valid   bool     `json:"valid"`
	Errors  []string `json:"errors,omitempty"`
	DevMode bool     `json:"dev_mode"`
}

// getConfigureResponse is returned from GET /api/v1/configure.
//
// Token values are never included. The hints carry only the token's type prefix and
// last four characters, which is enough for someone to recognize which credential is
// loaded without the endpoint handing out a workplace-read or target-write token.
type getConfigureResponse struct {
	Configured      bool   `json:"configured"`
	SourceHost      string `json:"source_host,omitempty"`
	SourceTokenHint string `json:"source_token_hint,omitempty"`
	HasSourceToken  bool   `json:"has_source_token"`
	TargetHost      string `json:"target_host,omitempty"`
	TargetTokenHint string `json:"target_token_hint,omitempty"`
	HasTargetToken  bool   `json:"has_target_token"`
	// The held token came from SOURCE_DOPPLER_TOKEN / TARGET_DOPPLER_TOKEN,
	// so the page can say where it came from.
	SourceTokenFromEnv   bool `json:"source_token_from_env"`
	TargetTokenFromEnv   bool `json:"target_token_from_env"`
	DevMode              bool `json:"dev_mode"`
	DevModeForced        bool `json:"dev_mode_forced"`
	MigrateWorkplaceName bool `json:"migrate_workplace_name"`
	StopOnNewErrorClass  bool `json:"stop_on_new_error_class"`
}

// tokenPrefixes are Doppler's documented auth token formats, longest first so that
// dp.said. is matched before any shorter prefix could shadow it.
// See https://docs.doppler.com/reference/auth-token-formats
var tokenPrefixes = []string{
	"dp.audit.",
	"dp.scim.",
	"dp.said.",
	"dp.ct.",
	"dp.pt.",
	"dp.st.",
	"dp.sa.",
}

// tokenHint renders a token as its format prefix plus its last four characters,
// e.g. "dp.ct.…a1b2". An unrecognized format yields just the last four. A token too
// short to have four characters beyond its prefix reveals none of them.
func tokenHint(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}

	prefix := ""
	for _, p := range tokenPrefixes {
		if strings.HasPrefix(token, p) {
			prefix = p
			break
		}
	}

	rest := token[len(prefix):]
	if len(rest) < 4 {
		return prefix + "…"
	}
	return prefix + "…" + rest[len(rest)-4:]
}

// HandleGetConfigure reports which configuration is loaded, so the form can show
// what is already set. It never returns token values.
func (s *Server) HandleGetConfigure() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.RLock()
		cfg := s.config
		s.mu.RUnlock()

		// Credentials-adjacent, and cheap to regenerate — keep it out of caches.
		w.Header().Set("Cache-Control", "no-store")

		source, target := s.heldTokens(cfg)
		resp := getConfigureResponse{
			SourceTokenHint:     tokenHint(source),
			HasSourceToken:      source != "",
			SourceTokenFromEnv:  source != "" && source == s.envSourceToken,
			TargetTokenHint:     tokenHint(target),
			HasTargetToken:      target != "",
			TargetTokenFromEnv:  target != "" && target == s.envTargetToken,
			DevMode:             s.devModeForced,
			DevModeForced:       s.devModeForced,
			StopOnNewErrorClass: true,
		}
		if cfg != nil {
			resp.Configured = true
			resp.SourceHost = cfg.SourceHost
			resp.TargetHost = cfg.TargetHost
			resp.DevMode = cfg.DevMode
			resp.MigrateWorkplaceName = cfg.MigrateWorkplaceName
			resp.StopOnNewErrorClass = cfg.StopOnNewErrorClass
		}
		jsonResponse(w, http.StatusOK, resp)
	}
}

// HandleConfigure accepts and validates migration configuration.
func (s *Server) HandleConfigure() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req configureRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, http.StatusBadRequest, "Invalid JSON body")
			return
		}

		// An omitted token means "keep the one already held", since the UI is never
		// given the value back to resubmit.
		s.mu.RLock()
		existing := s.config
		s.mu.RUnlock()

		sourceToken, targetToken := s.heldTokens(existing)
		if req.SourceToken != nil {
			sourceToken = *req.SourceToken
		}
		if req.TargetToken != nil {
			targetToken = *req.TargetToken
		}

		// DEV_MODE in the environment pins the session to a dry run.
		devMode := req.DevMode || s.devModeForced

		errors := validateConfig(req.SourceHost, sourceToken, req.TargetHost, targetToken, devMode)
		if len(errors) > 0 {
			jsonResponse(w, http.StatusBadRequest, configureResponse{
				Valid:  false,
				Errors: errors,
			})
			return
		}

		// One migration at a time. The run history holds one pair of instances,
		// so configuring another while a migration is unfinished would discard
		// its record of what it has done — and with it the top-ups, the
		// waiting, and the cutover it is waiting for.
		if h, err := migration.PeekScopeHistory(scopeHistoryPath()); err == nil && h.Unfinished() &&
			(h.SourceHost != req.SourceHost || h.TargetHost != req.TargetHost) {
			jsonResponse(w, http.StatusConflict, configureResponse{
				Valid: false,
				Errors: []string{fmt.Sprintf(
					"A migration from %s to %s is still in progress. Finish it (cut over) before starting one with other instances.",
					h.SourceHost, h.TargetHost)},
			})
			return
		}

		s.mu.Lock()
		s.config = &migration.MigrationConfig{
			SourceHost:           req.SourceHost,
			SourceToken:          sourceToken,
			TargetHost:           req.TargetHost,
			TargetToken:          targetToken,
			DevMode:              devMode,
			MigrateWorkplaceName: req.MigrateWorkplaceName,
			// Defaults on. A run that does not stop on the first failure of a kind
			// can burn through the whole workplace on a bad token before anyone
			// looks at the screen.
			StopOnNewErrorClass: req.StopOnNewErrorClass == nil || *req.StopOnNewErrorClass,
		}
		s.mu.Unlock()

		jsonResponse(w, http.StatusOK, configureResponse{
			Valid:   true,
			DevMode: devMode,
		})
	}
}

// validateConfig validates resolved configuration values and returns any errors.
// Tokens are the resolved values — either newly submitted or carried over from the
// existing configuration — not the raw request fields.
func validateConfig(sourceHost, sourceToken, targetHost, targetToken string, devMode bool) []string {
	var errors []string

	// Source host required
	if strings.TrimSpace(sourceHost) == "" {
		errors = append(errors, "Source host is required")
	} else if !isValidURL(sourceHost) {
		errors = append(errors, "Source host must be a valid URL (e.g., https://api.doppler.com)")
	}

	// Source token required
	if strings.TrimSpace(sourceToken) == "" {
		errors = append(errors, "Source token is required")
	} else if !isValidTokenPrefix(sourceToken) {
		errors = append(errors, "Source token must start with dp.ct., dp.pt., or dp.sa.")
	}

	if !devMode {
		// Target host required when not in dev mode
		if strings.TrimSpace(targetHost) == "" {
			errors = append(errors, "Target host is required")
		} else if !isValidURL(targetHost) {
			errors = append(errors, "Target host must be a valid URL (e.g., https://doppler.internal.company.com)")
		}

		// Target token required when not in dev mode
		if strings.TrimSpace(targetToken) == "" {
			errors = append(errors, "Target token is required")
		} else if !isValidTokenPrefix(targetToken) {
			errors = append(errors, "Target token must start with dp.ct., dp.pt., or dp.sa.")
		}
	}

	return errors
}

// isValidTokenPrefix checks if a token has a valid Doppler token prefix.
func isValidTokenPrefix(token string) bool {
	return strings.HasPrefix(token, "dp.ct.") ||
		strings.HasPrefix(token, "dp.pt.") ||
		strings.HasPrefix(token, "dp.sa.")
}

// isValidURL checks if a string is a valid URL.
func isValidURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// preflightResponse is returned from POST /api/v1/preflight.
type preflightResponse struct {
	Checks []preflight.Check `json:"checks"`
	Passed bool              `json:"passed"`
	// HasWarnings is reported separately from Passed: a warning does not block the
	// migration, but the UI offers to re-check rather than moving straight on.
	HasWarnings bool `json:"has_warnings"`
	DevMode     bool `json:"dev_mode"`
}

// HandlePreflight runs pre-flight checks against source and target.
// Runs synchronously — returns full results in the POST response.
// Also publishes SSE events for optional live progress.
func (s *Server) HandlePreflight() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.RLock()
		cfg := s.config
		s.mu.RUnlock()

		if cfg == nil {
			jsonError(w, http.StatusBadRequest, "Configuration not set. Call /api/v1/configure first.")
			return
		}

		source := doppler.NewClient(cfg.SourceHost, cfg.SourceToken, doppler.WithRateLimiter(s.newLimiter("source")))

		var target *doppler.Client
		if !cfg.DevMode && cfg.TargetHost != "" {
			target = doppler.NewClient(cfg.TargetHost, cfg.TargetToken, doppler.WithRateLimiter(s.newLimiter("target")))
		}

		checker := preflight.NewChecker(source, target, cfg.DevMode, func(check preflight.Check) {
			s.SSE.Publish(SSEEvent{
				Type: "preflight_check",
				Data: check,
			})
		})

		checks := checker.RunAll(r.Context())

		resp := preflightResponse{
			Checks:      checks,
			Passed:      preflight.AllPassed(checks),
			HasWarnings: preflight.HasWarnings(checks),
			DevMode:     cfg.DevMode,
		}

		// Publish completion via SSE for any connected listeners
		s.SSE.Publish(SSEEvent{
			Type: "preflight_complete",
			Data: resp,
		})

		jsonResponse(w, http.StatusOK, resp)
	}
}

// gatherResponse is returned when gathering completes.
type gatherResponse struct {
	Status  string                `json:"status"`
	Summary migration.PlanSummary `json:"summary"`
	Error   string                `json:"error,omitempty"`
	// Unavailable is what the source token was refused, and so is left out.
	Unavailable []migration.UnavailableResource `json:"unavailable"`
}

// HandleGather starts gathering data from the source instance.
//
// Runs in the background and returns 202 immediately. It used to run inline and
// return the whole result, which a realistic workplace could not finish inside the
// server's write timeout — the response write then failed and the UI reported
// "Gathering failed" for a gather that had in fact succeeded and stored its data.
// Progress and completion arrive over SSE, with GET /gather as the fallback for a
// client that missed the event.
func (s *Server) HandleGather() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Claim the gather slot under a single lock. Reading the flag, releasing,
		// then setting it let two concurrent requests both pass the check.
		s.mu.Lock()
		cfg := s.config
		switch {
		case cfg == nil:
			s.mu.Unlock()
			jsonError(w, http.StatusBadRequest, "Configuration not set. Call /api/v1/configure first.")
			return
		case s.gathering:
			s.mu.Unlock()
			jsonError(w, http.StatusConflict, "Gathering is already in progress")
			return
		}
		s.gathering = true
		s.migrationData = nil
		s.plan = nil
		s.gatherResult = nil
		s.mu.Unlock()

		jsonResponse(w, http.StatusAccepted, map[string]string{"status": "started"})

		// Detached from the request: the client is no longer waiting on it, and
		// cancelling the gather because the browser navigated away would be wrong.
		go s.runGather(context.Background(), cfg)
	}
}

// runGather performs the gather and records the outcome for SSE and polling.
func (s *Server) runGather(ctx context.Context, cfg *migration.MigrationConfig) {
	defer func() {
		s.mu.Lock()
		s.gathering = false
		s.mu.Unlock()
	}()

	source := doppler.NewClient(cfg.SourceHost, cfg.SourceToken, doppler.WithRateLimiter(s.newLimiter("source")))
	gatherer := migration.NewGatherer(source, func(p migration.GatherProgress) {
		s.SSE.Publish(SSEEvent{Type: "gather_progress", Data: p})
	})

	data, err := gatherer.GatherAll(ctx)
	if err != nil {
		s.mu.Lock()
		s.gatherResult = &gatherResponse{Status: "failed", Error: err.Error()}
		s.mu.Unlock()
		s.SSE.Publish(SSEEvent{
			Type: "gather_complete",
			Data: map[string]interface{}{"status": "failed", "error": err.Error()},
		})
		return
	}

	// The account the source token belongs to is there to run this migration,
	// not to be migrated by it. If the lookup fails, nothing is excluded: the
	// account then migrates like any other, which is the old behaviour.
	if info, err := source.GetTokenInfo(ctx); err == nil && info.Principal.Type == "service_account" {
		if sa := migration.ExcludeServiceAccount(data, info.Principal.Slug); sa != nil {
			log.Printf("[gather] leaving out service account %q: the source token belongs to it", sa.Name)
		}
	}

	plan := migration.GeneratePlan(data)

	// Run user check if not in dev mode
	if !cfg.DevMode && cfg.TargetHost != "" {
		target := doppler.NewClient(cfg.TargetHost, cfg.TargetToken, doppler.WithRateLimiter(s.newLimiter("target")))
		userCheck, err := migration.CheckUsers(ctx, data, target)
		if err != nil {
			// Non-fatal: log and continue with empty user check
			plan.UserCheck = migration.UserCheckResult{}
		} else {
			plan.UserCheck = *userCheck
		}
	}

	s.mu.Lock()
	s.migrationData = data
	s.plan = plan
	// A comparison of the last gather is not a comparison of this one. Kept,
	// the Compare page showed it instead of comparing again — so a top-up's
	// Review listed the previous top-up's changes.
	s.diff = nil
	s.snapshot = nil
	// A scope names projects from the previous gather. Starting again from full
	// is safer than carrying a selection into data it was not made against.
	s.scope = nil
	unavailable := data.Unavailable
	if unavailable == nil {
		unavailable = []migration.UnavailableResource{}
	}
	s.gatherResult = &gatherResponse{Status: "complete", Summary: plan.Summary, Unavailable: unavailable}
	s.mu.Unlock()

	s.SSE.Publish(SSEEvent{
		Type: "gather_complete",
		Data: map[string]interface{}{"status": "complete", "summary": plan.Summary, "unavailable": unavailable},
	})
}

// HandleGatherStatus reports the state of the gather, for a client that missed the
// SSE completion event.
func (s *Server) HandleGatherStatus() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.RLock()
		gathering := s.gathering
		result := s.gatherResult
		s.mu.RUnlock()

		resp := map[string]interface{}{"gathering": gathering}
		if result != nil {
			resp["status"] = result.Status
			if result.Error != "" {
				resp["error"] = result.Error
			}
			if result.Status == "complete" {
				resp["summary"] = result.Summary
				resp["unavailable"] = result.Unavailable
			}
		}
		jsonResponse(w, http.StatusOK, resp)
	}
}

// HandlePlan returns the migration plan.
func (s *Server) HandlePlan() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.RLock()
		plan := s.plan
		s.mu.RUnlock()

		if plan == nil {
			jsonError(w, http.StatusBadRequest, "No data gathered. Call /api/v1/gather first.")
			return
		}

		jsonResponse(w, http.StatusOK, plan)
	}
}

// migrateResponse is returned when migration completes or fails.
type migrateResponse struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// HandleMigrate starts migration execution.
func (s *Server) HandleMigrate() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Claim the migration slot under a single lock; see HandleGather. Two
		// executors writing the same checkpoint and target would corrupt both.
		s.mu.Lock()
		cfg := s.config
		data := s.migrationData
		plan := s.plan
		switch {
		case cfg == nil:
			s.mu.Unlock()
			jsonError(w, http.StatusBadRequest, "Configuration not set. Call /api/v1/configure first.")
			return
		case data == nil || plan == nil:
			s.mu.Unlock()
			jsonError(w, http.StatusBadRequest, "No data gathered. Call /api/v1/gather first.")
			return
		case s.migrating:
			s.mu.Unlock()
			jsonError(w, http.StatusConflict, "Migration is already in progress")
			return
		}
		s.migrating = true
		s.mu.Unlock()

		rp, err := s.planRun()
		if err != nil {
			s.mu.Lock()
			s.migrating = false
			s.mu.Unlock()
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		mode := rp.Resolution.Mode
		opts := s.currentRunOptions()

		// A project an earlier run migrated but that is no longer on the target
		// — deleted there since — is not "previously migrated" any more: there
		// is nothing to bring up to date. It goes back through the migration
		// engine, which builds a project whole (secrets, inheritance, locks,
		// webhooks, members), rather than the top-up, which writes differences
		// and has none beneath a project that is missing.
		var reintroduced []string
		if !cfg.DevMode && cfg.TargetHost != "" && len(rp.PreviouslyMigrated) > 0 {
			check := doppler.NewClient(cfg.TargetHost, cfg.TargetToken, doppler.WithRateLimiter(s.sharedLimiter("target")))
			if present, lerr := check.ListProjects(r.Context()); lerr != nil {
				log.Printf("[warn] could not list the target's projects; treating earlier projects as present: %v", lerr)
			} else {
				rp.PreviouslyMigrated, rp.Remaining, reintroduced = splitMissing(rp.PreviouslyMigrated, rp.Remaining, present)
			}
		}

		// Stage one is the migration engine over what no earlier run has covered.
		// It creates what is missing and never updates, so it steps around the
		// projects an earlier run moved rather than keep their old values and
		// create each of their webhooks a second time.
		stageOne := migration.WithProjects(rp.scoped, rp.PreviouslyMigrated, true)

		// Stage two brings those projects up to date. It also runs when the
		// Review page asked for deletions: only the top-up deletes, so without it
		// that choice would be silently dropped, as it used to be.
		stageTwo := rp.PreviouslyMigrated
		if opts.Deletes() {
			stageTwo = rp.Resolution.Projects
		}

		// Resume only a checkpoint for this source and target, still unfinished,
		// and taken over the same set of projects; anything else starts fresh.
		cm := migration.NewCheckpointManager(s.checkpointPath)
		key := migration.ScopeKey(mode, rp.Remaining, nil)
		if mode != migration.ScopePartial {
			key = migration.ScopeKey(mode, nil, rp.PreviouslyMigrated)
		}
		if _, err := cm.LoadForScope(cfg.SourceHost, cfg.TargetHost, key); err != nil {
			log.Printf("[warn] could not read checkpoint, starting fresh: %v", err)
		}
		cm.Start()

		sourceLimiter := s.newLimiter("source")
		targetLimiter := s.newLimiter("target")
		source := doppler.NewClient(cfg.SourceHost, cfg.SourceToken, doppler.WithRateLimiter(sourceLimiter))
		var target migration.DopplerReadWriter
		if !cfg.DevMode && cfg.TargetHost != "" {
			target = doppler.NewClient(cfg.TargetHost, cfg.TargetToken, doppler.WithRateLimiter(targetLimiter))
		}

		errorStore := migration.NewErrorStore()

		exec := migration.NewExecutor(source, target, stageOne, cm, cfg.DevMode, func(p migration.MigrateProgress) {
			s.SSE.Publish(SSEEvent{
				Type: "migrate_progress",
				Data: p,
			})
			// Publish phase_complete for phase-level completions (including phases with item errors)
			if (p.Status == "completed" || p.Status == "completed_with_errors") && p.Resource == "" {
				s.SSE.Publish(SSEEvent{
					Type: "phase_complete",
					Data: p,
				})
			}
		},
			migration.WithWorkplaceName(cfg.MigrateWorkplaceName),
			migration.WithStopOnNewErrorClass(cfg.StopOnNewErrorClass),
			migration.WithErrorStore(errorStore),
			migration.WithInstanceURLs(apiRoot(cfg.SourceHost), apiRoot(cfg.TargetHost)),
		)

		migrateCtx, cancelMigrate := context.WithCancel(context.Background())

		s.mu.Lock()
		// migrating was already claimed above; just publish the handles.
		s.executor = exec
		s.checkpoint = cm
		s.migrationCancel = cancelMigrate
		s.migrationResult = nil // clear previous result
		s.errors = errorStore
		s.sourceLimiter = sourceLimiter
		s.targetLimiter = targetLimiter
		s.lastRun = &runInfo{
			Mode:                 mode,
			Resolution:           rp.Resolution,
			Remaining:            len(rp.Remaining),
			PreviouslyMigrated:   rp.PreviouslyMigrated,
			PreviouslyMigratedAt: rp.PreviouslyMigratedAt,
			Reintroduced:         reintroduced,
			Stage:                "remaining",
		}
		run := *s.lastRun
		s.mu.Unlock()

		// The run's actual split, which the page cannot work out from the scope:
		// an earlier project deleted from the target has moved to stage one.
		// Also carried by /status, for a page whose event stream connects late.
		s.SSE.Publish(SSEEvent{Type: "migration_stage", Data: map[string]any{
			"stage": "remaining", "previously_migrated": len(run.PreviouslyMigrated), "reintroduced": run.Reintroduced,
		}})

		// Return immediately, run migration in background
		jsonResponse(w, http.StatusAccepted, map[string]string{"status": "started"})

		go func() {
			defer func() {
				cancelMigrate()
				cm.Stop()
				s.mu.Lock()
				s.migrating = false
				s.migrationCancel = nil
				// The executor handle is kept: fixing an error class re-runs
				// against the same executor, data and checkpoint, and that is
				// something you do after the run has stopped.
				s.mu.Unlock()
			}()

			err := exec.Execute(migrateCtx)

			// Recorded once stage one finishes, whatever stage two does: those
			// projects now exist on the target, and the next run has to step
			// around them. A dry run wrote nothing and records nothing — it would
			// otherwise lock projects into later runs and block cutover over a
			// migration that never happened.
			hist := s.history()
			isTopUp := false
			if err == nil && !cfg.DevMode {
				run := migration.ScopeRun{Mode: mode, Projects: rp.Resolution.Projects, At: time.Now().UTC()}
				isTopUp = mode == migration.ScopeFull && hist.FullDone()
				if cm != nil {
					if isTopUp {
						// A top-up's first stage creates what is new on the source —
						// a group, a policy, a project deleted from the target and
						// migrated again — and those count. What already existed
						// comes back as skipped and does not. The exceptions are the
						// writes it re-applies every time whether or not anything
						// changed; counting them would say it did what it only
						// confirmed.
						run.Changes = migration.ChangesFromCheckpoint(cm.Data(), stageOne, migration.ReappliedEveryRun...)
						run.Created, run.Failed = run.Changes.Totals()
					} else {
						run.Created, run.Failed = checkpointCounts(cm)
						run.Changes = migration.ChangesFromCheckpoint(cm.Data(), stageOne)
					}
				}
				if herr := hist.Record(run); herr != nil {
					log.Printf("[error] could not record the run in the scope history: %v", herr)
				}
				// Tokens set aside "for later" come back with each run, to be
				// offered again.
				if l, lerr := s.ledger(); lerr == nil {
					if n := l.ResurfaceDeferred(); n > 0 {
						log.Printf("[migrate] %d service token(s) set aside for later are undecided again", n)
					}
				}
				if cm != nil {
					if herr := hist.RecordWrites(migration.WritesFromCheckpoint(cm.Data())); herr != nil {
						log.Printf("[error] could not record the configs this run wrote: %v", herr)
					}
				}
				if herr := hist.RememberSettings(migration.RunSettings{
					MigrateWorkplaceName: cfg.MigrateWorkplaceName,
					StopOnNewErrorClass:  cfg.StopOnNewErrorClass,
				}); herr != nil {
					log.Printf("[error] could not record the run's settings: %v", herr)
				}
			}

			if err == nil && migrateCtx.Err() == nil && len(stageTwo) > 0 && target != nil {
				s.setRunStage("previously_migrated")
				topUp, terr := s.topUp(migrateCtx, target, rp, stageTwo, opts, errorStore)
				s.mu.Lock()
				s.lastRun.TopUp = topUp
				if terr != nil {
					s.lastRun.TopUpError = terr.Error()
				}
				s.mu.Unlock()
				if terr != nil {
					log.Printf("[error] top-up of previously migrated projects: %v", terr)
				}
				if topUp != nil && !cfg.DevMode {
					if herr := hist.RecordWrites(topUp.Writes); herr != nil {
						log.Printf("[error] could not record the configs the top-up wrote: %v", herr)
					}
					if herr := hist.AddToLast(topUp.Created, topUp.Updated, topUp.Failed, topUp.Changes); herr != nil {
						log.Printf("[error] could not record the top-up's counts: %v", herr)
					}
				}
				s.SSE.Publish(SSEEvent{Type: "errors_changed", Data: map[string]any{}})
			}
			s.setRunStage("done")

			result := &migrateResponse{}
			if err != nil {
				result.Status = "failed"
				result.Error = err.Error()
			} else {
				result.Status = "completed"
			}

			// Store result for polling before clearing migrating flag
			s.mu.Lock()
			s.migrationResult = result
			s.mu.Unlock()

			s.SSE.Publish(SSEEvent{
				Type: "migration_complete",
				Data: map[string]interface{}{"status": result.Status, "error": result.Error},
			})
		}()
	}
}

// HandleMigratePause pauses migration.
func (s *Server) HandleMigratePause() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.RLock()
		isMigrating := s.migrating
		exec := s.executor
		s.mu.RUnlock()

		if !isMigrating || exec == nil {
			jsonError(w, http.StatusBadRequest, "No migration in progress")
			return
		}

		exec.Pause()
		jsonResponse(w, http.StatusOK, map[string]string{"status": "paused"})
	}
}

// HandleMigrateResume resumes migration.
func (s *Server) HandleMigrateResume() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.RLock()
		isMigrating := s.migrating
		exec := s.executor
		s.mu.RUnlock()

		if !isMigrating || exec == nil {
			jsonError(w, http.StatusBadRequest, "No migration in progress")
			return
		}

		if !exec.IsPaused() {
			jsonError(w, http.StatusBadRequest, "Migration is not paused")
			return
		}

		exec.Resume()
		jsonResponse(w, http.StatusOK, map[string]string{"status": "resumed"})
	}
}

// statusResponse is returned from GET /api/v1/status.
type statusResponse struct {
	Migrating       bool   `json:"migrating"`
	Paused          bool   `json:"paused"`
	Phase           string `json:"phase,omitempty"`
	MigrationResult string `json:"migration_result,omitempty"`
	Error           string `json:"error,omitempty"`

	// Phases is where each phase stands. The page reads it alongside the event
	// stream, which can connect after a phase with nothing to do has already
	// started and finished.
	Phases map[string]migration.PhaseState `json:"phases,omitempty"`

	// Run is how the current or last run split its projects.
	Run *runInfo `json:"run,omitempty"`
}

// HandleStatus returns current migration status.
func (s *Server) HandleStatus() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.RLock()
		isMigrating := s.migrating
		exec := s.executor
		migResult := s.migrationResult
		s.mu.RUnlock()

		resp := statusResponse{
			Migrating: isMigrating,
		}
		if exec != nil {
			resp.Paused = exec.IsPaused()
			// Only for the run in progress or the one just finished. An executor
			// whose result has been forgotten belongs to a run the next one is
			// about to replace, and its phases would read as the new run's.
			if isMigrating || migResult != nil {
				resp.Phases = exec.PhaseStates()
				s.mu.RLock()
				if s.lastRun != nil {
					run := *s.lastRun
					resp.Run = &run
				}
				s.mu.RUnlock()
			}
		}
		if migResult != nil {
			resp.MigrationResult = migResult.Status
			resp.Error = migResult.Error
		}

		jsonResponse(w, http.StatusOK, resp)
	}
}

// HandleCheckpoint returns checkpoint data.
func (s *Server) HandleCheckpoint() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.RLock()
		cm := s.checkpoint
		s.mu.RUnlock()

		if cm == nil {
			jsonResponse(w, http.StatusOK, map[string]interface{}{
				"phases":    map[string]interface{}{},
				"completed": false,
			})
			return
		}

		jsonResponse(w, http.StatusOK, cm.Data())
	}
}

// HandleReportServiceTokens returns the service token report.
func (s *Server) HandleReportServiceTokens() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.RLock()
		data := s.migrationData
		s.mu.RUnlock()

		if data == nil {
			jsonError(w, http.StatusBadRequest, "No data gathered. Call /api/v1/gather first.")
			return
		}

		jsonResponse(w, http.StatusOK, map[string]interface{}{
			"service_tokens":         data.ServiceTokens,
			"service_account_tokens": data.ServiceAccountTokens,
		})
	}
}

// HandleReportManualSteps returns the manual steps report.
func (s *Server) HandleReportManualSteps() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.RLock()
		plan := s.plan
		s.mu.RUnlock()

		if plan == nil {
			jsonError(w, http.StatusBadRequest, "No plan available. Call /api/v1/gather first.")
			return
		}

		jsonResponse(w, http.StatusOK, map[string]interface{}{
			"manual_steps": plan.ManualSteps,
			"user_check":   plan.UserCheck,
		})
	}
}

// HandleReportSummary returns the migration summary.
func (s *Server) HandleReportSummary() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.RLock()
		plan := s.plan
		cm := s.checkpoint
		s.mu.RUnlock()

		if plan == nil {
			jsonError(w, http.StatusBadRequest, "No plan available. Call /api/v1/gather first.")
			return
		}

		resp := map[string]interface{}{
			"summary":           plan.Summary,
			"checkpoint":        nil,
			"manual_step_count": plan.Summary.ManualStepCount,
		}

		// Things the operator needs to know were not migrated, surfaced on the
		// final report rather than only in the manual steps they may have skimmed.
		var notes []string
		if plan.Summary.PersonalConfigCount > 0 {
			notes = append(notes, fmt.Sprintf(
				"%d personal config(s) were not migrated. They belong to individual users and the target instance creates its own; anyone relying on a personal secret override will need to set it again there.",
				plan.Summary.PersonalConfigCount))
		}
		resp["notes"] = notes

		// What is left by hand, by kind, so the summary can say "2 integrations,
		// 1 rotated secret" rather than a bare total.
		type manualKind struct {
			Category string `json:"category"`
			Count    int    `json:"count"`
		}
		kinds := make([]manualKind, 0, len(plan.ManualSteps))
		for _, ms := range plan.ManualSteps {
			if n := len(ms.Items); n > 0 {
				kinds = append(kinds, manualKind{Category: ms.Category, Count: n})
			}
		}
		resp["manual_steps"] = kinds

		if cm != nil {
			cpData := cm.Data()
			resp["checkpoint"] = cpData
		}

		// What the last run covered, and the whole source for comparison: a
		// partial's summary reads "3 of 92 projects", and that "of" is the point.
		s.mu.RLock()
		run := s.lastRun
		data := s.migrationData
		s.mu.RUnlock()
		resp["run"] = run
		if data != nil {
			resp["source_summary"] = migration.GeneratePlan(data).Summary
		}
		resp["cutover_blocked"] = s.history().CutoverBlocked()
		// Whether the next step is a choice between cutting over and waiting,
		// and whether the run just finished was a top-up.
		resp["standby"] = s.buildStandby()

		// Tokens are no longer counted among the manual steps — the token page
		// handles them — so the summary needs their state separately, or a
		// workplace whose only manual work is tokens would show none at all.
		if l, err := s.ledger(); err == nil {
			resp["token_counts"] = l.Counts()
		}

		jsonResponse(w, http.StatusOK, resp)
	}
}

// heldTokens is what an empty token field means: the token already configured,
// or failing that the one from the environment.
func (s *Server) heldTokens(cfg *migration.MigrationConfig) (source, target string) {
	source, target = s.envSourceToken, s.envTargetToken
	if cfg != nil {
		if cfg.SourceToken != "" {
			source = cfg.SourceToken
		}
		if cfg.TargetToken != "" {
			target = cfg.TargetToken
		}
	}
	return source, target
}

// checkpointCounts totals what a run's checkpoint says it created and failed.
// Workplace settings are an update of something that always exists, so they
// are not counted as created.
func checkpointCounts(cm *migration.CheckpointManager) (created, failed int) {
	if cm == nil {
		return 0, 0
	}
	for _, phase := range cm.Data().Phases {
		for key, item := range phase.Items {
			switch item.Status {
			case migration.StatusCompleted:
				if key != "workplace_settings" {
					created++
				}
			case migration.StatusFailed:
				failed++
			}
		}
	}
	return created, failed
}

// ResumeFromEnvironment picks an in-flight migration back up after a restart.
//
// Tokens live in memory only, so a restart forgets the configuration. When
// both come from the environment and the run history holds a migration
// waiting to cut over, everything Configure would be told is already known —
// the instances from the history, the tokens from the environment, the
// options from the last run — so the session is configured as it was, and
// the waiting page opens ready to top up. Nothing is read from either
// instance here; the first gather is the check that the tokens still work.
//
// Returns whether it resumed.
func (s *Server) ResumeFromEnvironment() bool {
	s.mu.RLock()
	configured := s.config != nil
	src, tgt := s.envSourceToken, s.envTargetToken
	dev := s.devModeForced
	s.mu.RUnlock()
	if configured || src == "" || tgt == "" || dev {
		return false
	}

	h, err := migration.PeekScopeHistory(scopeHistoryPath())
	if err != nil || !h.InFlight() || h.SourceHost == "" || h.TargetHost == "" {
		return false
	}
	if errs := validateConfig(h.SourceHost, src, h.TargetHost, tgt, false); len(errs) > 0 {
		log.Printf("[warn] not resuming the waiting migration: %s", strings.Join(errs, "; "))
		return false
	}

	cfg := &migration.MigrationConfig{
		SourceHost: h.SourceHost, SourceToken: src,
		TargetHost: h.TargetHost, TargetToken: tgt,
		StopOnNewErrorClass: true,
	}
	if h.Settings != nil {
		cfg.MigrateWorkplaceName = h.Settings.MigrateWorkplaceName
		cfg.StopOnNewErrorClass = h.Settings.StopOnNewErrorClass
	}

	s.mu.Lock()
	if s.config != nil { // configured in the meantime
		s.mu.Unlock()
		return false
	}
	s.config = cfg
	s.mu.Unlock()
	return true
}

// splitMissing moves the earlier projects the target no longer has from
// previously into remaining.
func splitMissing(previously, remaining []string, present []doppler.Project) (kept, rest, missing []string) {
	have := make(map[string]bool, len(present))
	for _, p := range present {
		have[p.Slug] = true
	}
	kept = make([]string, 0, len(previously))
	for _, slug := range previously {
		if have[slug] {
			kept = append(kept, slug)
		} else {
			log.Printf("[migrate] %s was migrated before but is not on the target; migrating it again", slug)
			missing = append(missing, slug)
		}
	}
	return kept, append(remaining, missing...), missing
}
