package doppler

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// RateLimitCategory represents different rate limit buckets.
type RateLimitCategory string

const (
	RateLimitRead       RateLimitCategory = "read"
	RateLimitSecretRead RateLimitCategory = "secret_read"
	RateLimitWrite      RateLimitCategory = "write"
)

// RateLimitState tracks the current rate limit state for a category.
type RateLimitState struct {
	Remaining int
	Limit     int
	ResetAt   time.Time
}

// RateLimitStatus is the public representation of rate limit state.
type RateLimitStatus struct {
	Category  RateLimitCategory `json:"category"`
	Remaining int               `json:"remaining"`
	Limit     int               `json:"limit"`
	ResetAt   time.Time         `json:"reset_at"`
	Waiting   bool              `json:"waiting"`
}

// RateLimiter tracks rate limits across categories and enforces waits.
type RateLimiter struct {
	mu       sync.Mutex
	limits   map[RateLimitCategory]*RateLimitState
	onUpdate func(RateLimitStatus)
	nowFunc  func() time.Time // injectable for testing

	// minInterval paces requests regardless of what the headers say. The limiter is
	// otherwise reactive — it slows down only once the instance has started
	// complaining — which is no help after a run has already exhausted its retries.
	// Setting a floor here is what "re-run slower" actually does.
	minInterval map[RateLimitCategory]time.Duration
	lastRequest map[RateLimitCategory]time.Time

	// Pauses in progress, per category. Several workers can be held by the same
	// limit at once, so a pause is reported as starting when the first one waits
	// and as over only when the last one resumes — otherwise the indicator would
	// flicker off while the run is still stopped.
	onWait   func(WaitStatus)
	waiters  map[RateLimitCategory]int
	resumeAt map[RateLimitCategory]time.Time
}

// WaitStatus is a pause starting, moving, or ending.
type WaitStatus struct {
	Category RateLimitCategory `json:"category"`
	Waiting  bool              `json:"waiting"`
	// ResumeAt is when requests start again. Zero once the pause is over.
	ResumeAt time.Time `json:"resume_at"`
	// Refused is set when the instance answered 429, as opposed to the limiter
	// holding back on its own because the known budget is spent.
	Refused bool `json:"refused"`
}

// NewRateLimiter creates a new rate limiter with optional update callback.
func NewRateLimiter(onUpdate func(RateLimitStatus)) *RateLimiter {
	return &RateLimiter{
		limits: map[RateLimitCategory]*RateLimitState{
			RateLimitRead:       {},
			RateLimitSecretRead: {},
			RateLimitWrite:      {},
		},
		onUpdate:    onUpdate,
		nowFunc:     time.Now,
		minInterval: map[RateLimitCategory]time.Duration{},
		lastRequest: map[RateLimitCategory]time.Time{},
		waiters:     map[RateLimitCategory]int{},
		resumeAt:    map[RateLimitCategory]time.Time{},
	}
}

// OnWait registers a listener for pauses. Separate from the update callback,
// which fires on every response — including "not waiting" ones, which arrive
// from other workers mid-pause and would clear an indicator that should stay up.
func (rl *RateLimiter) OnWait(fn func(WaitStatus)) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.onWait = fn
}

// SleepUntil holds the caller until the given time, reporting the pause.
//
// Every wait the limiter imposes goes through here — the budget running out,
// and a 429 telling it to back off — so both are visible, with when they end.
func (rl *RateLimiter) SleepUntil(ctx context.Context, category RateLimitCategory, until time.Time, refused bool) error {
	rl.mu.Lock()
	rl.waiters[category]++
	extended := until.After(rl.resumeAt[category])
	if extended {
		rl.resumeAt[category] = until
	}
	first := rl.waiters[category] == 1
	resume := rl.resumeAt[category]
	notify := rl.onWait
	rl.mu.Unlock()

	// A later waiter can push the resume time out; one that resumes sooner
	// than an existing pause would say nothing new.
	if notify != nil && (first || extended) {
		notify(WaitStatus{Category: category, Waiting: true, ResumeAt: resume, Refused: refused})
	}

	var err error
	select {
	case <-time.After(time.Until(until)):
	case <-ctx.Done():
		err = ctx.Err()
	}

	rl.mu.Lock()
	rl.waiters[category]--
	last := rl.waiters[category] == 0
	if last {
		delete(rl.resumeAt, category)
	}
	notify = rl.onWait
	rl.mu.Unlock()

	if notify != nil && last {
		notify(WaitStatus{Category: category, Waiting: false})
	}
	return err
}

// SetMinInterval sets a floor on the gap between requests in a category. Zero
// removes the floor. Applied on top of whatever the response headers ask for, so
// it can only ever slow things down.
func (rl *RateLimiter) SetMinInterval(category RateLimitCategory, d time.Duration) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if d <= 0 {
		delete(rl.minInterval, category)
		return
	}
	rl.minInterval[category] = d
}

// MinInterval reports the current floor for a category.
func (rl *RateLimiter) MinInterval(category RateLimitCategory) time.Duration {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return rl.minInterval[category]
}

