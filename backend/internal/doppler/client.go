package doppler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultUserAgent = "doppler-workplace-migrator/1.0"
	defaultPerPage   = 100
	maxRetries       = 3
	// maxRateLimitRetries bounds how many times a single request will wait out a
	// 429. Without a bound, an instance that keeps returning 429 pins the migration
	// in a retry loop with no error and no progress.
	maxRateLimitRetries = 10
)

// Client is an HTTP client for the Doppler API with rate limiting and retries.
type Client struct {
	httpClient  *http.Client
	baseURL     string
	token       string
	rateLimiter *RateLimiter
	userAgent   string

	// actorName is who the token acts as, cached from the first /v3/me response so
	// an error can name it without making a request of its own.
	identityMu sync.RWMutex
	actorName  string
}

// ClientOption configures a Client.
type ClientOption func(*Client)

// WithHTTPClient sets a custom HTTP client.
func WithHTTPClient(c *http.Client) ClientOption {
	return func(cl *Client) {
		cl.httpClient = c
	}
}

// WithRateLimiter sets a custom rate limiter.
func WithRateLimiter(rl *RateLimiter) ClientOption {
	return func(cl *Client) {
		cl.rateLimiter = rl
	}
}

// WithUserAgent sets a custom user agent.
func WithUserAgent(ua string) ClientOption {
	return func(cl *Client) {
		cl.userAgent = ua
	}
}

