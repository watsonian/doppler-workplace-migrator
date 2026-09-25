package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/doppleruniversity/workplace-migrator/internal/doppler"
	"github.com/doppleruniversity/workplace-migrator/internal/migration"
)

func listWaits(t *testing.T, h http.Handler) []rateWait {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newAPIRequest("GET", "/api/v1/rate-limits", nil))
	var out struct {
		Waits []rateWait `json:"waits"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out.Waits
}

func TestAPauseIsListedWhileItLasts(t *testing.T) {
	s := NewServer()
	h := newRouterWithServer(s, nil)
	rl := s.newLimiter("source")

	resume := time.Now().Add(80 * time.Millisecond)
	done := make(chan struct{})
	go func() {
		_ = rl.SleepUntil(context.Background(), doppler.RateLimitSecretRead, resume, true)
		close(done)
	}()
	time.Sleep(20 * time.Millisecond)

	waits := listWaits(t, h)
	if len(waits) != 1 {
		t.Fatalf("got %+v, want the one pause", waits)
	}
	w := waits[0]
	if w.Side != "source" || w.Category != doppler.RateLimitSecretRead || !w.Refused || !w.ResumeAt.Equal(resume) {
		t.Fatalf("got %+v — it has to say which side, what kind of request, and when it resumes", w)
	}

	<-done
	if waits := listWaits(t, h); len(waits) != 0 {
		t.Fatalf("the pause ended but is still listed: %+v", waits)
	}
}

func TestAPauseThatNeverReportedItsEndIsDropped(t *testing.T) {
	// A cancelled run can leave a pause behind without its closing event. Once
	// its resume time has passed it is not a pause any more.
	s := NewServer()
	s.rateWaits.set(rateWait{Side: "target", Category: doppler.RateLimitWrite, ResumeAt: time.Now().Add(-time.Second)}, true)
	if waits := listWaits(t, newRouterWithServer(s, nil)); len(waits) != 0 {
		t.Fatalf("got %+v", waits)
	}
}

func TestIssuingATokenAfterARestartDoesNotCrash(t *testing.T) {
	// The token handlers used the run's target limiter, which only exists once
	// a run has happened in this process. After a restart it was nil, and the
	// client's first request dereferenced it.
	t.Setenv("CHECKPOINT_DIR", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"token":{"name":"ci","slug":"s1","key":"dp.st.prd.VALUE"}}`))
	}))
	defer srv.Close()

	s := NewServer()
	s.config = &migration.MigrationConfig{SourceHost: "https://src", TargetHost: srv.URL, TargetToken: "dp.ct.x"}
	s.migrationData = &migration.MigrationData{
		ServiceTokens: []doppler.ServiceToken{{Project: "payments", Config: "prd", Name: "ci", Access: "read"}},
	}
	if s.targetLimiter != nil {
		t.Fatal("precondition: no run has created a limiter")
	}
	// The project was migrated by a run before the restart.
	_ = s.history().Record(migration.ScopeRun{Mode: migration.ScopeFull, Projects: []string{"payments"}, At: time.Now()})

	rec := postJSON(t, newRouterWithServer(s, nil), "/api/v1/tokens/issue",
		map[string]string{"key": migration.ServiceTokenKey("payments", "prd", "ci")})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "dp.st.prd.VALUE") {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
}

func TestRateStatusLevels(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	rs := &rateStatusStore{now: func() time.Time { return now }}
	reset := now.Add(time.Minute)

	level := func(side string) rateSideStatus {
		for _, st := range rs.snapshot() {
			if st.Side == side {
				return st
			}
		}
		t.Fatalf("no %s", side)
		return rateSideStatus{}
	}

	if got := level("source"); got.Level != "" {
		t.Fatalf("before any response: %+v", got)
	}

	rs.record("source", doppler.RateLimitStatus{Category: doppler.RateLimitRead, Remaining: 400, Limit: 480, ResetAt: reset})
	if got := level("source"); got.Level != "ok" {
		t.Errorf("17%% used: %+v", got)
	}
	rs.record("source", doppler.RateLimitStatus{Category: doppler.RateLimitWrite, Remaining: 20, Limit: 60, ResetAt: reset})
	if got := level("source"); got.Level != "ok" || got.Used < 0.66 {
		t.Errorf("writes 67%% used is under 70%%: %+v", got)
	}
	rs.record("source", doppler.RateLimitStatus{Category: doppler.RateLimitWrite, Remaining: 10, Limit: 60, ResetAt: reset})
	if got := level("source"); got.Level != "warn" {
		t.Errorf("83%% used is warn: %+v", got)
	}
	rs.record("source", doppler.RateLimitStatus{Category: doppler.RateLimitWrite, Remaining: 0, Limit: 60, ResetAt: reset})
	if got := level("source"); got.Level != "limited" || !got.ResetAt.Equal(reset) {
		t.Errorf("spent is limited, with its reset: %+v", got)
	}

	// Once the window resets, the budget is full again.
	now = reset.Add(time.Second)
	if got := level("source"); got.Level != "ok" {
		t.Errorf("after the reset: %+v", got)
	}
	if got := level("target"); got.Level != "" {
		t.Errorf("the other side is its own: %+v", got)
	}
}

func TestRateStatusIsPublishedOnChangeAndOtherwiseThrottled(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	rs := &rateStatusStore{now: func() time.Time { return now }}
	st := func(remaining int) doppler.RateLimitStatus {
		return doppler.RateLimitStatus{Category: doppler.RateLimitRead, Remaining: remaining, Limit: 100, ResetAt: now.Add(time.Minute)}
	}

	if !rs.record("target", st(90)) {
		t.Error("the first response is news")
	}
	if rs.record("target", st(89)) {
		t.Error("a response a moment later at the same level is throttled")
	}
	if !rs.record("target", st(20)) {
		t.Error("crossing into warn is published at once")
	}
	now = now.Add(rateStatusInterval)
	if !rs.record("target", st(19)) {
		t.Error("after the interval, the latest is published")
	}
	if rs.record("target", doppler.RateLimitStatus{Category: doppler.RateLimitRead}) {
		t.Error("a response without the headers says nothing")
	}
}
