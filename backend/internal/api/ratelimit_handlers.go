package api

import (
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/doppleruniversity/workplace-migrator/internal/doppler"
)

// Rate-limit pauses, made visible.
//
// A run held by a rate limit looks exactly like a run that has hung: nothing
// moves, and nothing says why. Every limiter the server creates reports its
// pauses here, and the UI shows them — which side, what kind of request, and
// when it starts again.

// rateWait is one pause in progress, as the UI sees it.
type rateWait struct {
	Side     string                    `json:"side"` // source or target
	Category doppler.RateLimitCategory `json:"category"`
	ResumeAt time.Time                 `json:"resume_at"`
	Refused  bool                      `json:"refused"`
}

// rateWaits holds the pauses in progress, so a page opened mid-pause can show
// one without waiting for the next event.
type rateWaits struct {
	mu     sync.Mutex
	active map[string]rateWait
}

func (rw *rateWaits) set(w rateWait, waiting bool) {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	if rw.active == nil {
		rw.active = map[string]rateWait{}
	}
	key := w.Side + "/" + string(w.Category)
	if waiting {
		rw.active[key] = w
	} else {
		delete(rw.active, key)
	}
}

func (rw *rateWaits) list() []rateWait {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	now := time.Now()
	out := make([]rateWait, 0, len(rw.active))
	for key, w := range rw.active {
		// A pause whose end has passed but whose last waiter never reported —
		// a cancelled run, say — is not a pause any more.
		if w.ResumeAt.Before(now) {
			delete(rw.active, key)
			continue
		}
		out = append(out, w)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ResumeAt.Before(out[j].ResumeAt) })
	return out
}

// newLimiter builds a rate limiter whose pauses reach the UI, labelled with
// the side of the migration its client talks to.
func (s *Server) newLimiter(side string) *doppler.RateLimiter {
	// Every response's rate-limit headers go to the one status per side: the
	// limit is the token's, however many limiters are making requests with it.
	rl := doppler.NewRateLimiter(func(st doppler.RateLimitStatus) {
		if s.rateStatus.record(side, st) {
			s.SSE.Publish(SSEEvent{Type: "rate_status", Data: s.rateStatus.snapshot()})
		}
	})
	rl.OnWait(func(w doppler.WaitStatus) {
		wait := rateWait{Side: side, Category: w.Category, ResumeAt: w.ResumeAt, Refused: w.Refused}
		s.rateWaits.set(wait, w.Waiting)
		s.SSE.Publish(SSEEvent{Type: "rate_limit", Data: map[string]any{
			"side":      side,
			"category":  w.Category,
			"waiting":   w.Waiting,
			"resume_at": w.ResumeAt,
			"refused":   w.Refused,
		}})
	})
	return rl
}

// HandleRateLimits lists the pauses in progress and where each token stands
// against its limits.
func (s *Server) HandleRateLimits() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, http.StatusOK, map[string]any{
			"waits":  s.rateWaits.list(),
			"status": s.rateStatus.snapshot(),
		})
	}
}

// Where each token stands against its rate limits, for the indicator in the
// top bar. Doppler reports the budget on every response — remaining, limit,
// and when the window resets — per kind of request, and counts it against the
// token; so the latest response for a side is that token's state, whichever
// limiter made the request.

// rateCategoryStatus is one kind of request's budget.
type rateCategoryStatus struct {
	Category  doppler.RateLimitCategory `json:"category"`
	Remaining int                       `json:"remaining"`
	Limit     int                       `json:"limit"`
	ResetAt   time.Time                 `json:"reset_at"`
	// Used is the fraction of the limit spent, 0 to 1. A window whose reset
	// has passed is spent nothing.
	Used float64 `json:"used"`
}

// rateSideStatus is one token's budgets, and the worst of them.
type rateSideStatus struct {
	Side       string               `json:"side"`
	Categories []rateCategoryStatus `json:"categories"`
	// Level is ok (under 70% of any limit), warn (70 to 99%) or limited (a
	// limit spent), from the most-used category; empty before any response.
	Level string  `json:"level"`
	Used  float64 `json:"used"`
	// ResetAt is when the most-used category's window resets.
	ResetAt time.Time `json:"reset_at"`
}

