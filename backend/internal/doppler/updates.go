package doppler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// The update endpoints.
//
// Migrating used to mean creating: every write was a create that tolerated a
// 409, which is enough to carry a workplace across once and not enough to keep
// one in step. These are what convergence needs — the API has had them all
// along and the migrator simply never called them.

// UpdateWorkplaceRole replaces a custom workplace role's permissions.
func (c *Client) UpdateWorkplaceRole(ctx context.Context, identifier string, name string, permissions []string) (*Role, error) {
	body := map[string]interface{}{
		"name":        name,
		"permissions": permissions,
	}

	result, _, err := c.Patch(ctx, "/workplace/roles/role/"+url.PathEscape(identifier), body)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Role Role `json:"role"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		return nil, fmt.Errorf("parsing updated workplace role: %w", err)
	}
	return &resp.Role, nil
}

// UpdateProjectRole replaces a custom project role's permissions.
func (c *Client) UpdateProjectRole(ctx context.Context, identifier string, name string, permissions []string) (*Role, error) {
	body := map[string]interface{}{
		"name":        name,
		"permissions": permissions,
	}

	result, _, err := c.Patch(ctx, "/projects/roles/role/"+url.PathEscape(identifier), body)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Role Role `json:"role"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		return nil, fmt.Errorf("parsing updated project role: %w", err)
	}
	return &resp.Role, nil
}

// UpdateGroup changes a group's name and default project role.
func (c *Client) UpdateGroup(ctx context.Context, slug string, group *Group) (*Group, error) {
	body := map[string]interface{}{
		"name": group.Name,
	}
	if group.DefaultRole != nil && group.DefaultRole.Identifier != "" {
		body["default_project_role"] = group.DefaultRole.Identifier
	}

	result, _, err := c.Patch(ctx, "/workplace/groups/group/"+url.PathEscape(slug), body)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Group Group `json:"group"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		return nil, fmt.Errorf("parsing updated group: %w", err)
	}
	return &resp.Group, nil
}

// ReplaceGroupMembers sets a group's membership to exactly the given list.
//
// The only converging write in the whole API: send the membership you want and
// the server reconciles it, so a member removed on the source is removed here
// without the migrator having to work out that it was. Everything else in this
// file is a read-diff-write by hand.
func (c *Client) ReplaceGroupMembers(ctx context.Context, slug string, members []GroupMember) error {
	type memberRef struct {
		Type string `json:"type"`
		Slug string `json:"slug"`
	}

	refs := make([]memberRef, 0, len(members))
	for _, m := range members {
		refs = append(refs, memberRef{Type: m.Type, Slug: m.Slug})
	}

	// Sent unconditionally, including when empty: an empty list is a legitimate
	// membership, and omitting the field would read as "leave it alone".
	body := map[string]interface{}{"members": refs}

	_, _, err := c.Put(ctx, "/workplace/groups/group/"+url.PathEscape(slug)+"/members", body)
	return err
}

// UpdateServiceAccount changes a service account's name and workplace role.
func (c *Client) UpdateServiceAccount(ctx context.Context, slug string, account *ServiceAccount) (*ServiceAccount, error) {
	body := map[string]interface{}{
		"name": account.Name,
	}
	if account.WorkplaceRole != nil && account.WorkplaceRole.Identifier != "" {
		body["workplace_role"] = account.WorkplaceRole.Identifier
	}

	params := url.Values{"service_account": {slug}}
	result, _, err := c.Patch(ctx, "/workplace/service_accounts/service_account?"+params.Encode(), body)
	if err != nil {
		return nil, err
	}

	var resp struct {
		ServiceAccount ServiceAccount `json:"service_account"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		return nil, fmt.Errorf("parsing updated service account: %w", err)
	}
	return &resp.ServiceAccount, nil
}

// UpdateProjectMember changes a member's role and environment scope on a project.
func (c *Client) UpdateProjectMember(ctx context.Context, project, memberType, memberSlug, role string, environments []string) error {
	body := map[string]interface{}{
		"role": role,
	}
	// A nil environments list means every environment; an empty one means none.
	// They are different states, so the field is sent whenever it is non-nil.
	if environments != nil {
		body["environments"] = environments
	}

	params := url.Values{"project": {project}}
	path := fmt.Sprintf("/projects/project/members/member/%s/%s?%s",
		url.PathEscape(memberType), url.PathEscape(memberSlug), params.Encode())

	_, _, err := c.Patch(ctx, path, body)
	return err
}

// UpdateWebhook changes a webhook's destination and delivery settings.
//
// Not its secret: the API never returns one, so the migrator has nothing to
// compare against and nothing to send. A changed secret stays a manual step.
func (c *Client) UpdateWebhook(ctx context.Context, project, slug string, webhook *Webhook) (*Webhook, error) {
	body := map[string]interface{}{
		"url":     webhook.URL,
		"enabled": webhook.Enabled,
	}
	if len(webhook.EnabledConfigs) > 0 {
		body["enableConfigs"] = webhook.EnabledConfigs
	}

	params := url.Values{"project": {project}}
	result, _, err := c.Patch(ctx, "/webhooks/webhook/"+url.PathEscape(slug)+"?"+params.Encode(), body)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Webhook Webhook `json:"webhook"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		return nil, fmt.Errorf("parsing updated webhook: %w", err)
	}
	return &resp.Webhook, nil
}

// UpdateEnvironment renames an environment. The slug is the identity and does
// not change; only the display name does.
func (c *Client) UpdateEnvironment(ctx context.Context, project, slug, name string) (*Environment, error) {
	body := map[string]interface{}{"name": name}

	params := url.Values{
		"project":     {project},
		"environment": {slug},
	}
	result, _, err := c.Put(ctx, "/environments/environment?"+params.Encode(), body)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Environment Environment `json:"environment"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		return nil, fmt.Errorf("parsing updated environment: %w", err)
	}
	return &resp.Environment, nil
}

// DeleteSecrets removes secrets from a config by name.
//
// Uses the change_requests format with shouldDelete, which is the same write
// path as everything else and therefore carries the same conflict semantics.
func (c *Client) DeleteSecrets(ctx context.Context, project, config string, names []string) error {
	if len(names) == 0 {
		return nil
	}

	changes := make([]SecretChangeRequest, 0, len(names))
	for _, name := range names {
		changes = append(changes, SecretChangeRequest{
			Name:         name,
			OriginalName: &name,
			Value:        nil,
			ShouldDelete: true,
		})
	}

	body := map[string]interface{}{
		"project":         project,
		"config":          config,
		"change_requests": changes,
	}

	_, _, err := c.Post(ctx, "/configs/config/secrets", body)
	return err
}

// DeleteConfig removes a config from the target.
func (c *Client) DeleteConfig(ctx context.Context, project, config string) error {
	body := map[string]string{
		"project": project,
		"config":  config,
	}

	params := url.Values{"project": {project}}
	_, _, err := c.doRequest(ctx, http.MethodDelete, "/configs/config?"+params.Encode(), body, nil, RateLimitWrite)
	return err
}
