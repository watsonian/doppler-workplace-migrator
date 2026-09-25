package doppler

import (
	"errors"
	"fmt"
	"net/http"
)

// maxEvidenceBody bounds how much of a response body is kept. Enough to read the
// messages array Doppler returns, short enough that ten thousand records do not
// become a memory problem.
const maxEvidenceBody = 2048

// Evidence is what makes a failure diagnosable rather than merely reported: the
// request that was made, the token that made it, and the response that came back.
//
// Nothing here is a credential. The token appears only as its prefix and last four.
type Evidence struct {
	Side           string `json:"side"` // "source" or "target"
	TokenHint      string `json:"token_hint,omitempty"`
	TokenKind      string `json:"token_kind,omitempty"`
	TokenKindLabel string `json:"token_kind_label,omitempty"`
	Actor          string `json:"actor,omitempty"`

	Method string `json:"method,omitempty"`
	URL    string `json:"url,omitempty"`

	Status     int    `json:"status,omitempty"`
	StatusText string `json:"status_text,omitempty"`
	Body       string `json:"body,omitempty"`

	Attempts   int   `json:"attempts,omitempty"`
	RetryAfter int   `json:"retry_after,omitempty"`
	LatencyMS  int64 `json:"latency_ms,omitempty"`
}

// EvidenceFrom builds an Evidence from an error, filling in whatever the error
// carried. A non-API error still yields a usable record — side and message —
// rather than nothing.
func EvidenceFrom(err error, side string) Evidence {
	ev := Evidence{Side: side}

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return ev
	}

	ev.TokenHint = apiErr.TokenHint
	ev.TokenKind = string(apiErr.TokenKind)
	if apiErr.TokenKind != "" {
		ev.TokenKindLabel = apiErr.TokenKind.Label()
	}
	ev.Actor = apiErr.Actor
	ev.Method = apiErr.Method
	ev.URL = apiErr.URL
	ev.Status = apiErr.StatusCode
	if apiErr.StatusCode != 0 {
		ev.StatusText = fmt.Sprintf("%d %s", apiErr.StatusCode, http.StatusText(apiErr.StatusCode))
	}
	ev.Body = truncateBody(apiErr.Body, maxEvidenceBody)
	ev.Attempts = apiErr.Attempts
	ev.RetryAfter = apiErr.RetryAfter
	ev.LatencyMS = apiErr.LatencyMS

	return ev
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n… truncated"
}

func truncateBody(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n… truncated"
}
