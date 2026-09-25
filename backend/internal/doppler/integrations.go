package doppler

import (
	"context"
	"encoding/json"
	"fmt"
)

// ListIntegrations returns all integrations in the workplace.
// Note: credentials are redacted in API responses. Integrations
// must be manually reconfigured on the target instance.
func (c *Client) ListIntegrations(ctx context.Context) ([]Integration, error) {
	items, err := c.ListAll(ctx, "/integrations", nil, "integrations", RateLimitRead)
	if err != nil {
		return nil, err
	}

	integrations := make([]Integration, 0, len(items))
	for _, item := range items {
		var i Integration
		if err := json.Unmarshal(item, &i); err != nil {
			return nil, fmt.Errorf("parsing integration: %w", err)
		}
		integrations = append(integrations, i)
	}

	return integrations, nil
}
