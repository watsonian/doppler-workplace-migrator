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

func decodeStandby(t *testing.T, body []byte) standbyResponse {
	t.Helper()
	var out standbyResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	return out
}

func TestChoosingToWaitAfterAFullMigration(t *testing.T) {
	s, h := scopeServer(t)

	if rec := postJSON(t, h, "/api/v1/standby", map[string]string{"action": "wait"}); rec.Code != http.StatusConflict {
		t.Fatalf("before any full run: %d %s", rec.Code, rec.Body.String())
	}

	_ = s.history().Record(migration.ScopeRun{Mode: migration.ScopeFull, Projects: []string{"payments"}, At: time.Now()})
	rec := postJSON(t, h, "/api/v1/standby", map[string]string{"action": "wait"})
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	out := decodeStandby(t, rec.Body.Bytes())
	if !out.Waiting || out.FullAt == nil || len(out.Runs) != 1 {
		t.Fatalf("%+v", out)
	}

	// The next full-scope run is a top-up, and the scope says which.
	scope := getScope(t, h)
	if scope.TopUp == nil || scope.TopUp.Number != 1 || scope.TopUp.Since == nil {
		t.Fatalf("top-up %+v", scope.TopUp)
	}
}

func TestFinishingNeedsACutoverWithNothingToDo(t *testing.T) {
	s, h := scopeServer(t)
	_ = s.history().Record(migration.ScopeRun{Mode: migration.ScopeFull, Projects: []string{"payments"}, At: time.Now()})

	if rec := postJSON(t, h, "/api/v1/standby", map[string]string{"action": "finish"}); rec.Code != http.StatusConflict {
		t.Fatalf("with no cutover plan: %d", rec.Code)
	}
	s.cutoverPlanned = &migration.CutoverPlan{TrustedIPs: []migration.TrustedIPWork{{Scope: "api/prd"}}}
	if rec := postJSON(t, h, "/api/v1/standby", map[string]string{"action": "finish"}); rec.Code != http.StatusConflict {
		t.Fatalf("with ranges to mirror: %d", rec.Code)
	}

	s.cutoverPlanned = &migration.CutoverPlan{}
	rec := postJSON(t, h, "/api/v1/standby", map[string]string{"action": "finish"})
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if out := decodeStandby(t, rec.Body.Bytes()); out.CutoverAt == nil || out.Waiting {
		t.Fatalf("%+v", out)
	}
}

func TestANoChangeCheckIsRecordedAsATopUp(t *testing.T) {
	s, h := scopeServer(t)
	s.config.DevMode = false
	_ = s.history().Record(migration.ScopeRun{Mode: migration.ScopeFull, Projects: []string{"payments"}, At: time.Now()})
	_ = s.history().SetWaiting(true, time.Now())

	s.diff = &migration.Diff{Matched: map[migration.ResourceKind]int{migration.KindSecret: 40}}
	rec := postJSON(t, h, "/api/v1/standby/check", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	out := decodeStandby(t, rec.Body.Bytes())
	if out.TopUps != 1 || out.Runs[0].Kind != migration.RunKindTopUp || out.Runs[0].Updated != 0 {
		t.Fatalf("%+v", out.Runs)
	}

	s.diff = &migration.Diff{Differences: []migration.Difference{{Kind: migration.KindSecret, Disposition: migration.DispositionUpdate}}, Matched: map[migration.ResourceKind]int{}}
	if rec := postJSON(t, h, "/api/v1/standby/check", nil); rec.Code != http.StatusConflict {
		t.Fatalf("a comparison with changes should run the top-up, not be recorded: %d", rec.Code)
	}
}

func TestOnlyOneMigrationAtATime(t *testing.T) {
	s, h := scopeServer(t)
	s.config.SourceHost, s.config.TargetHost = "https://api.doppler.com", "https://doppler.internal.example.com"
	_ = s.history().Record(migration.ScopeRun{Mode: migration.ScopeFull, Projects: []string{"payments"}, At: time.Now()})

	// A partial run would narrow the next top-up to a few projects.
	rec := postJSON(t, h, "/api/v1/scope", migration.Scope{Mode: migration.ScopePartial, Projects: []string{"payments"}})
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "waiting to cut over") {
		t.Fatalf("partial while in flight: %d %s", rec.Code, rec.Body.String())
	}

	configure := func(target string) *httptest.ResponseRecorder {
		return postJSON(t, h, "/api/v1/configure", configureRequest{
			SourceHost: "https://api.doppler.com", SourceToken: tokenPtr("dp.ct.source123"),
			TargetHost: target, TargetToken: tokenPtr("dp.pt.target456"),
		})
	}
	// Another target would overwrite this migration's history.
	if rec := configure("https://another.example.com"); rec.Code != http.StatusConflict ||
		!strings.Contains(rec.Body.String(), "still in progress") {
		t.Fatalf("other instances while in flight: %d %s", rec.Code, rec.Body.String())
	}
	// The same pair is carrying on, which is what a restart needs.
	if rec := configure("https://doppler.internal.example.com"); rec.Code != http.StatusOK {
		t.Fatalf("same instances: %d %s", rec.Code, rec.Body.String())
	}

	// After cutover it is over, and another may start.
	_ = s.history().RecordCutover(time.Now())
	if rec := configure("https://another.example.com"); rec.Code != http.StatusOK {
		t.Fatalf("after cutover: %d %s", rec.Code, rec.Body.String())
	}
}

