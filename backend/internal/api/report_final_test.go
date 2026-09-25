package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/doppler/onprem-migrator/internal/doppler"
	"github.com/doppler/onprem-migrator/internal/migration"
)

func getPath(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newAPIRequest("GET", path, nil))
	return rec
}

// reportServer is a finished full migration: one run, one token issued, and a
// cutover that had nothing to do.
func reportServer(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	s, h := scopeServer(t)
	s.migrationData.Workplace.Name = "Acme <Prod>" // escaped, not interpreted
	s.migrationData.ServiceTokens = []doppler.ServiceToken{
		{Project: "payments", Config: "prd", Name: "ci-deploy", Access: "read"},
	}
	_ = s.history().Record(migration.ScopeRun{Mode: migration.ScopeFull, Projects: []string{"payments", "checkout", "search", "billing"}, At: time.Now()})

	l, err := s.ledger()
	if err != nil {
		t.Fatal(err)
	}
	if err := l.MarkIssued(migration.ServiceTokenKey("payments", "prd", "ci-deploy"), "slug-1", nil, false, time.Now()); err != nil {
		t.Fatal(err)
	}
	s.cutoverPlanned = &migration.CutoverPlan{}
	// Finished: cutover had nothing to apply, and Finish Migration recorded it.
	_ = s.history().RecordCutover(time.Now())
	return s, h
}

func TestFinalReportSaysWhatHappened(t *testing.T) {
	_, h := reportServer(t)

	rec := getPath(t, h, "/api/v1/report/final.json")
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Header().Get("Content-Disposition"), "migration-report-") {
		t.Errorf("should download, got %q", rec.Header().Get("Content-Disposition"))
	}
	var r finalReport
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if r.Status != "Migration complete" || r.StatusTone != "good" {
		t.Errorf("verdict %q/%q", r.Status, r.StatusTone)
	}
	if r.Cutover.State != "not_needed" {
		t.Errorf("cutover %q: a plan with nothing to mirror is not needed, not unrun", r.Cutover.State)
	}
	if len(r.Runs) != 1 || r.Source.ProjectCount != 4 {
		t.Errorf("runs %+v, projects %d", r.Runs, r.Source.ProjectCount)
	}
	if r.Tokens.Counts.Issued != 1 || len(r.Tokens.Records) != 1 || r.Tokens.Records[0].Disposition != "Issued" {
		t.Errorf("tokens %+v", r.Tokens)
	}
}

func TestFinalReportHTMLIsAPageToKeep(t *testing.T) {
	_, h := reportServer(t)

	rec := getPath(t, h, "/api/v1/report/final.html")
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
		t.Errorf("content type %q", rec.Header().Get("Content-Type"))
	}
	for _, want := range []string{"Migration complete", "ci-deploy", "payments/prd", "Acme &lt;Prod&gt;", "Not needed"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	// Self-contained: nothing fetched from anywhere when it is opened later.
	for _, bad := range []string{"<script", "<link", "src=", "url("} {
		if strings.Contains(body, bad) {
			t.Errorf("the page should load nothing: found %q", bad)
		}
	}
}

func TestFinalReportVerdicts(t *testing.T) {
	base := func() finalReport {
		return finalReport{
			Runs:    []reportRun{{Mode: "full"}},
			Cutover: reportCutover{State: "not_needed"},
		}
	}
	cases := []struct {
		name string
		edit func(*finalReport)
		want string
		tone string
	}{
		{"nothing ran", func(r *finalReport) { r.Runs = nil }, "No migration has run yet", "warn"},
		{"partial only", func(r *finalReport) { r.Cutover.State = "blocked" }, "Partial migration only — a full migration has not run", "warn"},
		{"open errors", func(r *finalReport) { r.Errors.Open = 5 }, "Finished with 5 open errors", "bad"},
		{"cutover left the migrator in", func(r *finalReport) {
			r.Cutover = reportCutover{State: "ran", Result: &migration.CutoverResult{SelfTrustOutstanding: []string{"api/prd"}}}
		}, "Cutover did not finish", "bad"},
		{"cutover not run", func(r *finalReport) { r.Cutover.State = "not_run" }, "Migrated — cutover has not run", "warn"},
		{"nothing to apply, not finished", func(r *finalReport) { r.Cutover.State = "ready_to_finish" }, "Migrated — ready to finish", "warn"},
		{"a token undecided", func(r *finalReport) { r.Tokens.Counts.Pending = 1 }, "Migrated — 1 service token is still undecided", "warn"},
		{"done", func(*finalReport) {}, "Migration complete", "good"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := base()
			tc.edit(&r)
			if got, tone := reportVerdict(r); got != tc.want || tone != tc.tone {
				t.Errorf("got %q/%q, want %q/%q", got, tone, tc.want, tc.tone)
			}
		})
	}
}
