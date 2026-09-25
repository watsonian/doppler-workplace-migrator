package doppler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

// ListEnvironments returns all environments for a project.
func (c *Client) ListEnvironments(ctx context.Context, project string) ([]Environment, error) {
	params := url.Values{"project": {project}}
	items, err := c.ListAll(ctx, "/environments", params, "environments", RateLimitRead)
	if err != nil {
		return nil, err
	}

	envs := make([]Environment, 0, len(items))
	for _, item := range items {
		var e Environment
		if err := json.Unmarshal(item, &e); err != nil {
			return nil, fmt.Errorf("parsing environment: %w", err)
		}
		envs = append(envs, e)
	}

	return envs, nil
}

// CreateEnvironment creates a new environment in a project.
func (c *Client) CreateEnvironment(ctx context.Context, project, name, slug string, personalConfigs bool) (*Environment, error) {
	body := map[string]interface{}{
		"name":             name,
		"slug":             slug,
		"project":          project,
		"personal_configs": personalConfigs,
	}

	result, _, err := c.Post(ctx, "/environments", body)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Environment Environment `json:"environment"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		return nil, fmt.Errorf("parsing environment response: %w", err)
	}

	return &resp.Environment, nil
}

// SetEnvironmentOrder sets the display order of a project's environments.
// The order slice is the full list of environment slugs, first to last.
func (c *Client) SetEnvironmentOrder(ctx context.Context, project string, order []string) error {
	if len(order) == 0 {
		return nil
	}

	body := map[string]interface{}{
		"project": project,
		"order":   order,
	}

	_, _, err := c.Post(ctx, "/environments/order?project="+url.QueryEscape(project), body)
	return err
}
