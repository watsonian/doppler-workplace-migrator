package doppler

import (
	"context"
	"encoding/json"
	"fmt"
)

// ListServiceAccounts returns all service accounts in the workplace.
func (c *Client) ListServiceAccounts(ctx context.Context) ([]ServiceAccount, error) {
	items, err := c.ListAll(ctx, "/workplace/service_accounts", nil, "service_accounts", RateLimitRead)
	if err != nil {
		return nil, err
	}

	accounts := make([]ServiceAccount, 0, len(items))
	for _, item := range items {
		var sa ServiceAccount
		if err := json.Unmarshal(item, &sa); err != nil {
			return nil, fmt.Errorf("parsing service account: %w", err)
		}
		accounts = append(accounts, sa)
	}

	return accounts, nil
}

// CreateServiceAccount creates a new service account.
func (c *Client) CreateServiceAccount(ctx context.Context, account *ServiceAccount) (*ServiceAccount, error) {
	body := map[string]interface{}{
		"name": account.Name,
	}
	// workplace_role is an object here, unlike the plain identifier groups take.
	if account.WorkplaceRole != nil && account.WorkplaceRole.Identifier != "" {
		body["workplace_role"] = map[string]string{"identifier": account.WorkplaceRole.Identifier}
	}

	result, _, err := c.Post(ctx, "/workplace/service_accounts", body)
	if err != nil {
		return nil, err
	}

	var resp struct {
		ServiceAccount ServiceAccount `json:"service_account"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		return nil, fmt.Errorf("parsing service account response: %w", err)
	}

	return &resp.ServiceAccount, nil
}
