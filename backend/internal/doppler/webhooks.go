package doppler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

// ListWebhooks returns all webhooks for a project.
func (c *Client) ListWebhooks(ctx context.Context, project string) ([]Webhook, error) {
	params := url.Values{"project": {project}}
	items, err := c.ListAll(ctx, "/webhooks", params, "webhooks", RateLimitRead)
	if err != nil {
		return nil, err
	}

	webhooks := make([]Webhook, 0, len(items))
	for _, item := range items {
		var wh Webhook
		if err := json.Unmarshal(item, &wh); err != nil {
			return nil, fmt.Errorf("parsing webhook: %w", err)
		}
		webhooks = append(webhooks, wh)
	}

	return webhooks, nil
}

// CreateWebhook creates a new webhook for a project.
func (c *Client) CreateWebhook(ctx context.Context, project string, webhook *Webhook) (*Webhook, error) {
	body := map[string]interface{}{
		"project": project,
		"url":     webhook.URL,
		"secret":  webhook.Secret,
		"enabled": webhook.Enabled,
		// Note the asymmetry: the API returns "enabledConfigs" on read but expects
		// "enableConfigs" on write. Sending the read spelling is silently dropped,
		// which creates the webhook enabled for no configs at all.
		"enableConfigs": webhook.EnabledConfigs,
	}

	result, _, err := c.Post(ctx, "/webhooks", body)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Webhook Webhook `json:"webhook"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		return nil, fmt.Errorf("parsing webhook response: %w", err)
	}

	return &resp.Webhook, nil
}
