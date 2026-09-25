package doppler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"
)

// NewServiceToken is a token that has just been created. Key holds the value,
// which the API returns exactly once and never again.
//
// Nothing here is persisted. The value travels from this struct to the browser
// and is dropped; storing it would recreate, in the migrator's own state file,
// exactly the secret the API is careful never to hand back twice.
type NewServiceToken struct {
	Name      string  `json:"name"`
	Slug      string  `json:"slug"`
	Key       string  `json:"key"`
	Access    string  `json:"access"`
	Project   string  `json:"project"`
	Config    string  `json:"config"`
	ExpiresAt *string `json:"expires_at"`
}

// CreateServiceToken creates a service token on a config and returns its value.
//
// access is "read" or "read/write" — the same two strings ListServiceTokens
// reports, so a token's access round-trips without being inferred. expireAt is
// optional; the API rejects a time in the past and caps it a year out, so the
// caller is expected to have clamped it already.
func (c *Client) CreateServiceToken(ctx context.Context, project, config, name, access string, expireAt *time.Time) (*NewServiceToken, error) {
	body := map[string]interface{}{
		"project": project,
		"config":  config,
		"name":    name,
		"access":  access,
	}
	if expireAt != nil {
		body["expire_at"] = expireAt.Unix()
	}

	result, _, err := c.Post(ctx, "/configs/config/tokens", body)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Token NewServiceToken `json:"token"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		// Deliberately not wrapping the body: on this path it holds the token.
		return nil, fmt.Errorf("parsing the created service token: malformed response")
	}
	if resp.Token.Key == "" {
		return nil, fmt.Errorf("the target created the token but returned no value for it")
	}

	resp.Token.Project = project
	resp.Token.Config = config
	return &resp.Token, nil
}

// DeleteServiceToken removes a service token from a config, by slug.
func (c *Client) DeleteServiceToken(ctx context.Context, project, config, slug string) error {
	params := url.Values{
		"project": {project},
		"config":  {config},
		"slug":    {slug},
	}

	_, _, err := c.Delete(ctx, "/configs/config/tokens/token", params)
	return err
}

// ListServiceTokens returns all service tokens for a config.
// Note: token values are not retrievable — this returns metadata only.
func (c *Client) ListServiceTokens(ctx context.Context, project, config string) ([]ServiceToken, error) {
	params := url.Values{
		"project": {project},
		"config":  {config},
	}
	items, err := c.ListAll(ctx, "/configs/config/tokens", params, "tokens", RateLimitRead)
	if err != nil {
		return nil, err
	}

	tokens := make([]ServiceToken, 0, len(items))
	for _, item := range items {
		var st ServiceToken
		if err := json.Unmarshal(item, &st); err != nil {
			return nil, fmt.Errorf("parsing service token: %w", err)
		}
		tokens = append(tokens, st)
	}

	return tokens, nil
}
