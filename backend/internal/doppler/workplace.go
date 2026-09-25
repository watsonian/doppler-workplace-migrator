package doppler

import (
	"context"
	"encoding/json"
	"fmt"
)

// GetWorkplace returns the workplace settings.
func (c *Client) GetWorkplace(ctx context.Context) (*Workplace, error) {
	result, _, err := c.Get(ctx, "/workplace", nil)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Workplace Workplace `json:"workplace"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		return nil, fmt.Errorf("parsing workplace: %w", err)
	}

	return &resp.Workplace, nil
}

// UpdateWorkplace updates the workplace settings on the target. The name is only
// sent when includeName is set — see MigrationConfig.MigrateWorkplaceName.
func (c *Client) UpdateWorkplace(ctx context.Context, wp *Workplace, includeName bool) (*Workplace, error) {
	// Only send fields that have a value: the validator rejects empty strings, so
	// a source workplace without a billing or security email would fail the whole
	// update. Note that changing these emails triggers a verification flow on the
	// target rather than setting them outright.
	body := map[string]string{}
	if includeName && wp.Name != "" {
		body["name"] = wp.Name
	}
	if wp.BillingEmail != "" {
		body["billing_email"] = wp.BillingEmail
	}
	if wp.SecurityEmail != "" {
		body["security_email"] = wp.SecurityEmail
	}

	result, _, err := c.Post(ctx, "/workplace", body)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Workplace Workplace `json:"workplace"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		return nil, fmt.Errorf("parsing workplace response: %w", err)
	}

	return &resp.Workplace, nil
}
