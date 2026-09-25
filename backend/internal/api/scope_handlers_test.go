package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/doppleruniversity/workplace-migrator/internal/doppler"
	"github.com/doppleruniversity/workplace-migrator/internal/migration"
)

// scopeServer is a server with four gathered projects, its state kept in a
// temporary CHECKPOINT_DIR so the history and ledger files never land in the
// source tree.
func scopeServer(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	t.Setenv("CHECKPOINT_DIR", t.TempDir())

	s := NewServer()
	s.config = &migration.MigrationConfig{SourceHost: "https://src", TargetHost: "https://tgt", DevMode: true}

	project := func(slug string, secrets int) migration.ProjectData {
		return migration.ProjectData{
			Project: doppler.Project{Slug: slug, Name: strings.ToUpper(slug[:1]) + slug[1:]},
			Configs: []migration.ConfigData{{Config: doppler.Config{Name: "prd"}, SecretCount: secrets}},
		}
	}
	s.migrationData = &migration.MigrationData{
		Workplace: &doppler.Workplace{ID: "wp"},
		Projects: []migration.ProjectData{
			project("payments", 40), project("checkout", 12), project("search", 9), project("billing", 3),
		},
	}
	s.plan = migration.GeneratePlan(s.migrationData)
	return s, newRouterWithServer(s, nil)
}

func postJSON(t *testing.T, h http.Handler, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newAPIRequest("POST", path, bytes.NewReader(raw)))
	return rec
}

func getScope(t *testing.T, h http.Handler) scopeResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newAPIRequest("GET", "/api/v1/scope", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /scope: %d %s", rec.Code, rec.Body.String())
	}
	var out scopeResponse
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestScopeDefaultsToFull(t *testing.T) {
	_, h := scopeServer(t)
	out := getScope(t, h)
	if out.Scope.Mode != migration.ScopeFull || len(out.Run.Resolution.Projects) != 4 {
		t.Fatalf("got %s over %v", out.Scope.Mode, out.Run.Resolution.Projects)
	}
	if len(out.Projects) != 4 || out.Projects[0].Name != "Billing" {
		t.Fatalf("picker rows should be sorted by name: %+v", out.Projects)
	}
}

func TestScopeNeedsAGather(t *testing.T) {
	s, h := scopeServer(t)
	s.migrationData = nil
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newAPIRequest("GET", "/api/v1/scope", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d", rec.Code)
	}
}

func TestSettingAPartialScopeNarrowsThePlan(t *testing.T) {
	s, h := scopeServer(t)
	s.diff = &migration.Diff{}

	rec := postJSON(t, h, "/api/v1/scope", migration.Scope{Mode: migration.ScopePartial, Projects: []string{"payments"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /scope: %d %s", rec.Code, rec.Body.String())
	}

	if s.plan.Summary.ProjectCount != 1 {
		t.Errorf("the plan still counts %d projects — Review would describe the whole source", s.plan.Summary.ProjectCount)
	}
	if s.diff != nil {
		t.Error("a comparison of the old scope survived the change")
	}
	if got := getScope(t, h).Run.Resolution.Projects; strings.Join(got, ",") != "payments" {
		t.Errorf("scope did not stick: %v", got)
	}
}

func TestABadScopeIsRefusedAndNotStored(t *testing.T) {
	s, h := scopeServer(t)
	rec := postJSON(t, h, "/api/v1/scope", migration.Scope{Mode: migration.ScopePartial, Projects: []string{"nope"}})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d", rec.Code)
	}
	if s.scope != nil {
		t.Fatal("a refused scope became the current one")
	}
}

func TestScopeCannotChangeMidRun(t *testing.T) {
	s, h := scopeServer(t)
	s.migrating = true
	rec := postJSON(t, h, "/api/v1/scope", migration.Scope{Mode: migration.ScopeFull})
	if rec.Code != http.StatusConflict {
		t.Fatalf("got %d", rec.Code)
	}
}

func TestRunOptionsReachTheRun(t *testing.T) {
	// These used to travel as navigation state nothing read.
	s, h := scopeServer(t)
	rec := postJSON(t, h, "/api/v1/run-options", map[string]any{"delete_orphans": true, "drift": "keep_target"})
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d", rec.Code)
	}
	opts := s.currentRunOptions()
	if !opts.DeleteOrphans || opts.Drift != migration.DriftKeepTarget || opts.DeleteOrphanConfigs {
		t.Fatalf("got %+v", opts)
	}
}

