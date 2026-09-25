package migration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/doppler/onprem-migrator/internal/doppler"
)

func ts(s string) *string { return &s }

func at(t *testing.T, s string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("bad time in test: %v", err)
	}
	return parsed.UTC()
}

func tempLedger(t *testing.T) *TokenLedger {
	t.Helper()
	return NewTokenLedger(filepath.Join(t.TempDir(), "service-tokens.json"))
}

func seeded(t *testing.T, l *TokenLedger, now time.Time) {
	t.Helper()
	l.Seed(&MigrationData{
		ServiceTokens: []doppler.ServiceToken{
			{Project: "payments", Config: "prd", Environment: "production", Name: "ci-deploy", Access: "read"},
			{Project: "checkout", Config: "stg", Environment: "staging", Name: "render-stg", Access: "read", ExpiresAt: ts("2029-01-14T00:00:00Z")},
			{Project: "auth", Config: "dev", Environment: "development", Name: "legacy-agent", Access: "read", ExpiresAt: ts("2026-05-30T00:00:00Z")},
		},
		ServiceAccounts: []doppler.ServiceAccount{{Slug: "platform-automation", Name: "Platform Automation"}},
		ServiceAccountTokens: []doppler.ServiceAccountToken{
			{ServiceAccount: "platform-automation", Name: "terraform-runner"},
		},
	}, now)
}

func TestClampExpiry(t *testing.T) {
	now := at(t, "2026-09-18T12:00:00Z")

	t.Run("no expiry stays no expiry", func(t *testing.T) {
		exp, shortened, expired := ClampExpiry(nil, now)
		if exp != nil || shortened || expired {
			t.Fatalf("got %v %v %v, want nil false false", exp, shortened, expired)
		}
	})

	t.Run("a reproducible expiry is passed through untouched", func(t *testing.T) {
		src := at(t, "2027-01-01T00:00:00Z")
		exp, shortened, expired := ClampExpiry(&src, now)
		if expired || shortened {
			t.Fatalf("shortened=%v expired=%v, want both false", shortened, expired)
		}
		if !exp.Equal(src) {
			t.Fatalf("got %v, want the source value %v", exp, src)
		}
	})

	t.Run("beyond the cap is shortened, never lengthened", func(t *testing.T) {
		src := at(t, "2029-01-14T00:00:00Z")
		exp, shortened, expired := ClampExpiry(&src, now)
		if expired || !shortened {
			t.Fatalf("shortened=%v expired=%v, want true false", shortened, expired)
		}
		// The safe direction: the new token must die before the source one would.
		if !exp.Before(src) {
			t.Fatalf("clamped to %v, which is not sooner than the source %v", exp, src)
		}
		if want := now.Add(maxTokenLifetime); !exp.Equal(want) {
			t.Fatalf("got %v, want the one-year cap %v", exp, want)
		}
	})

	t.Run("already expired is reported rather than clamped forward", func(t *testing.T) {
		// Clamping a dead token forward would hand out access the source no
		// longer grants, which is the one outcome that must not happen quietly.
		src := at(t, "2026-05-30T00:00:00Z")
		exp, _, expired := ClampExpiry(&src, now)
		if !expired {
			t.Fatal("want expired=true for a source expiry in the past")
		}
		if exp != nil {
			t.Fatalf("want no expiry offered for a dead token, got %v", exp)
		}
	})
}

func TestSeedClosesExpiredTokensAutomatically(t *testing.T) {
	// Left pending, an already-expired token could never be issued and never be
	// decided, so it would deadlock the cutover gate against a row with no
	// usable action.
	l := tempLedger(t)
	seeded(t, l, at(t, "2026-09-18T12:00:00Z"))

	rec, ok := l.Get(ServiceTokenKey("auth", "dev", "legacy-agent"))
	if !ok {
		t.Fatal("the expired token was not seeded at all")
	}
	if rec.Disposition != DispNotNeeded {
		t.Fatalf("got %q, want %q", rec.Disposition, DispNotNeeded)
	}
	if rec.Note == "" {
		t.Fatal("a row that closed itself should say why")
	}
	if l.Counts().Pending != 3 {
		t.Fatalf("got %d pending, want the other three", l.Counts().Pending)
	}
}