type rateStatusStore struct {
	mu          sync.Mutex
	sides       map[string]map[doppler.RateLimitCategory]doppler.RateLimitStatus
	lastPublish map[string]time.Time
	lastLevel   map[string]string
	now         func() time.Time
}

// rateStatusInterval is the most often a side's status is published while
// its level stays the same. A run makes many requests a second.
const rateStatusInterval = 750 * time.Millisecond

// record keeps a response's budget and reports whether to publish: when the
// level changes, and otherwise no more often than rateStatusInterval.
func (rs *rateStatusStore) record(side string, st doppler.RateLimitStatus) bool {
	if st.Limit <= 0 {
		return false // a response without the headers says nothing
	}
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if rs.sides == nil {
		rs.sides = map[string]map[doppler.RateLimitCategory]doppler.RateLimitStatus{}
		rs.lastPublish = map[string]time.Time{}
		rs.lastLevel = map[string]string{}
	}
	if rs.sides[side] == nil {
		rs.sides[side] = map[doppler.RateLimitCategory]doppler.RateLimitStatus{}
	}
	rs.sides[side][st.Category] = st

	now := rs.clock()
	level := rs.sideLocked(side, now).Level
	if level != rs.lastLevel[side] || now.Sub(rs.lastPublish[side]) >= rateStatusInterval {
		rs.lastLevel[side] = level
		rs.lastPublish[side] = now
		return true
	}
	return false
}

func (rs *rateStatusStore) clock() time.Time {
	if rs.now != nil {
		return rs.now()
	}
	return time.Now()
}

// snapshot is both sides, source first.
func (rs *rateStatusStore) snapshot() []rateSideStatus {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	now := rs.clock()
	out := []rateSideStatus{}
	for _, side := range []string{"source", "target"} {
		out = append(out, rs.sideLocked(side, now))
	}
	return out
}

func (rs *rateStatusStore) sideLocked(side string, now time.Time) rateSideStatus {
	out := rateSideStatus{Side: side, Categories: []rateCategoryStatus{}}
	for _, st := range rs.sides[side] {
		c := rateCategoryStatus{Category: st.Category, Remaining: st.Remaining, Limit: st.Limit, ResetAt: st.ResetAt}
		if st.ResetAt.IsZero() || st.ResetAt.After(now) {
			c.Used = float64(st.Limit-st.Remaining) / float64(st.Limit)
		} else {
			// The window has reset since this was read: the budget is full again.
			c.Remaining = st.Limit
		}
		if c.Used < 0 {
			c.Used = 0
		}
		out.Categories = append(out.Categories, c)
		if c.Used >= out.Used {
			out.Used = c.Used
			out.ResetAt = c.ResetAt
		}
	}
	sort.Slice(out.Categories, func(i, j int) bool { return out.Categories[i].Category < out.Categories[j].Category })
	switch {
	case len(out.Categories) == 0:
		out.Level = ""
	case out.Used >= 1:
		out.Level = "limited"
	case out.Used >= 0.7:
		out.Level = "warn"
	default:
		out.Level = "ok"
	}
	return out
}

// sharedLimiter returns the server's limiter for a side, creating it the first
// time. The run and everything after it share one per side, so pacing applied
// by "re-run slower" holds across them. Handing a client a nil limiter is not
// an option: its first request dereferences it — which is what issuing a token
// after a restart used to do, since the limiters were only made by a run.
func (s *Server) sharedLimiter(side string) *doppler.RateLimiter {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sharedLimiterLocked(side)
}

func (s *Server) sharedLimiterLocked(side string) *doppler.RateLimiter {
	if side == "source" {
		if s.sourceLimiter == nil {
			s.sourceLimiter = s.newLimiter("source")
		}
		return s.sourceLimiter
	}
	if s.targetLimiter == nil {
		s.targetLimiter = s.newLimiter("target")
	}
	return s.targetLimiter
}