func TestRunSplitsAroundWhatAPartialMoved(t *testing.T) {
	s, _ := scopeServer(t)
	at := time.Date(2026, 9, 21, 9, 0, 0, 0, time.UTC)
	if err := s.history().Record(migration.ScopeRun{Mode: migration.ScopePartial, Projects: []string{"payments", "checkout"}, At: at}); err != nil {
		t.Fatal(err)
	}

	p, err := s.planRun()
	if err != nil {
		t.Fatal(err)
	}
	// Stage one creates only what no run has covered; stage two tops up the rest.
	if strings.Join(p.Remaining, ",") != "billing,search" {
		t.Errorf("remaining: %v", p.Remaining)
	}
	if strings.Join(p.PreviouslyMigrated, ",") != "checkout,payments" {
		t.Errorf("previously migrated: %v", p.PreviouslyMigrated)
	}
	if p.PreviouslyMigratedAt == nil || !p.PreviouslyMigratedAt.Equal(at) {
		t.Errorf("previously migrated at: %v", p.PreviouslyMigratedAt)
	}
}

func TestALaterPartialCarriesTheEarlierProjects(t *testing.T) {
	s, h := scopeServer(t)
	_ = s.history().Record(migration.ScopeRun{Mode: migration.ScopePartial, Projects: []string{"payments"}, At: time.Now()})

	postJSON(t, h, "/api/v1/scope", migration.Scope{Mode: migration.ScopePartial, Projects: []string{"search"}})
	out := getScope(t, h)

	if strings.Join(out.Run.Resolution.Projects, ",") != "payments,search" {
		t.Fatalf("scope: %v — the earlier project should stay in", out.Run.Resolution.Projects)
	}
	for _, row := range out.Projects {
		if row.Slug == "payments" && row.MigratedAt == nil {
			t.Error("the picker should show payments as already migrated")
		}
	}
}

// fakeTargetTrustedIPs is a target on which every config has only the
// allow-all default, which is what a new config gets.
func fakeTargetTrustedIPs(t *testing.T) string {
	t.Helper()
	url, _ := fakeTargetWith(t, []string{"payments", "checkout", "search", "billing"})
	return url
}

// fakeTargetWith is a target holding the given projects, each with a prd
// config whose only range is the allow-all default. It records the projects
// whose trusted IPs were asked for.
func fakeTargetWith(t *testing.T, projects []string) (string, *[]string) {
	t.Helper()
	var mu sync.Mutex
	asked := &[]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v3/projects":
			list := []map[string]string{}
			for _, p := range projects {
				list = append(list, map[string]string{"slug": p, "name": p})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"projects": list})
		case "/v3/configs":
			_ = json.NewEncoder(w).Encode(map[string]any{"configs": []map[string]string{{"name": "prd"}}})
		case "/v3/configs/config/trusted_ips":
			mu.Lock()
			*asked = append(*asked, r.URL.Query().Get("project"))
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"ips": []string{"0.0.0.0/0"}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL, asked
}

func TestCutoverWaitsForAFullRun(t *testing.T) {
	s, h := scopeServer(t)
	s.config.DevMode = false
	s.config.TargetHost = fakeTargetTrustedIPs(t)
	s.snapshot = &migration.TargetSnapshot{Projects: map[string]migration.ProjectSnapshot{}}
	_ = s.history().Record(migration.ScopeRun{Mode: migration.ScopePartial, Projects: []string{"payments"}, At: time.Now()})

	dry := postJSON(t, h, "/api/v1/cutover", map[string]any{"dry_run": true})
	if !strings.Contains(dry.Body.String(), `"partial_only":true`) {
		t.Fatalf("dry run should report the block: %s", dry.Body.String())
	}

	real := postJSON(t, h, "/api/v1/cutover", map[string]any{})
	if real.Code != http.StatusConflict || !strings.Contains(real.Body.String(), "full migration") {
		t.Fatalf("got %d %s", real.Code, real.Body.String())
	}

	_ = s.history().Record(migration.ScopeRun{Mode: migration.ScopeFull, Projects: []string{"payments", "checkout", "search", "billing"}, At: time.Now()})
	after := postJSON(t, h, "/api/v1/cutover", map[string]any{"dry_run": true})
	if strings.Contains(after.Body.String(), `"partial_only":true`) {
		t.Fatal("a full run after the partial should lift the block")
	}
}