func TestRunsCarryWhatTheyChanged(t *testing.T) {
	s, h := scopeServer(t)
	changes := &migration.RunChanges{}
	changes.Add("Secrets", "updated", "payments/prd · STRIPE_KEY")
	_ = s.history().Record(migration.ScopeRun{Mode: migration.ScopeFull, Projects: []string{"payments"}, At: time.Now(), Changes: changes})

	out := decodeStandby(t, getPath(t, h, "/api/v1/standby").Body.Bytes())
	c := out.Runs[0].Changes
	if c == nil || len(c.Groups) != 1 || c.Groups[0].Updated != 1 || c.Groups[0].Items[0].Item != "payments/prd · STRIPE_KEY" {
		t.Fatalf("%+v", c)
	}
}

func TestWelcomeCanSeeAWaitingMigrationBeforeConfigure(t *testing.T) {
	s, h := scopeServer(t)
	_ = s.history().Record(migration.ScopeRun{Mode: migration.ScopeFull, Projects: []string{"payments"}, At: time.Now()})
	_ = s.history().SetWaiting(true, time.Now())
	s.config = nil

	rec := getPath(t, h, "/api/v1/standby")
	out := decodeStandby(t, rec.Body.Bytes())
	if out.Configured || out.WaitingSince == nil || !strings.Contains(out.TargetHost, "tgt") {
		t.Fatalf("%+v", out)
	}
}

func TestAWaitingMigrationResumesWithTheEnvironmentsTokens(t *testing.T) {
	// After a restart the waiting page asked for tokens the environment was
	// already providing.
	t.Setenv("CHECKPOINT_DIR", t.TempDir())
	t.Setenv("SOURCE_DOPPLER_TOKEN", "dp.sa.source123")
	t.Setenv("TARGET_DOPPLER_TOKEN", "dp.sa.target456")

	// The migration as the last session left it.
	h, _ := migration.LoadScopeHistory(scopeHistoryPath(), "https://api.doppler.com", "https://doppler.internal.example.com")
	_ = h.Record(migration.ScopeRun{Mode: migration.ScopeFull, Projects: []string{"payments"}, At: time.Now()})
	_ = h.RememberSettings(migration.RunSettings{MigrateWorkplaceName: true, StopOnNewErrorClass: false})
	_ = h.SetWaiting(true, time.Now())

	s := NewServer()
	if !s.ResumeFromEnvironment() {
		t.Fatal("should resume")
	}
	cfg := s.config
	if cfg.TargetHost != "https://doppler.internal.example.com" || cfg.TargetToken != "dp.sa.target456" ||
		!cfg.MigrateWorkplaceName || cfg.StopOnNewErrorClass {
		t.Fatalf("config %+v", cfg)
	}
	out := decodeStandby(t, getPath(t, newRouterWithServer(s, nil), "/api/v1/standby").Body.Bytes())
	if !out.Configured || !out.InFlight {
		t.Fatalf("%+v", out)
	}
}