// NewClient creates a new Doppler API client.
func NewClient(baseURL, token string, opts ...ClientOption) *Client {
	// Normalize base URL: ensure trailing slash on base, ensure /v3/ path
	baseURL = strings.TrimRight(baseURL, "/")
	if !strings.HasSuffix(baseURL, "/v3") {
		baseURL += "/v3"
	}

	c := &Client{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		baseURL:     baseURL,
		token:       token,
		rateLimiter: NewRateLimiter(nil),
		userAgent:   defaultUserAgent,
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

// APIError represents an error from the Doppler API.
//
// Beyond the status and message it carries the evidence needed to tell whether the
// migrator is wrong or the setup is: which request was made, which token made it,
// what came back, and how hard it was tried. Every field here is safe to show —
// the token appears only as a hint.
type APIError struct {
	StatusCode int
	Message    string
	Body       string

	// Method and URL are the request that failed, with the query string intact.
	// Auth travels in a header, so the URL never carries a credential.
	Method string
	URL    string

	// TokenHint is the prefix-and-last-four form of the token used; TokenKind is
	// its documented format; Actor is who it acts as, when known.
	TokenHint string
	TokenKind TokenKind
	Actor     string

	// Attempts counts how many times the request was sent, including the first.
	// RetryAfter is the seconds the server asked for, on a 429.
	Attempts   int
	RetryAfter int

	// LatencyMS is how long the final attempt took.
	LatencyMS int64
}

// Error returns the status and the API's own message, and deliberately never the
// raw response body.
//
// An error string is the least controlled thing in the program: it is written to
// the checkpoint file on disk, printed to the logs, published on the event
// stream and carried into the CSV export. A body is whatever the other end sent
// — an HTML page from a proxy, a stack trace, or a validation message quoting
// the request back — and none of that belongs in all four places.
//
// The body is not lost. It is kept on the struct and published deliberately, in
// bounded form, through Evidence, where it is shown to someone diagnosing the
// failure rather than appended to every line that mentions it.
func (e *APIError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("doppler API error (status %d): %s", e.StatusCode, e.Message)
	}
	return fmt.Sprintf("doppler API error (status %d)", e.StatusCode)
}

// annotate fills in the request-and-token half of the evidence. Called at every
// return site so no APIError escapes doRequest without it.
func (c *Client) annotate(err *APIError, method, fullURL string, attempts int, latency time.Duration) *APIError {
	err.Method = method
	err.URL = fullURL
	err.Attempts = attempts
	err.LatencyMS = latency.Milliseconds()
	err.TokenHint = TokenHint(c.token)
	err.TokenKind = ClassifyToken(c.token)
	err.Actor = c.actor()
	return err
}

// actor returns who the token acts as, if a /v3/me call has resolved it. Reading
// it is cheap and lock-guarded; it is never fetched on the error path, because an
// error is a bad moment to make another request.
func (c *Client) actor() string {
	c.identityMu.RLock()
	defer c.identityMu.RUnlock()
	return c.actorName
}

// setActor records who the token acts as, from a /v3/me response.
func (c *Client) setActor(name string) {
	c.identityMu.Lock()
	c.actorName = name
	c.identityMu.Unlock()
}

// TokenHint returns this client's token in prefix-and-last-four form.
func (c *Client) TokenHint() string { return TokenHint(c.token) }

// TokenKind returns the documented format of this client's token.
func (c *Client) TokenKind() TokenKind { return ClassifyToken(c.token) }

// doRequest performs an HTTP request with rate limiting, retries, and header parsing.
func (c *Client) doRequest(ctx context.Context, method, path string, body interface{}, params url.Values, category RateLimitCategory) (json.RawMessage, http.Header, error) {
	fullURL := c.baseURL + path
	if len(params) > 0 {
		fullURL += "?" + params.Encode()
	}

	var attempt int
	var rateLimitRetries int
	for {
		attempt++

		// Wait for rate limiter
		if err := c.rateLimiter.Wait(ctx, category); err != nil {
			return nil, nil, err
		}

		// Build request body
		var bodyReader io.Reader
		if body != nil {
			bodyBytes, err := json.Marshal(body)
			if err != nil {
				return nil, nil, fmt.Errorf("marshaling request body: %w", err)
			}
			bodyReader = bytes.NewReader(bodyBytes)
		}

		req, err := http.NewRequestWithContext(ctx, method, fullURL, bodyReader)
		if err != nil {
			return nil, nil, fmt.Errorf("creating request: %w", err)
		}

		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("User-Agent", c.userAgent)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("Accept", "application/json")

		started := time.Now()
		resp, err := c.httpClient.Do(req)
		latency := time.Since(started)
		if err != nil {
			// Network error — retry with backoff
			if attempt <= maxRetries {
				select {
				case <-time.After(backoffDuration(attempt)):
				case <-ctx.Done():
					return nil, nil, ctx.Err()
				}
				continue
			}
			return nil, nil, fmt.Errorf("request failed after %d retries: %w", maxRetries, err)
		}

		// Update rate limiter from response headers
		c.rateLimiter.Update(category, resp.Header)

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, nil, fmt.Errorf("reading response: %w", err)
		}

		// Handle 429 — rate limited
		if resp.StatusCode == http.StatusTooManyRequests {
			rateLimitRetries++
			if rateLimitRetries > maxRateLimitRetries {
				apiErr := c.annotate(&APIError{
					StatusCode: resp.StatusCode,
					Message:    fmt.Sprintf("still rate limited after %d retries", maxRateLimitRetries),
					Body:       string(respBody),
				}, method, fullURL, attempt, latency)
				apiErr.RetryAfter = retryAfterSeconds(resp.Header)
				return nil, nil, apiErr
			}
			waitDuration := c.rateLimiter.UpdateFromRetryAfter(category, resp.Header)
			if err := c.rateLimiter.SleepUntil(ctx, category, time.Now().Add(waitDuration+500*time.Millisecond), true); err != nil {
				return nil, nil, err
			}
			continue // retry
		}

		// Handle 5xx — server error, retry with backoff.
		//
		// Only for methods the API defines as idempotent. Replaying a POST that the
		// server may already have applied can create a duplicate: most creates here
		// are guarded by a 409 or a name check, but not all, and a 502 from a proxy
		// says nothing about whether the write landed.
		if resp.StatusCode >= 500 {
			if attempt <= maxRetries && isIdempotentMethod(method) {
				select {
				case <-time.After(backoffDuration(attempt)):
				case <-ctx.Done():
					return nil, nil, ctx.Err()
				}
				continue
			}
			return nil, nil, c.annotate(&APIError{
				StatusCode: resp.StatusCode,
				Body:       string(respBody),
			}, method, fullURL, attempt, latency)
		}

		// Handle 4xx — client error, don't retry
		if resp.StatusCode >= 400 {
			apiErr := c.annotate(&APIError{
				StatusCode: resp.StatusCode,
				Body:       string(respBody),
			}, method, fullURL, attempt, latency)
			// Try to extract error message from JSON
			var errResp struct {
				Messages []string `json:"messages"`
				Message  string   `json:"message"`
			}
			if json.Unmarshal(respBody, &errResp) == nil {
				if len(errResp.Messages) > 0 {
					apiErr.Message = strings.Join(errResp.Messages, "; ")
				} else if errResp.Message != "" {
					apiErr.Message = errResp.Message
				}
			}
			return nil, nil, apiErr
		}

		return json.RawMessage(respBody), resp.Header, nil
	}
}

