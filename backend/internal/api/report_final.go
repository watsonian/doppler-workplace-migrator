package api

import (
	"bytes"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/doppleruniversity/workplace-migrator/internal/migration"
)

// The final report: the whole migration in one document, for whoever signs it
// off and for whoever picks it up later.
//
// It carries names, counts, statuses and error messages — the same fields the
// errors CSV already exports — and never a value. There is nothing here to
// leak one: secret values are not kept after they are written, and token
// values are never stored at all. Request URLs and response bodies are left
// out too; they are evidence for triage, not part of a record.

// finalReport is the report's content, rendered as HTML or sent as JSON.
type finalReport struct {
	GeneratedAt time.Time `json:"generated_at"`
	SourceHost  string    `json:"source_host"`
	TargetHost  string    `json:"target_host"`
	// SourceWorkplace is the source workplace's name, when it was gathered.
	SourceWorkplace string `json:"source_workplace,omitempty"`
	DevMode         bool   `json:"dev_mode"`

	// Status is the one-line verdict; StatusTone is how it is shown.
	Status     string `json:"status"`
	StatusTone string `json:"status_tone"` // good, warn, bad

	Runs   []reportRun               `json:"runs"`
	Source migration.PlanSummary     `json:"source"`
	Phases []reportPhase             `json:"phases"`
	TopUp  *migration.ConvergeResult `json:"top_up,omitempty"`

	Tokens reportTokens `json:"tokens"`

	Cutover reportCutover `json:"cutover"`

	Errors reportErrors `json:"errors"`

	// UsersMissing are source users not on the target, whose access was skipped.
	UsersMissing []string               `json:"users_missing"`
	ManualSteps  []migration.ManualStep `json:"manual_steps"`
	Notes        []string               `json:"notes"`
}

type reportRun struct {
	Mode     string    `json:"mode"`
	Kind     string    `json:"kind,omitempty"`
	Created  int       `json:"created"`
	Updated  int       `json:"updated"`
	Failed   int       `json:"failed"`
	Projects []string  `json:"projects"`
	At       time.Time `json:"at"`
}

type reportPhase struct {
	Label     string `json:"label"`
	Status    string `json:"status"`
	Completed int    `json:"completed"`
	Skipped   int    `json:"skipped"`
	Failed    int    `json:"failed"`
}

type reportTokens struct {
	Counts  migration.TokenCounts `json:"counts"`
	Records []reportToken         `json:"records"`
}

type reportToken struct {
	Kind        string     `json:"kind"`
	Placement   string     `json:"placement"`
	Name        string     `json:"name"`
	Access      string     `json:"access,omitempty"`
	Disposition string     `json:"disposition"`
	IssuedAt    *time.Time `json:"issued_at,omitempty"`
	Note        string     `json:"note,omitempty"`
}

type reportCutover struct {
	// State is ran, not_needed, done (recorded before a restart, details
	// gone with it), ready_to_finish (nothing to apply, not yet finished),
	// waiting, not_run or blocked.
	State  string                   `json:"state"`
	Result *migration.CutoverResult `json:"result,omitempty"`
	At     *time.Time               `json:"at,omitempty"`
}

type reportErrors struct {
	Total     int                `json:"total"`
	Open      int                `json:"open"`
	Classes   []reportErrorClass `json:"classes"`
	OpenItems []reportErrorItem  `json:"open_items"`
}

type reportErrorClass struct {
	Title    string `json:"title"`
	Phase    string `json:"phase"`
	Count    int    `json:"count"`
	Open     int    `json:"open"`
	Resolved int    `json:"resolved"`
}

type reportErrorItem struct {
	Class   string `json:"class"`
	Phase   string `json:"phase"`
	Item    string `json:"item"`
	Message string `json:"message"`
}

// phaseLabels are the executor's phases as the Migrate page names them.
var phaseLabels = []struct{ key, label string }{
	{"workplace_settings_and_roles", "Workplace & Roles"},
	{"groups_and_service_accounts", "Groups & Service Accounts"},
	{"projects_and_environments", "Projects & Environments"},
	{"configs", "Configs"},
	{"secrets_and_locks", "Secrets & Config Locks"},
	{"config_inheritance", "Config Inheritance"},
	{"webhooks", "Webhooks"},
	{"members", "Members & User Access"},
	{"change_request_policies", "Change Request Policies"},
	{"trusted_ips", "Trusted IPs"},
}

