package doppler

import (
	"context"
	"encoding/json"
	"fmt"
)

// changeRequestPoliciesPath is workplace-scoped. Policies are not config-scoped,
// despite what older revisions of docs/technical-design.md claimed.
const changeRequestPoliciesPath = "/workplace/change_request_policies"

// ListChangeRequestPolicies returns every change request policy in the workplace.
func (c *Client) ListChangeRequestPolicies(ctx context.Context) ([]ChangeRequestPolicy, error) {
	items, err := c.ListAll(ctx, changeRequestPoliciesPath, nil, "policies", RateLimitRead)
	if err != nil {
		return nil, err
	}

	policies := make([]ChangeRequestPolicy, 0, len(items))
	for _, item := range items {
		var p ChangeRequestPolicy
		if err := json.Unmarshal(item, &p); err != nil {
			return nil, fmt.Errorf("parsing change request policy: %w", err)
		}
		policies = append(policies, p)
	}

	return policies, nil
}

// CreateChangeRequestPolicy creates a change request policy on the target.
// The ID is omitted from the request body — the target assigns its own.
func (c *Client) CreateChangeRequestPolicy(ctx context.Context, policy *ChangeRequestPolicy) (*ChangeRequestPolicy, error) {
	body := ChangeRequestPolicy{
		Name:        policy.Name,
		Description: policy.Description,
		Rules:       normalizeRules(policy.Rules),
		Targets:     policy.Targets,
	}
	if body.Targets.Projects == nil {
		body.Targets.Projects = map[string]*ChangeRequestPolicyTarget{}
	}

	result, _, err := c.Post(ctx, changeRequestPoliciesPath, body)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Policy ChangeRequestPolicy `json:"policy"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		return nil, fmt.Errorf("parsing change request policy response: %w", err)
	}

	return &resp.Policy, nil
}

// normalizeRules shapes rules for the upsert endpoint: subject entries carry only
// type and slug, an empty subject list becomes absent (the API rejects an empty
// list but reads an absent one as "any reviewer"), and fields belonging to other
// rule types are dropped.
func normalizeRules(rules []ChangeRequestPolicyRule) []ChangeRequestPolicyRule {
	out := make([]ChangeRequestPolicyRule, 0, len(rules))
	for _, r := range rules {
		nr := ChangeRequestPolicyRule{Type: r.Type}

		switch r.Type {
		case RuleRequiredReviewer:
			nr.Count = r.Count
			if len(r.Subjects) > 0 {
				subjects := make([]ChangeRequestPolicySubject, 0, len(r.Subjects))
				for _, s := range r.Subjects {
					subjects = append(subjects, ChangeRequestPolicySubject{Type: s.Type, Slug: s.Slug})
				}
				nr.Subjects = subjects
			}
		case RuleAutoAssignReviewers:
			nr.Strategy = r.Strategy
		}

		out = append(out, nr)
	}
	return out
}