func TestSeedLeavesExistingDecisionsAlone(t *testing.T) {
	// The whole point of persisting: a resumed session must not offer to issue
	// a token that already exists on the target.
	now := at(t, "2026-09-18T12:00:00Z")
	l := tempLedger(t)
	seeded(t, l, now)

	key := ServiceTokenKey("payments", "prd", "ci-deploy")
	if err := l.MarkIssued(key, "slug-1", nil, false, now); err != nil {
		t.Fatalf("MarkIssued: %v", err)
	}

	seeded(t, l, now) // a second gather

	rec, _ := l.Get(key)
	if rec.Disposition != DispIssued {
		t.Fatalf("re-seeding reset the row to %q", rec.Disposition)
	}
	if rec.TargetSlug != "slug-1" {
		t.Fatalf("re-seeding lost the target slug, got %q", rec.TargetSlug)
	}
}

func TestGateCountsAndSkipAll(t *testing.T) {
	now := at(t, "2026-09-18T12:00:00Z")
	l := tempLedger(t)
	seeded(t, l, now)

	if c := l.Counts(); c.Pending != 3 || c.Handled() != 1 {
		t.Fatalf("got pending=%d handled=%d, want 3 and 1", c.Pending, c.Handled())
	}

	if moved := l.SkipAllPending(); moved != 3 {
		t.Fatalf("got %d moved, want 3", moved)
	}

	c := l.Counts()
	if c.Pending != 0 {
		t.Fatalf("got %d still pending after skip-all", c.Pending)
	}
	if c.ToRecreate != 3 {
		t.Fatalf("got %d to recreate, want 3", c.ToRecreate)
	}
	// Skipping is handling: a workplace that issues nothing must still get through.
	if c.Handled() != c.Total {
		t.Fatalf("got %d of %d handled, want everything", c.Handled(), c.Total)
	}
}

func TestUndoRefusesAnIssuedToken(t *testing.T) {
	// An issued token exists on the target. Returning the row to undecided would
	// invite a second one with the same name.
	now := at(t, "2026-09-18T12:00:00Z")
	l := tempLedger(t)
	seeded(t, l, now)

	key := ServiceTokenKey("payments", "prd", "ci-deploy")
	if err := l.MarkIssued(key, "slug-1", nil, false, now); err != nil {
		t.Fatalf("MarkIssued: %v", err)
	}

	if err := l.Undo(key); err == nil {
		t.Fatal("want undo to be refused for an issued token")
	}

	if err := l.MarkSkipped(ServiceTokenKey("checkout", "stg", "render-stg"), false); err != nil {
		t.Fatalf("MarkSkipped: %v", err)
	}
	if err := l.Undo(ServiceTokenKey("checkout", "stg", "render-stg")); err != nil {
		t.Fatalf("want undo to work on a skip, got %v", err)
	}
}

func TestCSVIsAWorklistNotAnInventory(t *testing.T) {
	now := at(t, "2026-09-18T12:00:00Z")
	l := tempLedger(t)
	seeded(t, l, now)

	_ = l.MarkIssued(ServiceTokenKey("payments", "prd", "ci-deploy"), "slug-1", nil, false, now)
	_ = l.MarkSkipped(ServiceTokenKey("checkout", "stg", "render-stg"), false)

	out := l.CSV()

	if !strings.Contains(out, "render-stg") {
		t.Error("a token to recreate by hand must appear on the worklist")
	}
	if !strings.Contains(out, "ci-deploy") {
		t.Error("an issued token should stay in as the record of what was done")
	}
	// Deliberately dropped is not work, and padding the list with it makes the
	// list less useful to whoever has to work through it.
	if strings.Contains(out, "legacy-agent") {
		t.Error("a not-needed token must be kept off the worklist")
	}
}

