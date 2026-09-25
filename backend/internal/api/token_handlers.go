package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/doppler/onprem-migrator/internal/doppler"
	"github.com/doppler/onprem-migrator/internal/migration"
)

// Service token issuance.
//
// The one place in this API where a secret crosses the wire on purpose. A token
// value exists in the create response, is copied into the JSON body of exactly
// one reply, and is dropped. Nothing writes it to the ledger, the checkpoint or
// the log, and no GET can fetch it back — losing it is what reroll is for.

const tokenLedgerFileName = "service-tokens.json"

// tokenLedgerPath puts the ledger beside the checkpoint, so one CHECKPOINT_DIR
// carries the whole of a migration's resumable state.
func tokenLedgerPath() string {
	return filepath.Join(filepath.Dir(checkpointPath()), tokenLedgerFileName)
}

// ledger returns the token ledger, building it on first use from whatever has
// been gathered. Seeding is additive, so decisions already on disk survive.
func (s *Server) ledger() (*migration.TokenLedger, error) {
	l, gathered, err := s.loadLedger()
	if err != nil || !gathered {
		return l, err
	}

	// Seeded from the scoped data, so a partial run's worklist holds that run's
	// tokens. Seeding only ever adds, so a later full run brings the rest in
	// beside the decisions already made. Done outside the server lock, because
	// resolving the scope takes it.
	l.Seed(s.scopedData(), time.Now().UTC())
	if err := l.Save(); err != nil {
		return nil, err
	}
	l.SetScope(s.tokenScope())

	// A failure from before a token's project was migrated described a target
	// without that project; it does not say anything about issuing it now.
	last := s.history().LastMigrated()
	var latest time.Time
	for _, at := range last {
		if at.After(latest) {
			latest = at
		}
	}
	l.ClearStaleFailures(func(rec migration.TokenRecord) time.Time {
		if rec.Kind == migration.KindServiceAccountToken {
			// An account comes across with whichever run migrates a project that
			// uses it; the latest run is the conservative boundary.
			return latest
		}
		return last[rec.Project]
	})
	return l, nil
}

// errTokenNotMigrated is the refusal to act on a token whose project, or
// service account, is not on the target yet.
const errTokenNotMigrated = "This token's project has not been migrated yet, so there is nowhere on the target to create it. It becomes available once a run migrates that project."

// tokenScope says which tokens have somewhere to go on the target.
//
// After a full migration, every one. Before it, a service token is in scope
// once a run has migrated its project, and a service account token once a
// run has migrated its account — which a partial run does for the accounts
// the migrated projects' members use, and only those. Nothing is in scope
// before any run: no project exists on the target to issue a token in.
func (s *Server) tokenScope() func(migration.TokenRecord) bool {
	s.mu.RLock()
	data := s.migrationData
	dev := s.config != nil && s.config.DevMode
	s.mu.RUnlock()
	// A dry run records no runs, so nothing would ever be in scope; and
	// nothing it "issues" reaches a target anyway.
	if data == nil || dev {
		return nil
	}

	hist := s.history()
	if hist.FullDone() {
		return nil
	}

	migrated := hist.Migrated()
	accounts := map[string]bool{}
	var slugs []string
	for _, pd := range data.Projects {
		if _, ok := migrated[pd.Project.Slug]; ok {
			slugs = append(slugs, pd.Project.Slug)
		}
	}
	if len(slugs) > 0 {
		scope := migration.Scope{Mode: migration.ScopePartial, Projects: slugs}
		if res, err := migration.ResolveScope(data, scope, nil); err == nil {
			for _, sa := range migration.ApplyScope(data, res).ServiceAccounts {
				accounts[sa.Slug] = true
			}
		}
	}

	return func(rec migration.TokenRecord) bool {
		if rec.Kind == migration.KindServiceAccountToken {
			return accounts[rec.ServiceAccount]
		}
		_, ok := migrated[rec.Project]
		return ok
	}
}

// loadLedger returns the ledger, reading it from disk the first time.
func (s *Server) loadLedger() (*migration.TokenLedger, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.tokens == nil {
		l := migration.NewTokenLedger(tokenLedgerPath())
		if err := l.Load(); err != nil {
			// A ledger that cannot be read is not a reason to refuse the page —
			// but it is a reason not to silently start from nothing, because that
			// would offer to reissue tokens that already exist.
			return nil, false, err
		}
		s.tokens = l
	}
	return s.tokens, s.migrationData != nil, nil
}

// tokenGate is the summary the cutover page reads to decide whether it can run.
type tokenGate struct {
	Counts  migration.TokenCounts `json:"counts"`
	Blocked bool                  `json:"blocked"`
	Reason  string                `json:"reason,omitempty"`
}

