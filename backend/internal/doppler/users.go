package doppler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

// workplaceUsersPageSize is what GET /v3/workplace/users actually returns per page.
// The endpoint accepts no per_page parameter and hard-codes 20 server-side, which
// looks like an upstream oversight — most v3 list endpoints take per_page. It is
// called out here so that if upstream adds per_page later, this stops being needed
// rather than silently capping the list.
const workplaceUsersPageSize = 20

// ListWorkplaceUsers returns all users in the workplace.
//
// Special-cased rather than using the shared per_page: see workplaceUsersPageSize.
// ListAll pages until a response comes back empty, so the hard-coded page size is
// handled correctly — but a stop rule based on the requested per_page would have
// truncated every workplace to its first 20 users.
func (c *Client) ListWorkplaceUsers(ctx context.Context) ([]WorkplaceUser, error) {
	items, err := c.ListAll(ctx, "/workplace/users", nil, "workplace_users", RateLimitRead)
	if err != nil {
		return nil, err
	}

	users := make([]WorkplaceUser, 0, len(items))
	for _, item := range items {
		var u WorkplaceUser
		if err := json.Unmarshal(item, &u); err != nil {
			return nil, fmt.Errorf("parsing workplace user: %w", err)
		}
		users = append(users, u)
	}

	return users, nil
}

// UpdateWorkplaceUserAccess sets a workplace user's access level. The access value
// is a workplace role identifier, which may name a custom role.
func (c *Client) UpdateWorkplaceUserAccess(ctx context.Context, userSlug, access string) error {
	body := map[string]string{"access": access}
	_, _, err := c.Patch(ctx, "/workplace/users/"+url.PathEscape(userSlug), body)
	return err
}