func TestLedgerSurvivesAReload(t *testing.T) {
	now := at(t, "2026-09-18T12:00:00Z")
	path := filepath.Join(t.TempDir(), "service-tokens.json")

	first := NewTokenLedger(path)
	seeded(t, first, now)
	_ = first.MarkIssued(ServiceTokenKey("payments", "prd", "ci-deploy"), "slug-1", nil, false, now)

	second := NewTokenLedger(path)
	if err := second.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	rec, ok := second.Get(ServiceTokenKey("payments", "prd", "ci-deploy"))
	if !ok {
		t.Fatal("the reloaded ledger lost the row")
	}
	if rec.Disposition != DispIssued || rec.TargetSlug != "slug-1" {
		t.Fatalf("got %q / %q after reload", rec.Disposition, rec.TargetSlug)
	}
}

func TestLoadOfAMissingFileIsNotAnError(t *testing.T) {
	l := NewTokenLedger(filepath.Join(t.TempDir(), "nope.json"))
	if err := l.Load(); err != nil {
		t.Fatalf("a first run has no ledger yet; got %v", err)
	}
}

// The security property the rest of the design rests on: a value is returned
// once and is never written down. This exercises the real create path against a
// stub target and then reads the file back off disk.
func TestIssuedValueNeverReachesDisk(t *testing.T) {
	const value = "dp.st.prd.7Kq2mXvR9tLpA4wZnB6yF3sHdE8jCuVgQx1oYi5rNk"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token": map[string]any{"name": "ci-deploy", "slug": "slug-1", "key": value, "access": "read"},
		})
	}))
	defer srv.Close()

	now := at(t, "2026-09-18T12:00:00Z")
	path := filepath.Join(t.TempDir(), "service-tokens.json")
	l := NewTokenLedger(path)
	seeded(t, l, now)

	iss := NewTokenIssuer(doppler.NewClient(srv.URL, "dp.ct.test"), l)
	issued, err := iss.Issue(context.Background(), ServiceTokenKey("payments", "prd", "ci-deploy"))
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	// It must reach the caller — that is the entire point of the feature.
	if issued.Value != value {
		t.Fatalf("the value did not reach the caller: got %q", issued.Value)
	}

	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	if strings.Contains(string(onDisk), value) {
		t.Fatal("the token value was written to the ledger file")
	}
	// The distinctive tail on its own, in case anything stored a fragment.
	if strings.Contains(string(onDisk), "7Kq2mXvR") {
		t.Fatal("part of the token value was written to the ledger file")
	}

	// And the row is now issued, carrying only identifiers.
	rec, _ := l.Get(ServiceTokenKey("payments", "prd", "ci-deploy"))
	if rec.Disposition != DispIssued || rec.TargetSlug != "slug-1" {
		t.Fatalf("got %q / %q", rec.Disposition, rec.TargetSlug)
	}
}

func TestIssueFailureLeavesTheRowUndecided(t *testing.T) {
	// A failure is not a decision. If a 403 closed the row, cutover would let
	// itself run against a token nobody chose to abandon.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"messages":["Your IP address is not allowed to access this config"]}`))
	}))
	defer srv.Close()

	now := at(t, "2026-09-18T12:00:00Z")
	l := tempLedger(t)
	seeded(t, l, now)

	iss := NewTokenIssuer(doppler.NewClient(srv.URL, "dp.ct.test"), l)
	key := ServiceTokenKey("payments", "prd", "ci-deploy")
	if _, err := iss.Issue(context.Background(), key); err == nil {
		t.Fatal("want an error from a 403")
	}

	rec, _ := l.Get(key)
	if rec.Disposition != DispPending {
		t.Fatalf("got %q, want the row left pending", rec.Disposition)
	}
	if !strings.Contains(rec.Note, "trusted IPs") {
		t.Fatalf("the 403 should be explained, not left as a status code; got %q", rec.Note)
	}
}