// tokenGateFor builds the gate from a ledger.
func tokenGateFor(l *migration.TokenLedger) tokenGate {
	counts := l.Counts()
	// Deferred tokens block too: cutover removes the write access issuing one
	// needs, so "later" has to be decided before it.
	open := counts.Pending + counts.Deferred
	g := tokenGate{Counts: counts, Blocked: open > 0}
	if g.Blocked {
		g.Reason = fmt.Sprintf("%d service %s neither been issued nor skipped",
			open, plural(open, "token has", "tokens have"))
		if counts.Deferred > 0 {
			g.Reason += fmt.Sprintf(" (%d set aside for later)", counts.Deferred)
		}
	}
	return g
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// HandleTokens returns the worklist.
func (s *Server) HandleTokens() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		l, err := s.ledger()
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}

		s.mu.RLock()
		gathered := s.migrationData != nil
		s.mu.RUnlock()

		if !gathered {
			jsonError(w, http.StatusBadRequest, "No data gathered. Call /api/v1/gather first.")
			return
		}

		jsonResponse(w, http.StatusOK, map[string]interface{}{
			"records": l.Records(),
			"counts":  l.Counts(),
			// Tokens whose project has not been migrated yet, and so are not
			// offered — for the page to say they exist rather than hide them.
			"not_yet_migrated": l.NotYetInScope(),
			"gate":             tokenGateFor(l),
		})
	}
}

type tokenKeyRequest struct {
	Key       string `json:"key"`
	NotNeeded bool   `json:"not_needed"`
}

// decodeTokenKey reads and validates the {key} body shared by most of these.
func decodeTokenKey(w http.ResponseWriter, r *http.Request) (*tokenKeyRequest, bool) {
	var req tokenKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "Could not read the request: "+err.Error())
		return nil, false
	}
	if strings.TrimSpace(req.Key) == "" {
		jsonError(w, http.StatusBadRequest, "A token key is required.")
		return nil, false
	}
	return &req, true
}

// issuer builds a TokenIssuer against the configured target.
func (s *Server) issuer(l *migration.TokenLedger) (*migration.TokenIssuer, error) {
	s.mu.RLock()
	cfg := s.config
	s.mu.RUnlock()

	if cfg == nil {
		return nil, fmt.Errorf("nothing configured yet")
	}
	if cfg.DevMode {
		return nil, fmt.Errorf("dev mode is on, which pins this session to a dry run — no token can be created on the target")
	}

	target := doppler.NewClient(cfg.TargetHost, cfg.TargetToken, doppler.WithRateLimiter(s.sharedLimiter("target")))
	return migration.NewTokenIssuer(target, l), nil
}

// HandleTokenIssue creates one token and returns its value, once.
func (s *Server) HandleTokenIssue() http.HandlerFunc {
	return s.issueHandler(false)
}

// HandleTokenReroll replaces one token and returns the new value, once.
func (s *Server) HandleTokenReroll() http.HandlerFunc {
	return s.issueHandler(true)
}

func (s *Server) issueHandler(reroll bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		req, ok := decodeTokenKey(w, r)
		if !ok {
			return
		}

		l, err := s.ledger()
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !l.InScope(req.Key) {
			jsonError(w, http.StatusConflict, errTokenNotMigrated)
			return
		}

		iss, err := s.issuer(l)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}

		var issued *migration.IssuedToken
		if reroll {
			issued, err = iss.Reroll(r.Context(), req.Key)
		} else {
			issued, err = iss.Issue(r.Context(), req.Key)
		}
		if err != nil {
			jsonError(w, tokenFailureStatus(err), err.Error())
			return
		}

		// The only response in this API that carries a secret. It is not logged
		// here and is not stored anywhere on the way out.
		jsonResponse(w, http.StatusOK, map[string]interface{}{
			"token":  issued,
			"counts": l.Counts(),
			"gate":   tokenGateFor(l),
		})
	}
}

// tokenFailureStatus keeps a refusal by the target distinguishable from a
// refusal by the migrator, which the UI shows differently.
func tokenFailureStatus(err error) int {
	var apiErr *doppler.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusForbidden, http.StatusTooManyRequests:
			return apiErr.StatusCode
		}
		return http.StatusBadGateway
	}
	return http.StatusBadRequest
}

// HandleTokenSkip records a decision to handle a token outside the migrator.
func (s *Server) HandleTokenSkip() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		req, ok := decodeTokenKey(w, r)
		if !ok {
			return
		}

		l, err := s.ledger()
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !l.InScope(req.Key) {
			jsonError(w, http.StatusConflict, errTokenNotMigrated)
			return
		}
		if err := l.MarkSkipped(req.Key, req.NotNeeded); err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}

		jsonResponse(w, http.StatusOK, map[string]interface{}{
			"records": l.Records(),
			"counts":  l.Counts(),
			"gate":    tokenGateFor(l),
		})
	}
}