// retryAfterSeconds reads the Retry-After header as whole seconds. Absent or
// unparsable, it reports 0 — the evidence panel then simply omits it.
func retryAfterSeconds(h http.Header) int {
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// backoffDuration returns exponential backoff duration for a given attempt.
func backoffDuration(attempt int) time.Duration {
	// 1s, 2s, 4s
	d := time.Duration(1<<uint(attempt-1)) * time.Second
	if d > 4*time.Second {
		d = 4 * time.Second
	}
	return d
}

// Get performs a GET request.
func (c *Client) Get(ctx context.Context, path string, params url.Values) (json.RawMessage, http.Header, error) {
	return c.doRequest(ctx, http.MethodGet, path, nil, params, RateLimitRead)
}

// GetSecrets performs a GET request using the secret-read rate limit category.
func (c *Client) GetSecrets(ctx context.Context, path string, params url.Values) (json.RawMessage, http.Header, error) {
	return c.doRequest(ctx, http.MethodGet, path, nil, params, RateLimitSecretRead)
}

// Post performs a POST request.
func (c *Client) Post(ctx context.Context, path string, body interface{}) (json.RawMessage, http.Header, error) {
	return c.doRequest(ctx, http.MethodPost, path, body, nil, RateLimitWrite)
}

// Put performs a PUT request.
func (c *Client) Put(ctx context.Context, path string, body interface{}) (json.RawMessage, http.Header, error) {
	return c.doRequest(ctx, http.MethodPut, path, body, nil, RateLimitWrite)
}

// Patch performs a PATCH request.
func (c *Client) Patch(ctx context.Context, path string, body interface{}) (json.RawMessage, http.Header, error) {
	return c.doRequest(ctx, http.MethodPatch, path, body, nil, RateLimitWrite)
}

// Delete performs a DELETE request.
func (c *Client) Delete(ctx context.Context, path string, params url.Values) (json.RawMessage, http.Header, error) {
	return c.doRequest(ctx, http.MethodDelete, path, nil, params, RateLimitWrite)
}

// ListAll collects every item from a list endpoint, paginating when the endpoint
// supports it. The key parameter specifies which JSON field contains the items.
//
// Whether an endpoint paginates is determined from the response, not from a list
// kept here: a paginated endpoint echoes a "page" attribute alongside the items.
// An endpoint without one ignores page parameters entirely and has already returned
// everything, so asking for a second page would return the same payload again —
// appending it forever. Several endpoints the migrator uses behave that way
// (workplace roles, project roles, integrations, webhooks, trusted IPs, config
// service tokens), and the old "stop on a short page" rule looped on all of them
// once a workplace had a full page's worth.
//
// For endpoints that do paginate, the loop runs until a page comes back empty.
// Stopping on a short page is not safe either: some endpoints cap page size below
// what is requested — GET /workplace/users ignores per_page and hard-codes 20 — so
// a short first page says nothing about whether more remain.
func (c *Client) ListAll(ctx context.Context, path string, params url.Values, key string, category RateLimitCategory) ([]json.RawMessage, error) {
	if params == nil {
		params = url.Values{}
	}

	var all []json.RawMessage
	page := 1

	for {
		params.Set("page", strconv.Itoa(page))
		params.Set("per_page", strconv.Itoa(defaultPerPage))

		result, _, err := c.doRequest(ctx, http.MethodGet, path, nil, params, category)
		if err != nil {
			return nil, err
		}

		// Parse the response to extract the items array
		var parsed map[string]json.RawMessage
		if err := json.Unmarshal(result, &parsed); err != nil {
			return nil, fmt.Errorf("parsing paginated response: %w", err)
		}

		itemsRaw, ok := parsed[key]
		if !ok {
			return nil, fmt.Errorf("response missing key %q", key)
		}

		var items []json.RawMessage
		if err := json.Unmarshal(itemsRaw, &items); err != nil {
			return nil, fmt.Errorf("parsing items array: %w", err)
		}

		all = append(all, items...)

		if _, paginated := parsed["page"]; !paginated {
			// Everything was in this one response.
			break
		}
		if len(items) == 0 {
			break
		}
		page++
	}

	return all, nil
}

// BaseURL returns the client's base URL (for testing/debugging).
func (c *Client) BaseURL() string {
	return c.baseURL
}

// RateLimiter returns the client's rate limiter.
func (c *Client) RateLimiterStatus() map[RateLimitCategory]RateLimitStatus {
	return c.rateLimiter.Status()
}

// isIdempotentMethod reports whether replaying a request is safe. POST is the only
// method the migrator uses that can create something, so it is the one excluded.
func isIdempotentMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete, http.MethodOptions:
		return true
	default:
		return false
	}
}
