package doppler

import (
	"context"
	"encoding/json"
	"fmt"
)

// ListWorkplaceRoles returns all workplace roles.
func (c *Client) ListWorkplaceRoles(ctx context.Context) ([]Role, error) {
	items, err := c.ListAll(ctx, "/workplace/roles", nil, "roles", RateLimitRead)
	if err != nil {
		return nil, err
	}

	roles := make([]Role, 0, len(items))
	for _, item := range items {
		var r Role
		if err := json.Unmarshal(item, &r); err != nil {
			return nil, fmt.Errorf("parsing workplace role: %w", err)
		}
		roles = append(roles, r)
	}

	return roles, nil
}

// CreateWorkplaceRole creates a new custom workplace role.
func (c *Client) CreateWorkplaceRole(ctx context.Context, name string, permissions []string) (*Role, error) {
	body := map[string]interface{}{
		"name":        name,
		"permissions": permissions,
	}

	result, _, err := c.Post(ctx, "/workplace/roles", body)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Role Role `json:"role"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		return nil, fmt.Errorf("parsing role response: %w", err)
	}

	return &resp.Role, nil
}

// ListProjectRoles returns all project roles.
func (c *Client) ListProjectRoles(ctx context.Context) ([]Role, error) {
	items, err := c.ListAll(ctx, "/projects/roles", nil, "roles", RateLimitRead)
	if err != nil {
		return nil, err
	}

	roles := make([]Role, 0, len(items))
	for _, item := range items {
		var r Role
		if err := json.Unmarshal(item, &r); err != nil {
			return nil, fmt.Errorf("parsing project role: %w", err)
		}
		roles = append(roles, r)
	}

	return roles, nil
}

// CreateProjectRole creates a new custom project role.
func (c *Client) CreateProjectRole(ctx context.Context, name string, permissions []string) (*Role, error) {
	body := map[string]interface{}{
		"name":        name,
		"permissions": permissions,
	}

	result, _, err := c.Post(ctx, "/projects/roles", body)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Role Role `json:"role"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		return nil, fmt.Errorf("parsing role response: %w", err)
	}

	return &resp.Role, nil
}
