package doppler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// ListConfigs returns all configs for a project.
func (c *Client) ListConfigs(ctx context.Context, project string) ([]Config, error) {
	params := url.Values{"project": {project}}
	items, err := c.ListAll(ctx, "/configs", params, "configs", RateLimitRead)
	if err != nil {
		return nil, err
	}

	configs := make([]Config, 0, len(items))
	for _, item := range items {
		var cfg Config
		if err := json.Unmarshal(item, &cfg); err != nil {
			return nil, fmt.Errorf("parsing config: %w", err)
		}
		configs = append(configs, cfg)
	}

	return configs, nil
}

// GetConfig returns a single config.
func (c *Client) GetConfig(ctx context.Context, project, config string) (*Config, error) {
	params := url.Values{
		"project": {project},
		"config":  {config},
	}
	result, _, err := c.Get(ctx, "/configs/config", params)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Config Config `json:"config"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	return &resp.Config, nil
}

// CreateConfig creates a new branch config.
func (c *Client) CreateConfig(ctx context.Context, project, environment, name string) (*Config, error) {
	body := map[string]string{
		"project":     project,
		"environment": environment,
		"name":        name,
	}

	result, _, err := c.Post(ctx, "/configs", body)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Config Config `json:"config"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		return nil, fmt.Errorf("parsing config response: %w", err)
	}

	return &resp.Config, nil
}

// LockConfig locks a config to prevent modifications.
func (c *Client) LockConfig(ctx context.Context, project, config string) error {
	body := map[string]string{
		"project": project,
		"config":  config,
	}

	_, _, err := c.Post(ctx, "/configs/config/lock", body)
	return err
}

// SetConfigInheritable sets or unsets the inheritable flag on a config.
func (c *Client) SetConfigInheritable(ctx context.Context, project, config string, inheritable bool) error {
	body := map[string]interface{}{
		"project":     project,
		"config":      config,
		"inheritable": inheritable,
	}

	_, _, err := c.Post(ctx, "/configs/config/inheritable", body)
	return err
}

// SetConfigInherits sets which configs the given config inherits from, replacing
// any existing relationships. Each source config must already be marked inheritable.
func (c *Client) SetConfigInherits(ctx context.Context, project, config string, inherits []ConfigDescriptor) error {
	if inherits == nil {
		inherits = []ConfigDescriptor{}
	}

	body := map[string]interface{}{
		"project":  project,
		"config":   config,
		"inherits": inherits,
	}

	params := url.Values{
		"project": {project},
		"config":  {config},
	}

	_, _, err := c.Post(ctx, "/configs/config/inherits?"+params.Encode(), body)
	return err
}

// personalConfigSuffix marks a config as belonging to one user. The v3 config
// payload does not expose the personal_wp_user_id the server tracks internally, so
// the name suffix is the only signal available to a client.
const personalConfigSuffix = "_personal"

// IsPersonalConfig reports whether a config is a user's personal config.
//
// These cannot be migrated in any useful sense: each belongs to one user on the
// source instance, holds that user's own secret overrides, and is created by the
// target instance itself when an environment has personal configs enabled. Copying
// one would attach another instance's user data to whichever account the migration
// runs as.
func IsPersonalConfig(name string) bool {
	return strings.HasSuffix(name, personalConfigSuffix)
}