func TestIssueRefusesWhenAlreadyIssued(t *testing.T) {
	now := at(t, "2026-09-18T12:00:00Z")
	l := tempLedger(t)
	seeded(t, l, now)

	key := ServiceTokenKey("payments", "prd", "ci-deploy")
	_ = l.MarkIssued(key, "slug-1", nil, false, now)

	iss := NewTokenIssuer(doppler.NewClient("http://127.0.0.1:1", "dp.ct.test"), l)
	_, err := iss.Issue(context.Background(), key)
	if err == nil || !strings.Contains(err.Error(), "reroll") {
		t.Fatalf("want a refusal pointing at reroll, got %v", err)
	}
}

func TestServiceAccountTokenRoundTrip(t *testing.T) {
	const value = "dp.sa.Wm4bTz8CqL2nYkR7vJ5xHgP9dSfA3eU6oN1iBrXt"

	// The target's copy of the account has its own slug. Issuing under the
	// source's is a 404: "Service account does not exist".
	var createdUnder string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/v3/workplace/service_accounts" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"service_accounts": []map[string]string{
					{"name": "Someone Else", "slug": "tgt-other"},
					{"name": "Platform Automation", "slug": "tgt-7f3a"},
				},
			})
			return
		}
		createdUnder = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{
			"api_token": map[string]any{"name": "terraform-runner", "slug": "sa-tok-1"},
			"api_key":   value,
		})
	}))
	defer srv.Close()

	now := at(t, "2026-09-18T12:00:00Z")
	path := filepath.Join(t.TempDir(), "service-tokens.json")
	l := NewTokenLedger(path)
	seeded(t, l, now)

	iss := NewTokenIssuer(doppler.NewClient(srv.URL, "dp.ct.test"), l)
	issued, err := iss.Issue(context.Background(), ServiceAccountTokenKey("platform-automation", "terraform-runner"))
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if issued.Value != value {
		t.Fatalf("got %q, want the api_key", issued.Value)
	}
	if !strings.Contains(createdUnder, "/tgt-7f3a/") {
		t.Fatalf("created under %q, want the target's slug for the account", createdUnder)
	}
	if issued.Scope != "Platform Automation" {
		t.Fatalf("scope %q, want the account's name", issued.Scope)
	}

	onDisk, _ := os.ReadFile(path)
	if strings.Contains(string(onDisk), value) {
		t.Fatal("the service account token value was written to the ledger file")
	}
}

func TestExpiryShorteningIsRecordedOnTheRow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token": map[string]any{"name": "render-stg", "slug": "slug-2", "key": "dp.st.stg.x"},
		})
	}))
	defer srv.Close()

	l := tempLedger(t)
	seeded(t, l, at(t, "2026-09-18T12:00:00Z"))

	iss := NewTokenIssuer(doppler.NewClient(srv.URL, "dp.ct.test"), l)
	issued, err := iss.Issue(context.Background(), ServiceTokenKey("checkout", "stg", "render-stg"))
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if !issued.Shortened {
		t.Fatal("a 2029 expiry cannot be reproduced and the caller should be told")
	}

	rec, _ := l.Get(ServiceTokenKey("checkout", "stg", "render-stg"))
	if !rec.ExpiryShortened {
		t.Fatal("the shortening should survive on the row, not only in the response")
	}
	if !strings.Contains(rec.Note, "shortened") {
		t.Fatalf("got note %q", rec.Note)
	}
}

func TestServiceAccountTokenNeedsTheAccountOnTheTarget(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"service_accounts": []any{}})
	}))
	defer srv.Close()

	l := tempLedger(t)
	seeded(t, l, at(t, "2026-09-18T12:00:00Z"))
	iss := NewTokenIssuer(doppler.NewClient(srv.URL, "dp.ct.test"), l)
	_, err := iss.Issue(context.Background(), ServiceAccountTokenKey("platform-automation", "terraform-runner"))
	if err == nil || !strings.Contains(err.Error(), `"Platform Automation" is not on the target`) {
		t.Fatalf("want the missing account named, got %v", err)
	}
}