// buildFinalReport assembles the report from what the server holds.
func (s *Server) buildFinalReport() finalReport {
	s.mu.RLock()
	cfg := s.config
	data := s.migrationData
	cm := s.checkpoint
	store := s.errors
	run := s.lastRun
	cutoverResult := s.cutoverResult
	cutoverPlanned := s.cutoverPlanned
	plan := s.plan
	s.mu.RUnlock()

	r := finalReport{
		GeneratedAt:  time.Now().UTC(),
		Runs:         []reportRun{},
		Phases:       []reportPhase{},
		UsersMissing: []string{},
		ManualSteps:  []migration.ManualStep{},
		Notes:        []string{},
		Tokens:       reportTokens{Records: []reportToken{}},
		Errors:       reportErrors{Classes: []reportErrorClass{}, OpenItems: []reportErrorItem{}},
	}
	if cfg != nil {
		r.SourceHost, r.TargetHost, r.DevMode = cfg.SourceHost, cfg.TargetHost, cfg.DevMode
	}
	if data != nil {
		r.Source = migration.GeneratePlan(data).Summary
		if data.Workplace != nil {
			r.SourceWorkplace = data.Workplace.Name
		}
		if ex := data.ExcludedServiceAccount; ex != nil {
			r.Notes = append(r.Notes, "Service account \""+ex.Name+"\" was not migrated: the source token belongs to it, and it exists to run the migration.")
		}
		for _, u := range data.Unavailable {
			r.Notes = append(r.Notes, u.Label+" were not read from the source: the source token was refused ("+u.Message+"). "+u.Consequence)
		}
		if n := len(data.PersonalConfigs); n > 0 {
			r.Notes = append(r.Notes, pluralize(n, "personal config was", "personal configs were")+" not migrated. Each belongs to one user, and the target creates its own.")
		}
	}

	hist := s.history()
	for _, run := range hist.Runs {
		r.Runs = append(r.Runs, reportRun{
			Mode: string(run.Mode), Kind: run.Kind, Projects: run.Projects, At: run.At,
			Created: run.Created, Updated: run.Updated, Failed: run.Failed,
		})
	}

	if cm != nil {
		cp := cm.Data()
		for _, ph := range phaseLabels {
			pc, ok := cp.Phases[ph.key]
			if !ok {
				continue
			}
			rp := reportPhase{Label: ph.label, Status: string(pc.Status)}
			for _, item := range pc.Items {
				switch item.Status {
				case migration.StatusCompleted:
					rp.Completed++
				case migration.StatusSkipped:
					rp.Skipped++
				case migration.StatusFailed:
					rp.Failed++
				}
			}
			r.Phases = append(r.Phases, rp)
		}
	}
	if run != nil {
		r.TopUp = run.TopUp
	}

	if l, err := s.ledger(); err == nil {
		r.Tokens.Counts = l.Counts()
		for _, rec := range l.Records() {
			r.Tokens.Records = append(r.Tokens.Records, reportToken{
				Kind:        tokenKindLabel(rec.Kind),
				Placement:   rec.Scope(),
				Name:        rec.Name,
				Access:      rec.Access,
				Disposition: tokenDispositionLabel(rec.Disposition),
				IssuedAt:    rec.IssuedAt,
				Note:        rec.Note,
			})
		}
		sort.SliceStable(r.Tokens.Records, func(i, j int) bool {
			a, b := r.Tokens.Records[i], r.Tokens.Records[j]
			if a.Kind != b.Kind {
				return a.Kind > b.Kind // service tokens first
			}
			return a.Placement+a.Name < b.Placement+b.Name
		})
	}

	notNeeded := cutoverPlanned != nil && cutoverPlanned.Empty()
	switch {
	case cutoverResult != nil:
		r.Cutover = reportCutover{State: "ran", Result: cutoverResult, At: hist.CutoverAt}
	case hist.CutoverBlocked():
		r.Cutover = reportCutover{State: "blocked"}
	case hist.CutoverAt != nil && notNeeded:
		r.Cutover = reportCutover{State: "not_needed", At: hist.CutoverAt}
	case hist.CutoverAt != nil:
		r.Cutover = reportCutover{State: "done", At: hist.CutoverAt}
	case hist.InFlight() && notNeeded:
		r.Cutover = reportCutover{State: "ready_to_finish"}
	case hist.InFlight():
		r.Cutover = reportCutover{State: "waiting"}
	case notNeeded:
		r.Cutover = reportCutover{State: "not_needed"}
	default:
		r.Cutover = reportCutover{State: "not_run"}
	}

	if store != nil {
		for _, c := range store.Classes() {
			r.Errors.Total += c.Count
			r.Errors.Open += c.OpenCount
			r.Errors.Classes = append(r.Errors.Classes, reportErrorClass{
				Title: c.Title, Phase: c.PhaseName, Count: c.Count, Open: c.OpenCount, Resolved: c.ResolvedCount,
			})
		}
		for _, rec := range store.Records(migration.RecordFilter{OpenOnly: true}) {
			r.Errors.OpenItems = append(r.Errors.OpenItems, reportErrorItem{
				Class:   migration.LookupErrorClass(rec.Class).Title,
				Phase:   rec.PhaseName,
				Item:    rec.Item,
				Message: rec.Message,
			})
		}
	}

	if plan != nil {
		r.UsersMissing = append(r.UsersMissing, plan.UserCheck.Missing...)
		for _, ms := range plan.ManualSteps {
			if len(ms.Items) > 0 {
				r.ManualSteps = append(r.ManualSteps, ms)
			}
		}
	}

	r.Status, r.StatusTone = reportVerdict(r)
	return r
}