// HandleTokenSkipAll closes every undecided row onto the worklist.
func (s *Server) HandleTokenSkipAll() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		l, err := s.ledger()
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}

		moved := l.SkipAllPending()
		jsonResponse(w, http.StatusOK, map[string]interface{}{
			"moved":   moved,
			"records": l.Records(),
			"counts":  l.Counts(),
			"gate":    tokenGateFor(l),
		})
	}
}

// HandleTokenUndo returns a skipped row to undecided.
// HandleTokenDefer sets a token aside until the next run, which offers it
// again.
func (s *Server) HandleTokenDefer() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		req, ok := decodeTokenKey(w, r)
		if !ok {
			return
		}

		l, err := s.ledger()
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !l.InScope(req.Key) {
			jsonError(w, http.StatusConflict, errTokenNotMigrated)
			return
		}
		if err := l.MarkDeferred(req.Key); err != nil {
			jsonError(w, http.StatusConflict, err.Error())
			return
		}

		jsonResponse(w, http.StatusOK, map[string]interface{}{
			"records": l.Records(),
			"counts":  l.Counts(),
			"gate":    tokenGateFor(l),
		})
	}
}

func (s *Server) HandleTokenUndo() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		req, ok := decodeTokenKey(w, r)
		if !ok {
			return
		}

		l, err := s.ledger()
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !l.InScope(req.Key) {
			jsonError(w, http.StatusConflict, errTokenNotMigrated)
			return
		}
		if err := l.Undo(req.Key); err != nil {
			jsonError(w, http.StatusConflict, err.Error())
			return
		}

		jsonResponse(w, http.StatusOK, map[string]interface{}{
			"records": l.Records(),
			"counts":  l.Counts(),
			"gate":    tokenGateFor(l),
		})
	}
}

// HandleTokenWorklist exports the CSV of what still has to be rebuilt by hand.
func (s *Server) HandleTokenWorklist() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		l, err := s.ledger()
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}

		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="service-token-worklist.csv"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(l.CSV()))
	}
}

// HandleTokenDetect looks for tokens already on the target under the same name,
// so the page can offer a reroll instead of quietly creating a duplicate.
//
// One list call per config that still has an undecided token, which is why it is
// a request of its own rather than part of loading the page.
func (s *Server) HandleTokenDetect() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		l, err := s.ledger()
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}

		s.mu.RLock()
		cfg := s.config
		s.mu.RUnlock()

		if cfg == nil {
			jsonError(w, http.StatusBadRequest, "Nothing configured yet")
			return
		}
		if cfg.DevMode {
			// Nothing was ever written, so nothing can pre-exist because of us.
			jsonResponse(w, http.StatusOK, map[string]interface{}{"records": l.Records(), "counts": l.Counts(), "gate": tokenGateFor(l)})
			return
		}

		target := doppler.NewClient(cfg.TargetHost, cfg.TargetToken, doppler.WithRateLimiter(s.sharedLimiter("target")))

		// Group the pending rows by the list call that would answer them, so a
		// config with twelve tokens costs one request rather than twelve.
		type scope struct {
			project, config, account string
		}
		wanted := map[scope][]migration.TokenRecord{}
		for _, rec := range l.Pending() {
			key := scope{project: rec.Project, config: rec.Config, account: rec.ServiceAccount}
			wanted[key] = append(wanted[key], rec)
		}

		var found []string
		for sc, recs := range wanted {
			names := map[string]bool{}

			if sc.account != "" {
				// The record holds the source's slug; the target's copy of the
				// account has its own, found by name.
				account, err := migration.ResolveTargetServiceAccount(r.Context(), target, recs[0].ServiceAccountName)
				if err != nil {
					continue
				}
				tokens, err := target.ListServiceAccountTokens(r.Context(), account)
				if err != nil {
					continue // A scope we cannot read is simply not reported on.
				}
				for _, t := range tokens {
					n := t.Name
					if n == "" {
						n = t.Slug
					}
					names[n] = true
				}
			} else {
				tokens, err := target.ListServiceTokens(r.Context(), sc.project, sc.config)
				if err != nil {
					continue
				}
				for _, t := range tokens {
					names[t.Name] = true
				}
			}

			for _, rec := range recs {
				if names[rec.Name] {
					found = append(found, rec.Key)
				}
			}
		}

		l.MarkPreexisting(found)

		jsonResponse(w, http.StatusOK, map[string]interface{}{
			"detected": len(found),
			"records":  l.Records(),
			"counts":   l.Counts(),
			"gate":     tokenGateFor(l),
		})
	}
}
