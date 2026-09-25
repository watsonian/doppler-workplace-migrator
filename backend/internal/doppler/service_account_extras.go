package doppler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"
)

// NewServiceAccountToken is a token that has just been created. APIKey holds the
// value, returned once. Like NewServiceToken, none of it is persisted.
type NewServiceAccountToken struct {
	Name           string
	Slug           string
	APIKey         string
	ServiceAccount string
	ExpiresAt      *string
}

// CreateServiceAccountToken creates an API token under a service account.
//
// These carry no permissions of their own — they inherit the account's workplace
// role — so placing one under the right account is the whole of reproducing it.
func (c *Client) CreateServiceAccountToken(ctx context.Context, serviceAccount, name string, expiresAt *time.Time) (*NewServiceAccountToken, error) {
	body := map[string]interface{}{"name": name}
	if expiresAt != nil {
		// This endpoint takes a date string rather than the unix timestamp its
		// config-token counterpart wants.
		body["expires_at"] = expiresAt.UTC().Format(time.RFC3339)
	}

	result, _, err := c.Post(ctx, serviceAccountPath(serviceAccount, "/tokens"), body)
	if err != nil {
		return nil, err
	}

	var resp struct {
		APIToken struct {
			Name      string  `json:"name"`
			Slug      string  `json:"slug"`
			ExpiresAt *string `json:"expires_at"`
		} `json:"api_token"`
		APIKey string `json:"api_key"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		// Deliberately not wrapping the body: on this path it holds the token.
		return nil, fmt.Errorf("parsing the created service account token: malformed response")
	}
	if resp.APIKey == "" {
		return nil, fmt.Errorf("the target created the token but returned no value for it")
	}

	return &NewServiceAccountToken{
		Name:           resp.APIToken.Name,
		Slug:           resp.APIToken.Slug,
		APIKey:         resp.APIKey,
		ServiceAccount: serviceAccount,
		ExpiresAt:      resp.APIToken.ExpiresAt,
	}, nil
}

// DeleteServiceAccountToken removes one token from a service account.
func (c *Client) DeleteServiceAccountToken(ctx context.Context, serviceAccount, tokenSlug string) error {
	_, _, err := c.Delete(ctx, serviceAccountPath(serviceAccount, "/tokens/token/"+url.PathEscape(tokenSlug)), nil)
	return err
}

// serviceAccountPath builds the path for a resource nested under a service account.
func serviceAccountPath(slug, sub string) string {
	return "/workplace/service_accounts/service_account/" + slug + sub
}

// ListServiceAccountTokens returns the API tokens belonging to a service account.
// Values are not retrievable — this is metadata only, like config service tokens.
func (c *Client) ListServiceAccountTokens(ctx context.Context, serviceAccount string) ([]ServiceAccountToken, error) {
	items, err := c.ListAll(ctx, serviceAccountPath(serviceAccount, "/tokens"), nil, "api_tokens", RateLimitRead)
	if err != nil {
		return nil, err
	}

	tokens := make([]ServiceAccountToken, 0, len(items))
	for _, item := range items {
		var t ServiceAccountToken
		if err := json.Unmarshal(item, &t); err != nil {
			return nil, fmt.Errorf("parsing service account token: %w", err)
		}
		t.ServiceAccount = serviceAccount
		tokens = append(tokens, t)
	}

	return tokens, nil
}

// ListServiceAccountIdentities returns the keyless auth identities attached to a
// service account.
func (c *Client) ListServiceAccountIdentities(ctx context.Context, serviceAccount string) ([]ServiceAccountIdentity, error) {
	items, err := c.ListAll(ctx, serviceAccountPath(serviceAccount, "/identities"), nil, "identities", RateLimitRead)
	if err != nil {
		return nil, err
	}

	identities := make([]ServiceAccountIdentity, 0, len(items))
	for _, item := range items {
		var id ServiceAccountIdentity
		if err := json.Unmarshal(item, &id); err != nil {
			return nil, fmt.Errorf("parsing service account identity: %w", err)
		}
		id.ServiceAccount = serviceAccount
		identities = append(identities, id)
	}

	return identities, nil
}

// CreateServiceAccountIdentity creates an identity on a target service account.
// The source slug is dropped so the target assigns its own.
func (c *Client) CreateServiceAccountIdentity(ctx context.Context, serviceAccount string, identity *ServiceAccountIdentity) (*ServiceAccountIdentity, error) {
	body := map[string]interface{}{
		"name":        identity.Name,
		"method":      identity.Method,
		"ttl_seconds": identity.TTLSeconds,
	}
	if len(identity.Config) > 0 {
		body["config"] = identity.Config
	}

	result, _, err := c.Post(ctx, serviceAccountPath(serviceAccount, "/identities"), body)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Identity ServiceAccountIdentity `json:"identity"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		return nil, fmt.Errorf("parsing service account identity response: %w", err)
	}

	return &resp.Identity, nil
}
