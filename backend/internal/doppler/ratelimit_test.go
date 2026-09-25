package doppler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRateLimiterUpdateParsesHeaders(t *testing.T) {
	rl := NewRateLimiter(nil)

	headers := http.Header{}
	headers.Set("X-Ratelimit-Remaining", "42")
	headers.Set("X-Ratelimit-Limit", "100")
	headers.Set("X-Ratelimit-Reset", "1700000000")

	rl.Update(RateLimitRead, headers)

	status := rl.Status()
	s := status[RateLimitRead]

	if s.Remaining != 42 {
		t.Errorf("expected remaining 42, got %d", s.Remaining)
	}
	if s.Limit != 100 {
		t.Errorf("expected limit 100, got %d", s.Limit)
	}
	if s.ResetAt != time.Unix(1700000000, 0) {
		t.Errorf("expected resetAt Unix 1700000000, got %v", s.ResetAt)
	}
}

func TestRateLimiterUpdateMissingHeaders(t *testing.T) {
	rl := NewRateLimiter(nil)

	// Empty headers should not panic or change state from zero values
	headers := http.Header{}
	rl.Update(RateLimitRead, headers)

	status := rl.Status()
	s := status[RateLimitRead]
	if s.Remaining != 0 || s.Limit != 0 {
		t.Errorf("expected zero state, got remaining=%d limit=%d", s.Remaining, s.Limit)
	}
}

func TestRateLimiterWaitReturnsImmediatelyWhenNotExhausted(t *testing.T) {
	rl := NewRateLimiter(nil)

	headers := http.Header{}
	headers.Set("X-Ratelimit-Remaining", "50")
	headers.Set("X-Ratelimit-Limit", "100")
	rl.Update(RateLimitRead, headers)

	ctx := context.Background()
	start := time.Now()
	err := rl.Wait(ctx, RateLimitRead)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if elapsed > 50*time.Millisecond {
		t.Errorf("Wait took too long: %v", elapsed)
	}
}

func TestRateLimiterWaitBlocksWhenExhausted(t *testing.T) {
	rl := NewRateLimiter(nil)

	now := time.Now()
	rl.nowFunc = func() time.Time { return now }

	// Set state to exhausted with reset 200ms in the future
	rl.mu.Lock()
	rl.limits[RateLimitWrite] = &RateLimitState{
		Remaining: 0,
		Limit:     60,
		ResetAt:   now.Add(200 * time.Millisecond),
	}
	rl.mu.Unlock()

	ctx := context.Background()
	start := time.Now()
	err := rl.Wait(ctx, RateLimitWrite)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should wait ~700ms (200ms + 500ms buffer)
	if elapsed < 600*time.Millisecond {
		t.Errorf("Wait returned too quickly: %v (expected ~700ms)", elapsed)
	}
}

func TestRateLimiterWaitContextCancellation(t *testing.T) {
	rl := NewRateLimiter(nil)

	now := time.Now()
	rl.nowFunc = func() time.Time { return now }

	// Set state to exhausted with reset far in the future
	rl.mu.Lock()
	rl.limits[RateLimitWrite] = &RateLimitState{
		Remaining: 0,
		Limit:     60,
		ResetAt:   now.Add(10 * time.Second),
	}
	rl.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := rl.Wait(ctx, RateLimitWrite)
	if err != context.DeadlineExceeded {
		t.Errorf("expected DeadlineExceeded, got %v", err)
	}
}

func TestRateLimiterProactiveSlowdown(t *testing.T) {
	rl := NewRateLimiter(nil)

	// Set remaining to < 10% of limit (9 out of 100)
	headers := http.Header{}
	headers.Set("X-Ratelimit-Remaining", "9")
	headers.Set("X-Ratelimit-Limit", "100")
	rl.Update(RateLimitRead, headers)

	ctx := context.Background()
	start := time.Now()
	err := rl.Wait(ctx, RateLimitRead)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should have a ~200ms proactive delay
	if elapsed < 150*time.Millisecond {
		t.Errorf("Expected proactive slowdown (~200ms), got %v", elapsed)
	}
}

func TestRateLimiterNoSlowdownAboveThreshold(t *testing.T) {
	rl := NewRateLimiter(nil)

	// Set remaining to > 10% of limit (15 out of 100)
	headers := http.Header{}
	headers.Set("X-Ratelimit-Remaining", "15")
	headers.Set("X-Ratelimit-Limit", "100")
	rl.Update(RateLimitRead, headers)

	ctx := context.Background()
	start := time.Now()
	err := rl.Wait(ctx, RateLimitRead)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if elapsed > 50*time.Millisecond {
		t.Errorf("Should not slow down above threshold, but waited %v", elapsed)
	}
}

