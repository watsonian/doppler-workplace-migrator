package migration

import (
	"errors"
	"net/http"
	"sort"

	"github.com/doppler/onprem-migrator/internal/doppler"
)

// Resources the gather can go without. Each is either gated by a permission
// that has no read-only form — so a read-only source token cannot list it —
// or switched off by the source workplace's plan, in which case there is
// nothing of it to migrate anyway. A refusal to read one is recorded and the
// gather carries on; everything else a refusal stops.
const (
	ResourceGroups                   = "groups"
	ResourceServiceAccountTokens     = "service_account_tokens"
	ResourceServiceAccountIdentities = "service_account_identities"
	ResourceIntegrations             = "integrations"
	ResourceChangeRequestPolicies    = "change_request_policies"
	ResourceWebhooks                 = "webhooks"
	ResourceProjectMembers           = "project_members"
	ResourceServiceTokens            = "service_tokens"
	ResourceRotatedSecrets           = "rotated_secrets"
	ResourceTrustedIPs               = "trusted_ips"
)

// UnavailableResource is a kind of thing the source token could not read,
// and so the migration leaves out.
type UnavailableResource struct {
	Resource string `json:"resource"`
	// Label is how it reads: "Webhooks".
	Label string `json:"label"`
	// Consequence says what the migration does without it.
	Consequence string `json:"consequence"`
	// Permission is what the source token would need to read it.
	Permission string `json:"permission"`
	// Message is the source's own refusal, from the first one.
	Message string `json:"message"`
	// Where counts the places it was refused (projects, configs, accounts);
	// Examples names a few of them.
	Where    int      `json:"where"`
	Examples []string `json:"examples,omitempty"`
}

var unavailableInfo = map[string]struct{ label, consequence, permission string }{
	ResourceGroups: {"Groups", "Groups and their memberships are not migrated.",
		"team, and the workplace's plan must include groups"},
	ResourceServiceAccountTokens: {"Service account tokens", "Service account tokens are not listed, so none can be issued on the target or reported.",
		"service_account_api_tokens"},
	ResourceServiceAccountIdentities: {"Service account identities", "OIDC identities of service accounts are not migrated.",
		"service_account_identities, and the workplace's plan must include service account identities"},
	ResourceIntegrations: {"Integrations", "Integrations are not listed in the manual steps.",
		"workplace_integrations_list"},
	ResourceChangeRequestPolicies: {"Change request policies", "Change request policies are not migrated.",
		"change_request_policy_read, and the workplace's plan must include change request policies"},
	ResourceWebhooks: {"Webhooks", "Webhooks are not migrated.",
		"enclave_project_webhooks (Manage Project Webhooks) on each project"},
	ResourceProjectMembers: {"Project members", "Project members are not migrated; access to each project on the target has to be granted by hand.",
		"enclave_project_members (Manage Project Members) on each project"},
	ResourceServiceTokens: {"Service tokens", "Service tokens are not listed, so none can be issued on the target or reported.",
		"enclave_project_config_service_tokens (Manage Service Tokens) on each project"},
	ResourceRotatedSecrets: {"Rotated secrets", "Rotated secrets are not listed in the manual steps.",
		"enclave_project_config_rotated_secrets_read, and the workplace's plan must include secrets rotation"},
	ResourceTrustedIPs: {"Trusted IP ranges", "Trusted IP ranges are not mirrored; the target's configs keep their defaults, and cutover has nothing to apply for them.",
		"enclave_project_config_trusted_ips (Manage Trusted IPs) on each project"},
}

// exampleCap is how many places an entry names.
const exampleCap = 5

// isRefusal reports whether an error is the source declining a read — a 403,
// whether for a missing permission or a feature the plan does not include.
func isRefusal(err error) bool {
	var apiErr *doppler.APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusForbidden
}

// refused records a refusal to read a resource that the migration can go
// without, and reports whether it was one. Anything else is left for the
// caller to fail on.
func (g *Gatherer) refused(resource, where string, err error) bool {
	if !isRefusal(err) {
		return false
	}
	g.unavailableMu.Lock()
	defer g.unavailableMu.Unlock()
	if g.unavailable == nil {
		g.unavailable = map[string]*UnavailableResource{}
	}
	u, ok := g.unavailable[resource]
	if !ok {
		info := unavailableInfo[resource]
		message := err.Error()
		var apiErr *doppler.APIError
		if errors.As(err, &apiErr) && apiErr.Message != "" {
			message = apiErr.Message
		}
		u = &UnavailableResource{
			Resource: resource, Label: info.label, Consequence: info.consequence,
			Permission: info.permission, Message: message,
		}
		g.unavailable[resource] = u
	}
	if where != "" {
		u.Where++
		if len(u.Examples) < exampleCap {
			u.Examples = append(u.Examples, where)
		}
	}
	return true
}

// unavailableList is the refusals in a stable order.
func (g *Gatherer) unavailableList() []UnavailableResource {
	g.unavailableMu.Lock()
	defer g.unavailableMu.Unlock()
	out := make([]UnavailableResource, 0, len(g.unavailable))
	for _, u := range g.unavailable {
		sort.Strings(u.Examples)
		out = append(out, *u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}