func TestNothingResumesWithoutBothTokensOrAMigrationInFlight(t *testing.T) {
	t.Setenv("CHECKPOINT_DIR", t.TempDir())
	t.Setenv("SOURCE_DOPPLER_TOKEN", "dp.sa.source123")
	t.Setenv("TARGET_DOPPLER_TOKEN", "")
	h, _ := migration.LoadScopeHistory(scopeHistoryPath(), "https://api.doppler.com", "https://doppler.internal.example.com")
	_ = h.Record(migration.ScopeRun{Mode: migration.ScopeFull, Projects: []string{"payments"}, At: time.Now()})

	if NewServer().ResumeFromEnvironment() {
		t.Fatal("one token is not enough")
	}

	t.Setenv("TARGET_DOPPLER_TOKEN", "dp.sa.target456")
	_ = h.RecordCutover(time.Now())
	if NewServer().ResumeFromEnvironment() {
		t.Fatal("a migration that is cut over is not in flight")
	}
}

// A partial run of one project offered to issue tokens for every project,
// and issuing one for a project not on the target failed with a 400.
func TestTheTokenPageOffersOnlyWhatHasBeenMigrated(t *testing.T) {
	s, h := scopeServer(t)
	s.config.DevMode = false
	data := s.migrationData
	data.Projects[0].Members = []doppler.ProjectMember{{Type: "service_account", Slug: "sa-used", Role: doppler.MemberRole{Identifier: "viewer"}}}
	data.ServiceAccounts = []doppler.ServiceAccount{{Slug: "sa-used", Name: "Used"}, {Slug: "sa-other", Name: "Other"}}
	data.ServiceTokens = []doppler.ServiceToken{
		{Project: "payments", Config: "prd", Name: "pay-ci", Access: "read"},
		{Project: "checkout", Config: "prd", Name: "checkout-ci", Access: "read"},
	}
	data.ServiceAccountTokens = []doppler.ServiceAccountToken{
		{ServiceAccount: "sa-used", Name: "used-token"},
		{ServiceAccount: "sa-other", Name: "other-token"},
	}

	// Seeded before any scope was chosen — every token lands in the ledger.
	if _, err := s.ledger(); err != nil {
		t.Fatal(err)
	}
	_ = s.history().Record(migration.ScopeRun{Mode: migration.ScopePartial, Projects: []string{"payments"}, At: time.Now()})

	var out struct {
		Records []migration.TokenRecord `json:"records"`
		Counts  migration.TokenCounts   `json:"counts"`
	}
	if err := json.Unmarshal(getPath(t, h, "/api/v1/tokens").Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, r := range out.Records {
		names[r.Name] = true
	}
	if !names["pay-ci"] || !names["used-token"] || names["checkout-ci"] || names["other-token"] || out.Counts.Total != 2 {
		t.Fatalf("records %v, counts %+v; want the migrated project's token and the account it uses", names, out.Counts)
	}

	rec := postJSON(t, h, "/api/v1/tokens/issue", map[string]string{"key": migration.ServiceTokenKey("checkout", "prd", "checkout-ci")})
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "not been migrated yet") {
		t.Fatalf("issuing for an unmigrated project: %d %s", rec.Code, rec.Body.String())
	}

	// A full run later brings everything into scope, decisions intact.
	_ = s.history().Record(migration.ScopeRun{Mode: migration.ScopeFull, Projects: []string{"payments", "checkout", "search", "billing"}, At: time.Now()})
	_ = json.Unmarshal(getPath(t, h, "/api/v1/tokens").Body.Bytes(), &out)
	if out.Counts.Total != 4 {
		t.Fatalf("after the full run: %+v", out.Counts)
	}
}

func TestATokenSetAsideStillHoldsUpCutover(t *testing.T) {
	s, h := scopeServer(t)
	s.migrationData.ServiceTokens = []doppler.ServiceToken{{Project: "payments", Config: "prd", Name: "pay-ci", Access: "read"}}
	_ = s.history().Record(migration.ScopeRun{Mode: migration.ScopeFull, Projects: []string{"payments", "checkout", "search", "billing"}, At: time.Now()})

	rec := postJSON(t, h, "/api/v1/tokens/defer", map[string]string{"key": migration.ServiceTokenKey("payments", "prd", "pay-ci")})
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Counts migration.TokenCounts `json:"counts"`
		Gate   tokenGate             `json:"gate"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Counts.Deferred != 1 || !out.Gate.Blocked || !strings.Contains(out.Gate.Reason, "set aside for later") {
		t.Fatalf("%+v", out)
	}
}
