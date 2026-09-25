package doppler

import (
	"context"
	"encoding/json"
	"fmt"
)

// GetTokenInfo returns information about the current token via GET /v3/me.
func (c *Client) GetTokenInfo(ctx context.Context) (*TokenInfo, error) {
	result, _, err := c.Get(ctx, "/me", nil)
	if err != nil {
		return nil, err
	}

	var resp struct {
		TokenInfo
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		return nil, fmt.Errorf("parsing token info: %w", err)
	}

	// Remember who the token acts as, so a later failure can name it without
	// making a request of its own on the error path.
	c.setActor(resp.TokenInfo.Name)

	return &resp.TokenInfo, nil
}