// pace blocks until the configured minimum gap since the last request in this
// category has elapsed, then records this request as the new last one.
func (rl *RateLimiter) pace(ctx context.Context, category RateLimitCategory) error {
	rl.mu.Lock()
	minGap := rl.minInterval[category]
	if minGap <= 0 {
		rl.mu.Unlock()
		return nil
	}
	now := rl.now()
	last := rl.lastRequest[category]
	wait := minGap - now.Sub(last)
	// Claim the slot before unlocking, so two callers cannot both decide they may
	// go now. Sequential today, but a limiter that only works single-threaded is a
	// trap for whoever parallelizes this later.
	rl.lastRequest[category] = now.Add(max(wait, 0))
	rl.mu.Unlock()

	if wait <= 0 {
		return nil
	}
	select {
	case <-time.After(wait):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// now returns the current time, using the injectable nowFunc for testing.
func (rl *RateLimiter) now() time.Time {
	if rl.nowFunc != nil {
		return rl.nowFunc()
	}
	return time.Now()
}

// Wait blocks until it's safe to make a request in the given category.
// Returns an error if the context is cancelled while waiting.
func (rl *RateLimiter) Wait(ctx context.Context, category RateLimitCategory) error {
	if err := rl.pace(ctx, category); err != nil {
		return err
	}

	rl.mu.Lock()
	state := rl.limits[category]
	if state == nil {
		rl.mu.Unlock()
		return nil
	}

	remaining := state.Remaining
	limit := state.Limit
	resetAt := state.ResetAt
	rl.mu.Unlock()

	now := rl.now()

	// If we've exhausted the limit and the reset time is in the future, wait
	if remaining <= 0 && limit > 0 && now.Before(resetAt) {
		waitDuration := resetAt.Sub(now) + 500*time.Millisecond

		if rl.onUpdate != nil {
			rl.onUpdate(RateLimitStatus{
				Category:  category,
				Remaining: remaining,
				Limit:     limit,
				ResetAt:   resetAt,
				Waiting:   true,
			})
		}

		if err := rl.SleepUntil(ctx, category, time.Now().Add(waitDuration), false); err != nil {
			return err
		}

		if rl.onUpdate != nil {
			rl.onUpdate(RateLimitStatus{
				Category:  category,
				Remaining: remaining,
				Limit:     limit,
				ResetAt:   resetAt,
				Waiting:   false,
			})
		}

		return nil
	}

	// Reserve this request against the budget before it goes out. The count
	// otherwise moves only when a response's headers arrive, so concurrent
	// callers all reading the same "remaining: 1" would go together and overrun
	// it. The next response corrects the figure to whatever the instance says.
	rl.reserve(category)

	// Proactive slowdown when approaching the limit (< 10% remaining)
	if limit > 0 && remaining > 0 && remaining < limit/10 {
		select {
		case <-time.After(200 * time.Millisecond):
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return nil
}

// reserve counts one request against a category's known budget.
func (rl *RateLimiter) reserve(category RateLimitCategory) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if state := rl.limits[category]; state != nil && state.Limit > 0 && state.Remaining > 0 {
		state.Remaining--
	}
}

// Update parses rate limit headers from an HTTP response and updates state.
func (rl *RateLimiter) Update(category RateLimitCategory, headers http.Header) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	state := rl.limits[category]
	if state == nil {
		state = &RateLimitState{}
		rl.limits[category] = state
	}

	if v := headers.Get("X-Ratelimit-Remaining"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			state.Remaining = n
		}
	}

	if v := headers.Get("X-Ratelimit-Limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			state.Limit = n
		}
	}

	if v := headers.Get("X-Ratelimit-Reset"); v != "" {
		if ts, err := strconv.ParseInt(v, 10, 64); err == nil {
			state.ResetAt = time.Unix(ts, 0)
		}
	}

	if rl.onUpdate != nil {
		rl.onUpdate(RateLimitStatus{
			Category:  category,
			Remaining: state.Remaining,
			Limit:     state.Limit,
			ResetAt:   state.ResetAt,
			Waiting:   false,
		})
	}
}

// UpdateFromRetryAfter updates the state from a 429 response's retry-after header.
func (rl *RateLimiter) UpdateFromRetryAfter(category RateLimitCategory, headers http.Header) time.Duration {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	state := rl.limits[category]
	if state == nil {
		state = &RateLimitState{}
		rl.limits[category] = state
	}

	state.Remaining = 0

	retryAfter := headers.Get("Retry-After")
	if retryAfter != "" {
		if seconds, err := strconv.Atoi(retryAfter); err == nil {
			duration := time.Duration(seconds) * time.Second
			state.ResetAt = rl.now().Add(duration)
			return duration
		}
	}

	// Fallback: use reset header or default 1 second
	if v := headers.Get("X-Ratelimit-Reset"); v != "" {
		if ts, err := strconv.ParseInt(v, 10, 64); err == nil {
			state.ResetAt = time.Unix(ts, 0)
			return time.Until(state.ResetAt)
		}
	}

	// Default wait of 1 second if no headers found
	state.ResetAt = rl.now().Add(1 * time.Second)
	return 1 * time.Second
}

// Status returns the current rate limit status for all categories.
func (rl *RateLimiter) Status() map[RateLimitCategory]RateLimitStatus {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	result := make(map[RateLimitCategory]RateLimitStatus, len(rl.limits))
	for cat, state := range rl.limits {
		result[cat] = RateLimitStatus{
			Category:  cat,
			Remaining: state.Remaining,
			Limit:     state.Limit,
			ResetAt:   state.ResetAt,
		}
	}
	return result
}