func TestADryRunRecordsNoHistory(t *testing.T) {
	// A dry run wrote nothing. Recording it would lock its projects into later
	// runs and block cutover over a migration that never happened.
	s, h := scopeServer(t)
	postJSON(t, h, "/api/v1/scope", migration.Scope{Mode: migration.ScopePartial, Projects: []string{"payments"}})

	rec := postJSON(t, h, "/api/v1/migrate", nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("migrate: %d %s", rec.Code, rec.Body.String())
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.mu.RLock()
		done := !s.migrating
		s.mu.RUnlock()
		if done || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if runs := s.history().Runs; len(runs) != 0 {
		t.Fatalf("dry run recorded history: %+v", runs)
	}
	if s.lastRun == nil || s.lastRun.Mode != migration.ScopePartial || s.lastRun.Stage != "done" {
		t.Fatalf("last run: %+v", s.lastRun)
	}
}

func TestPreviewResolvesWithoutStoring(t *testing.T) {
	s, h := scopeServer(t)
	rec := postJSON(t, h, "/api/v1/scope/preview", migration.Scope{Mode: migration.ScopePartial, Projects: []string{"search"}})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"projects":["search"]`) {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
	if s.scope != nil || s.plan.Summary.ProjectCount != 4 {
		t.Fatal("a preview changed the stored scope or the plan")
	}
}

// A partial run finished, then a full one chosen: the Migrate page read the
// partial's result from /status and showed the full run as already done.
func TestChoosingTheNextRunForgetsTheLastResult(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
		body any
	}{
		{"new scope", "/api/v1/scope", migration.Scope{Mode: migration.ScopeFull}},
		{"run options", "/api/v1/run-options", map[string]any{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, h := scopeServer(t)
			s.migrationResult = &migrateResponse{Status: "completed"}

			if rec := postJSON(t, h, tc.path, tc.body); rec.Code != http.StatusOK {
				t.Fatalf("%s: %d %s", tc.path, rec.Code, rec.Body.String())
			}

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, newAPIRequest("GET", "/api/v1/status", nil))
			if strings.Contains(rec.Body.String(), "migration_result") {
				t.Fatalf("status still reports the last run: %s", rec.Body.String())
			}
		})
	}
}

// The snapshot is from Compare, before the migration created anything. Planned
// from it, every new config read as having no ranges, and cutover offered to
// add the 0.0.0.0/0 default the target had already given them.
func TestCutoverPlansFromTheTargetAsItIsNow(t *testing.T) {
	s, h := scopeServer(t)
	s.config.DevMode = false
	s.config.TargetHost = fakeTargetTrustedIPs(t)
	s.snapshot = &migration.TargetSnapshot{Projects: map[string]migration.ProjectSnapshot{}} // taken before the migration
	for i := range s.migrationData.Projects {
		for j := range s.migrationData.Projects[i].Configs {
			s.migrationData.Projects[i].Configs[j].TrustedIPs = []doppler.TrustedIP{{IP: "0.0.0.0/0"}}
		}
	}

	rec := postJSON(t, h, "/api/v1/cutover", map[string]any{"dry_run": true})
	var out struct {
		Plan migration.CutoverPlan `json:"plan"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("%v: %s", err, rec.Body.String())
	}
	if len(out.Plan.TrustedIPs) != 0 {
		t.Fatalf("nothing should need mirroring, got %+v", out.Plan.TrustedIPs)
	}
	if len(out.Plan.Matching) != 4 || out.Plan.AlreadyMatching != 4 {
		t.Fatalf("all four configs match and should be named: %+v", out.Plan)
	}
}

// After a partial run most source projects are not on the target. Asking for
// their ranges was refused ("this token does not have access to requested
// project") and logged as a warning for every config, and the plan counted
// them as unreadable.
func TestCutoverAsksOnlyAboutWhatIsOnTheTarget(t *testing.T) {
	s, h := scopeServer(t)
	s.config.DevMode = false
	url, asked := fakeTargetWith(t, []string{"payments"})
	s.config.TargetHost = url
	for i := range s.migrationData.Projects {
		s.migrationData.Projects[i].Configs[0].TrustedIPs = []doppler.TrustedIP{{IP: "0.0.0.0/0"}}
	}

	rec := postJSON(t, h, "/api/v1/cutover", map[string]any{"dry_run": true})
	var out struct {
		Plan migration.CutoverPlan `json:"plan"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)

	if len(*asked) != 1 || (*asked)[0] != "payments" {
		t.Fatalf("asked about %v; only payments is on the target", *asked)
	}
	if len(out.Plan.Unreadable) != 0 || len(out.Plan.TrustedIPs) != 0 {
		t.Fatalf("missing projects are neither unreadable nor work: %+v", out.Plan)
	}
	if out.Plan.NotOnTarget != 3 || out.Plan.AlreadyMatching != 1 {
		t.Fatalf("plan %+v", out.Plan)
	}
}