func TestSeedDropsTokensOfTheExcludedAccount(t *testing.T) {
	now := at(t, "2026-09-18T12:00:00Z")
	l := tempLedger(t)
	seeded(t, l, now) // an earlier gather, before the account was left out

	data := &MigrationData{
		ExcludedServiceAccount: &doppler.ServiceAccount{Slug: "platform-automation", Name: "Platform Automation"},
	}
	l.Seed(data, now)

	if _, ok := l.Get(ServiceAccountTokenKey("platform-automation", "terraform-runner")); ok {
		t.Fatal("a pending token of the excluded account should be dropped")
	}
	if _, ok := l.Get(ServiceTokenKey("payments", "prd", "ci-deploy")); !ok {
		t.Fatal("other rows should stay")
	}
}

func TestSeedBackfillsAccountNames(t *testing.T) {
	now := at(t, "2026-09-18T12:00:00Z")
	l := tempLedger(t)
	l.Seed(&MigrationData{ServiceAccountTokens: []doppler.ServiceAccountToken{
		{ServiceAccount: "platform-automation", Name: "terraform-runner"},
	}}, now)
	seeded(t, l, now)

	rec, _ := l.Get(ServiceAccountTokenKey("platform-automation", "terraform-runner"))
	if rec.ServiceAccountName != "Platform Automation" {
		t.Fatalf("got %q", rec.ServiceAccountName)
	}
}

func TestAFailureFromBeforeTheProjectWasMigratedIsCleared(t *testing.T) {
	// The report: an attempt made before the project existed on the target
	// ("does not have access to requested project") stayed on the row after
	// the project was migrated, reading as a failure of the token now.
	l := tempLedger(t)
	seeded(t, l, at(t, "2026-09-18T12:00:00Z"))
	stale := ServiceTokenKey("payments", "prd", "ci-deploy")
	recent := ServiceTokenKey("checkout", "stg", "render-stg")
	_ = l.MarkFailed(stale, "This token does not have access to requested project 'payments'")

	migrated := time.Now().UTC().Add(time.Second) // migrated after that attempt
	_ = l.MarkFailed(recent, "trusted IPs refused")
	later := migrated.Add(time.Hour)
	l.update(recent, func(r *TokenRecord) { r.FailedAt = &later }) // failed after its project was migrated

	l.ClearStaleFailures(func(r TokenRecord) time.Time {
		if r.Project == "payments" || r.Project == "checkout" {
			return migrated
		}
		return time.Time{}
	})

	if rec, _ := l.Get(stale); rec.Note != "" || rec.FailedAt != nil {
		t.Errorf("the stale failure should be cleared: %+v", rec)
	}
	if rec, _ := l.Get(recent); rec.Note == "" {
		t.Error("a failure since the migration is still true and should stay")
	}
}

func TestALaterTokenComesBackWithTheNextRun(t *testing.T) {
	l := tempLedger(t)
	seeded(t, l, at(t, "2026-09-18T12:00:00Z"))
	key := ServiceTokenKey("payments", "prd", "ci-deploy")

	if err := l.MarkDeferred(key); err != nil {
		t.Fatal(err)
	}
	c := l.Counts()
	if c.Deferred != 1 {
		t.Fatalf("counts %+v", c)
	}
	for _, rec := range l.Pending() {
		if rec.Key == key {
			t.Error("set aside, it is not outstanding now")
		}
	}
	if DispDeferred.Handled() {
		t.Error("set aside is not decided: cutover waits on it")
	}

	if n := l.ResurfaceDeferred(); n != 1 {
		t.Fatalf("resurfaced %d", n)
	}
	if rec, _ := l.Get(key); rec.Disposition != DispPending {
		t.Errorf("after the next run it is undecided again: %s", rec.Disposition)
	}
}

func TestAnIssuedTokenCannotBeSetAside(t *testing.T) {
	l := tempLedger(t)
	seeded(t, l, at(t, "2026-09-18T12:00:00Z"))
	key := ServiceTokenKey("payments", "prd", "ci-deploy")
	_ = l.MarkIssued(key, "slug-1", nil, false, time.Now())
	if err := l.MarkDeferred(key); err == nil {
		t.Fatal("an issued token is done; there is nothing to defer")
	}
}
