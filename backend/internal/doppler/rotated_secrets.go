package doppler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

// ListRotatedSecrets returns the rotated secrets configured on a config.
//
// This endpoint is not paginated — it returns the full set and ignores page
// parameters — so it must not go through ListAll, which would request page after
// page of identical results whenever a config has a full page's worth.
func (c *Client) ListRotatedSecrets(ctx context.Context, project, config string) ([]RotatedSecret, error) {
	params := url.Values{
		"project": {project},
		"config":  {config},
	}

	result, _, err := c.Get(ctx, "/configs/config/rotated_secrets", params)
	if err != nil {
		return nil, err
	}

	// Note the camelCase key — unlike most v3 list responses.
	var resp struct {
		RotatedSecrets []RotatedSecret `json:"rotatedSecrets"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		return nil, fmt.Errorf("parsing rotated secrets: %w", err)
	}

	for i := range resp.RotatedSecrets {
		if resp.RotatedSecrets[i].Project == "" {
			resp.RotatedSecrets[i].Project = project
		}
		if resp.RotatedSecrets[i].Config == "" {
			resp.RotatedSecrets[i].Config = config
		}
	}

	return resp.RotatedSecrets, nil
}
