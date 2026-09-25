package doppler

import (
	"context"
	"encoding/json"
	"fmt"
)

// ListGroups returns all workplace groups.
func (c *Client) ListGroups(ctx context.Context) ([]Group, error) {
	items, err := c.ListAll(ctx, "/workplace/groups", nil, "groups", RateLimitRead)
	if err != nil {
		return nil, err
	}

	groups := make([]Group, 0, len(items))
	for _, item := range items {
		var g Group
		if err := json.Unmarshal(item, &g); err != nil {
			return nil, fmt.Errorf("parsing group: %w", err)
		}
		groups = append(groups, g)
	}

	return groups, nil
}

// CreateGroup creates a new workplace group.
func (c *Client) CreateGroup(ctx context.Context, group *Group) (*Group, error) {
	body := map[string]interface{}{
		"name": group.Name,
	}
	// Roles are what make the group mean anything; creating by name alone lands it
	// on the target's defaults.
	if group.DefaultRole != nil && group.DefaultRole.Identifier != "" {
		body["default_project_role"] = group.DefaultRole.Identifier
	}
	if group.WorkplaceRole != nil && group.WorkplaceRole.Identifier != "" {
		body["workplace_role"] = group.WorkplaceRole.Identifier
	}

	result, _, err := c.Post(ctx, "/workplace/groups", body)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Group Group `json:"group"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		return nil, fmt.Errorf("parsing group response: %w", err)
	}

	return &resp.Group, nil
}

// GetGroupMembers returns the members of a group.
func (c *Client) GetGroupMembers(ctx context.Context, groupSlug string) ([]GroupMember, error) {
	path := fmt.Sprintf("/workplace/groups/group/%s/members", groupSlug)
	items, err := c.ListAll(ctx, path, nil, "members", RateLimitRead)
	if err != nil {
		return nil, err
	}

	members := make([]GroupMember, 0, len(items))
	for _, item := range items {
		var m GroupMember
		if err := json.Unmarshal(item, &m); err != nil {
			return nil, fmt.Errorf("parsing group member: %w", err)
		}
		members = append(members, m)
	}

	return members, nil
}

// AddGroupMember adds a member to a group.
func (c *Client) AddGroupMember(ctx context.Context, groupSlug string, memberType, memberSlug string) error {
	path := fmt.Sprintf("/workplace/groups/group/%s/members", groupSlug)
	body := map[string]string{
		"type": memberType,
		"slug": memberSlug,
	}

	_, _, err := c.Post(ctx, path, body)
	return err
}
