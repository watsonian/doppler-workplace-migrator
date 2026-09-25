package doppler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"
)

// ConfigLogEntry is one recorded change to a config.
//
// This is how drift is detected. The alternative — keeping a hash of every
// secret we wrote so a later run can spot a changed one — means a file whose
// whole purpose is to answer "is this the value of that secret?", for a
// workplace full of values like "true" and "production". The instance already
// records what we would be reconstructing, so nothing derived from a secret
// value has to be written down at all.
//
// The v3 export carries a rendered description rather than the structured diff
// the log holds internally, so this is config-level: it says the config was
// changed, by whom and when, and usually names the secrets in Text.
type ConfigLogEntry struct {
	ID        string    `json:"id"`
	Text      string    `json:"text"`
	HTML      string    `json:"html"`
	Rollback  bool      `json:"rollback"`
	CreatedAt time.Time `json:"created_at"`
	Config    string    `json:"config"`
	Project   string    `json:"project"`
	User      LogActor  `json:"user"`
}

// LogActor is whoever made a change. A service account and a person are both
// actors here, and telling them apart is most of the value.
type LogActor struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	Slug  string `json:"slug"`
	Type  string `json:"type"`
}

// Identity returns the most specific name this actor has, for matching against
// our own and for showing to whoever has to decide what the change means.
func (a LogActor) Identity() string {
	switch {
	case a.Email != "":
		return a.Email
	case a.Name != "":
		return a.Name
	default:
		return a.Slug
	}
}

// configLogPageSize is how many entries a page asks for. Drift only cares about
// entries newer than our last write, so a run normally reads one page and stops.
const configLogPageSize = 100

// maxConfigLogPages bounds the walk back through a config's history. A config
// changed thousands of times since our write is drifted many times over; there
// is no answer past the first page that changes what anyone would do.
const maxConfigLogPages = 5

// ListConfigLogsSince returns the entries for a config newer than the given
// time, newest first.
//
// Paging stops as soon as an entry older than the cutoff is seen, because the
// API returns them in descending order — so a config nobody has touched costs
// one request.
func (c *Client) ListConfigLogsSince(ctx context.Context, project, config string, since time.Time) ([]ConfigLogEntry, error) {
	var out []ConfigLogEntry

	for page := 1; page <= maxConfigLogPages; page++ {
		params := url.Values{
			"project":  {project},
			"config":   {config},
			"page":     {fmt.Sprintf("%d", page)},
			"per_page": {fmt.Sprintf("%d", configLogPageSize)},
		}

		result, _, err := c.Get(ctx, "/configs/config/logs", params)
		if err != nil {
			return nil, err
		}

		var resp struct {
			Logs []ConfigLogEntry `json:"logs"`
		}
		if err := json.Unmarshal(result, &resp); err != nil {
			return nil, fmt.Errorf("parsing config logs for %s/%s: %w", project, config, err)
		}
		if len(resp.Logs) == 0 {
			break
		}

		reachedCutoff := false
		for _, entry := range resp.Logs {
			if !entry.CreatedAt.After(since) {
				reachedCutoff = true
				break
			}
			out = append(out, entry)
		}
		if reachedCutoff || len(resp.Logs) < configLogPageSize {
			break
		}
	}

	return out, nil
}