// reportVerdict is the report's first line: whether the migration is done,
// and if not, the one thing standing in the way.
func reportVerdict(r finalReport) (string, string) {
	switch {
	case len(r.Runs) == 0 && len(r.Phases) == 0:
		return "No migration has run yet", "warn"
	case r.Cutover.State == "blocked":
		return "Partial migration only — a full migration has not run", "warn"
	case r.Errors.Open > 0:
		return "Finished with " + pluralize(r.Errors.Open, "open error", "open errors"), "bad"
	case r.Cutover.State == "ran" && !r.Cutover.Result.Complete():
		return "Cutover did not finish", "bad"
	case r.Cutover.State == "not_run":
		return "Migrated — cutover has not run", "warn"
	case r.Cutover.State == "waiting":
		return "Migrated — waiting to cut over", "warn"
	case r.Cutover.State == "ready_to_finish":
		return "Migrated — ready to finish", "warn"
	case r.Tokens.Counts.Pending+r.Tokens.Counts.Deferred > 0:
		n := r.Tokens.Counts.Pending + r.Tokens.Counts.Deferred
		return "Migrated — " + pluralize(n, "service token is", "service tokens are") + " still undecided", "warn"
	default:
		return "Migration complete", "good"
	}
}

func tokenKindLabel(k migration.TokenKind) string {
	if k == migration.KindServiceAccountToken {
		return "Service account token"
	}
	return "Service token"
}

func tokenDispositionLabel(d migration.TokenDisposition) string {
	switch d {
	case migration.DispIssued:
		return "Issued"
	case migration.DispToRecreate:
		return "Skipped — to recreate"
	case migration.DispNotNeeded:
		return "Not needed"
	case migration.DispDeferred:
		return "Set aside for later"
	default:
		return "Undecided"
	}
}

// pluralize writes "1 thing" or "3 things".
func pluralize(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// HandleFinalReportHTML serves the report as a page to keep.
func (s *Server) HandleFinalReportHTML() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		report := s.buildFinalReport()
		var buf bytes.Buffer
		if err := renderFinalReport(&buf, report); err != nil {
			jsonError(w, http.StatusInternalServerError, "Could not render the report: "+err.Error())
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Disposition", `attachment; filename="`+reportFileName(report, "html")+`"`)
		_, _ = w.Write(buf.Bytes())
	}
}

// HandleFinalReportJSON serves the same report as data.
func (s *Server) HandleFinalReportJSON() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		report := s.buildFinalReport()
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Disposition", `attachment; filename="`+reportFileName(report, "json")+`"`)
		jsonResponse(w, http.StatusOK, report)
	}
}

// reportFileName is "migration-report-2026-09-23.html".
func reportFileName(r finalReport, ext string) string {
	return "migration-report-" + r.GeneratedAt.Format("2006-01-02") + "." + ext
}
