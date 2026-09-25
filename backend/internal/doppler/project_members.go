package doppler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

// ListProjectMembers returns all members assigned to a project.
func (c *Client) ListProjectMembers(ctx context.Context, project string) ([]ProjectMember, error) {
	params := url.Values{"project": {project}}
	items, err := c.ListAll(ctx, "/projects/project/members", params, "members", RateLimitRead)
	if err != nil {
		return nil, err
	}

	members := make([]ProjectMember, 0, len(items))
	for _, item := range items {
		var m ProjectMember
		if err := json.Unmarshal(item, &m); err != nil {
			return nil, fmt.Errorf("parsing project member: %w", err)
		}
		members = append(members, m)
	}

	return members, nil
}

// AddProjectMember adds a member to a project.
func (c *Client) AddProjectMember(ctx context.Context, project string, memberType, memberSlug, role string, environments []string) error {
	// Ensure environments is never nil — the API requires an array, not null.
	if environments == nil {
		environments = []string{}
	}

	body := map[string]interface{}{
		"project":      project,
		"type":         memberType,
		"slug":         memberSlug,
		"role":         role,
		"environments": environments,
	}

	_, _, err := c.Post(ctx, "/projects/project/members", body)
	return err
}
