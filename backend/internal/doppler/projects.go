package doppler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

// ListProjects returns all projects in the workplace.
func (c *Client) ListProjects(ctx context.Context) ([]Project, error) {
	items, err := c.ListAll(ctx, "/projects", nil, "projects", RateLimitRead)
	if err != nil {
		return nil, err
	}

	projects := make([]Project, 0, len(items))
	for _, item := range items {
		var p Project
		if err := json.Unmarshal(item, &p); err != nil {
			return nil, fmt.Errorf("parsing project: %w", err)
		}
		projects = append(projects, p)
	}

	return projects, nil
}

// GetProject returns a single project by slug.
func (c *Client) GetProject(ctx context.Context, slug string) (*Project, error) {
	params := url.Values{"project": {slug}}
	result, _, err := c.Get(ctx, "/projects/project", params)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Project Project `json:"project"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		return nil, fmt.Errorf("parsing project: %w", err)
	}

	return &resp.Project, nil
}

// CreateProject creates a new project.
func (c *Client) CreateProject(ctx context.Context, name, description string) (*Project, error) {
	body := map[string]interface{}{
		"name":        name,
		"description": description,
		// The server defaults this to true. Letting it seed dev/stg/prd would add
		// environments the source may not have, and those extras keep their own
		// ordering values and interleave with the order migrated from the source.
		"create_default_environments": false,
	}

	result, _, err := c.Post(ctx, "/projects", body)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Project Project `json:"project"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		return nil, fmt.Errorf("parsing project response: %w", err)
	}

	return &resp.Project, nil
}