func TestRateLimiterConcurrentAccess(t *testing.T) {
	rl := NewRateLimiter(nil)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			headers := http.Header{}
			headers.Set("X-Ratelimit-Remaining", "50")
			headers.Set("X-Ratelimit-Limit", "100")
			rl.Update(RateLimitRead, headers)

			_ = rl.Status()

			ctx := context.Background()
			_ = rl.Wait(ctx, RateLimitRead)
		}(i)
	}
	wg.Wait()
}

func TestRateLimiterUpdateFromRetryAfter(t *testing.T) {
	rl := NewRateLimiter(nil)

	headers := http.Header{}
	headers.Set("Retry-After", "5")

	duration := rl.UpdateFromRetryAfter(RateLimitWrite, headers)

	if duration != 5*time.Second {
		t.Errorf("expected 5s, got %v", duration)
	}

	status := rl.Status()
	s := status[RateLimitWrite]
	if s.Remaining != 0 {
		t.Errorf("expected remaining 0, got %d", s.Remaining)
	}
}

func TestRateLimiterUpdateFromRetryAfterFallback(t *testing.T) {
	rl := NewRateLimiter(nil)

	// No retry-after header, should default to 1 second
	headers := http.Header{}
	duration := rl.UpdateFromRetryAfter(RateLimitWrite, headers)

	if duration != 1*time.Second {
		t.Errorf("expected 1s fallback, got %v", duration)
	}
}

func TestRateLimiterOnUpdateCallback(t *testing.T) {
	var received []RateLimitStatus
	rl := NewRateLimiter(func(s RateLimitStatus) {
		received = append(received, s)
	})

	headers := http.Header{}
	headers.Set("X-Ratelimit-Remaining", "42")
	headers.Set("X-Ratelimit-Limit", "100")
	rl.Update(RateLimitRead, headers)

	if len(received) != 1 {
		t.Fatalf("expected 1 callback, got %d", len(received))
	}
	if received[0].Remaining != 42 {
		t.Errorf("callback got remaining %d, expected 42", received[0].Remaining)
	}
}

func TestRateLimiterSeparateCategories(t *testing.T) {
	rl := NewRateLimiter(nil)

	readHeaders := http.Header{}
	readHeaders.Set("X-Ratelimit-Remaining", "100")
	readHeaders.Set("X-Ratelimit-Limit", "200")
	rl.Update(RateLimitRead, readHeaders)

	writeHeaders := http.Header{}
	writeHeaders.Set("X-Ratelimit-Remaining", "10")
	writeHeaders.Set("X-Ratelimit-Limit", "60")
	rl.Update(RateLimitWrite, writeHeaders)

	status := rl.Status()

	if status[RateLimitRead].Remaining != 100 {
		t.Errorf("read remaining: expected 100, got %d", status[RateLimitRead].Remaining)
	}
	if status[RateLimitWrite].Remaining != 10 {
		t.Errorf("write remaining: expected 10, got %d", status[RateLimitWrite].Remaining)
	}
}

func TestRateLimiterWaitNoLimitSet(t *testing.T) {
	rl := NewRateLimiter(nil)

	// No limits set — should return immediately
	ctx := context.Background()
	start := time.Now()
	err := rl.Wait(ctx, RateLimitRead)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if elapsed > 50*time.Millisecond {
		t.Errorf("Wait with no limits should be instant, took %v", elapsed)
	}
}

func TestWaitReservesAgainstTheBudget(t *testing.T) {
	// Concurrent callers must not all read the same "remaining" and go together.
	// Each Wait takes one from the known budget, so once it is spent the next
	// caller waits for the reset instead of drawing a 429.
	rl := NewRateLimiter(nil)
	now := time.Now()
	rl.nowFunc = func() time.Time { return now }
	rl.limits[RateLimitRead] = &RateLimitState{Remaining: 2, Limit: 100, ResetAt: now.Add(50 * time.Millisecond)}

	ctx := context.Background()
	_ = rl.Wait(ctx, RateLimitRead)
	_ = rl.Wait(ctx, RateLimitRead)

	if got := rl.limits[RateLimitRead].Remaining; got != 0 {
		t.Fatalf("remaining after two reservations: %d, want 0", got)
	}

	// The third has nothing left and must hold until the reset.
	done := make(chan struct{})
	go func() {
		_ = rl.Wait(ctx, RateLimitRead)
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("a caller went ahead with the budget already spent")
	case <-time.After(20 * time.Millisecond):
	}
	<-done
}

func TestReservationIsCorrectedByTheNextResponse(t *testing.T) {
	rl := NewRateLimiter(nil)
	rl.limits[RateLimitRead] = &RateLimitState{Remaining: 5, Limit: 100, ResetAt: time.Now().Add(time.Minute)}
	_ = rl.Wait(context.Background(), RateLimitRead)

	headers := http.Header{}
	headers.Set("X-Ratelimit-Remaining", "40")
	rl.Update(RateLimitRead, headers)

	if got := rl.limits[RateLimitRead].Remaining; got != 40 {
		t.Fatalf("got %d, want the instance's own figure", got)
	}
}

func TestReserveIsANoOpWithoutAKnownLimit(t *testing.T) {
	// Before the first response there is no budget to count against.
	rl := NewRateLimiter(nil)
	_ = rl.Wait(context.Background(), RateLimitRead)
	if got := rl.limits[RateLimitRead].Remaining; got != 0 {
		t.Fatalf("got %d", got)
	}
}

type waitLog struct {
	mu     sync.Mutex
	events []WaitStatus
}

func (w *waitLog) record(s WaitStatus) {
	w.mu.Lock()
	w.events = append(w.events, s)
	w.mu.Unlock()
}

func (w *waitLog) snapshot() []WaitStatus {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]WaitStatus(nil), w.events...)
}

func TestSleepUntilReportsAPauseAndItsEnd(t *testing.T) {
	rl := NewRateLimiter(nil)
	log := &waitLog{}
	rl.OnWait(log.record)

	until := time.Now().Add(30 * time.Millisecond)
	if err := rl.SleepUntil(context.Background(), RateLimitSecretRead, until, false); err != nil {
		t.Fatal(err)
	}

	ev := log.snapshot()
	if len(ev) != 2 {
		t.Fatalf("got %d events, want a start and an end: %+v", len(ev), ev)
	}
	if !ev[0].Waiting || !ev[0].ResumeAt.Equal(until) || ev[0].Category != RateLimitSecretRead {
		t.Fatalf("start: %+v — it has to say when requests resume", ev[0])
	}
	if ev[1].Waiting {
		t.Fatalf("end: %+v", ev[1])
	}
}

func TestSharedPauseEndsOnlyWhenTheLastWaiterResumes(t *testing.T) {
	// Six workers can be held by one limit. If each reported its own end, the
	// indicator would clear while the rest of the run was still stopped.
	rl := NewRateLimiter(nil)
	log := &waitLog{}
	rl.OnWait(log.record)

	var wg sync.WaitGroup
	base := time.Now()
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = rl.SleepUntil(context.Background(), RateLimitRead, base.Add(time.Duration(20+10*i)*time.Millisecond), true)
		}(i)
	}
	wg.Wait()

	ev := log.snapshot()
	ends := 0
	for _, e := range ev {
		if !e.Waiting {
			ends++
		}
	}
	if ends != 1 {
		t.Fatalf("got %d end events for one shared pause: %+v", ends, ev)
	}
	if last := ev[len(ev)-1]; last.Waiting {
		t.Fatalf("the last event should end the pause: %+v", last)
	}
	// The resume time reported only ever moves later, to the latest waiter's.
	var latest time.Time
	for _, e := range ev {
		if e.Waiting {
			if e.ResumeAt.Before(latest) {
				t.Fatalf("resume time moved earlier: %+v", ev)
			}
			latest = e.ResumeAt
		}
	}
	if !latest.Equal(base.Add(60 * time.Millisecond)) {
		t.Fatalf("final resume time %v, want the latest waiter's", latest)
	}
}

func TestA429IsReportedAsARefusedPause(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"messages":["slow down"]}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"projects":[],"page":1}`))
	}))
	defer srv.Close()

	rl := NewRateLimiter(nil)
	log := &waitLog{}
	rl.OnWait(log.record)
	client := NewClient(srv.URL, "dp.ct.test", WithRateLimiter(rl))

	start := time.Now()
	if _, _, err := client.Get(context.Background(), "/projects", nil); err != nil {
		t.Fatalf("the retry should have succeeded: %v", err)
	}

	ev := log.snapshot()
	if len(ev) != 2 || !ev[0].Waiting || !ev[0].Refused {
		t.Fatalf("events: %+v — want a refused pause, then its end", ev)
	}
	if ev[0].ResumeAt.Before(start.Add(time.Second)) {
		t.Fatalf("resume at %v, sooner than the Retry-After allows", ev[0].ResumeAt)
	}
}
