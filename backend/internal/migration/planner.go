package migration

import (
	"context"
	"fmt"
	"strings"
)

// MigrationPlan is the complete migration plan presented for user approval.
type MigrationPlan struct {
	Summary     PlanSummary     `json:"summary"`
	Workplace   WorkplacePlan   `json:"workplace"`
	Projects    []ProjectPlan   `json:"projects"`
	ManualSteps []ManualStep    `json:"manual_steps"`
	UserCheck   UserCheckResult `json:"user_check"`
}

// PlanSummary provides aggregate counts of resources to be migrated.
type PlanSummary struct {
	ProjectCount             int `json:"project_count"`
	EnvironmentCount         int `json:"environment_count"`
	ConfigCount              int `json:"config_count"`
	SecretCount              int `json:"secret_count"`
	RoleCount                int `json:"role_count"`
	GroupCount               int `json:"group_count"`
	WebhookCount             int `json:"webhook_count"`
	ServiceAccountCount      int `json:"service_account_count"`
	TrustedIPCount           int `json:"trusted_ip_count"`
	ChangeRequestPolicyCount int `json:"change_request_policy_count"`
	PersonalConfigCount      int `json:"personal_config_count"`
	ManualStepCount          int `json:"manual_step_count"`
}

// WorkplacePlan describes workplace-level resources to migrate.
type WorkplacePlan struct {
	Settings              bool                          `json:"settings"`
	WorkplaceRoles        []string                      `json:"workplace_roles"`
	ProjectRoles          []string                      `json:"project_roles"`
	Groups                []GroupPlanItem               `json:"groups"`
	ServiceAccounts       []string                      `json:"service_accounts"`
	ChangeRequestPolicies []ChangeRequestPolicyPlanItem `json:"change_request_policies"`
}

// ChangeRequestPolicyPlanItem summarizes a policy for the plan review.
type ChangeRequestPolicyPlanItem struct {
	Name        string `json:"name"`
	RuleCount   int    `json:"rule_count"`
	AllProjects bool   `json:"all_projects"`
	// ProjectCount is the number of individually targeted projects. Zero with
	// AllProjects true means the policy covers the whole workplace.
	ProjectCount int `json:"project_count"`
}

// GroupPlanItem describes a group to be migrated.
type GroupPlanItem struct {
	Name        string `json:"name"`
	MemberCount int    `json:"member_count"`
}

// ProjectPlan describes a project and its sub-resources.
type ProjectPlan struct {
	Name         string            `json:"name"`
	Description  string            `json:"description"`
	Environments []EnvironmentPlan `json:"environments"`
	Configs      []ConfigPlanItem  `json:"configs"`
	Webhooks     []WebhookPlan     `json:"webhooks"`
	Members      []MemberPlan      `json:"members"`
}

// EnvironmentPlan describes an environment.
type EnvironmentPlan struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// ConfigPlanItem describes a config to be migrated.
type ConfigPlanItem struct {
	Name        string   `json:"name"`
	Environment string   `json:"environment"`
	Root        bool     `json:"root"`
	SecretCount int      `json:"secret_count"`
	TrustedIPs  int      `json:"trusted_ips"`
	Locked      bool     `json:"locked"`
	Inheritable bool     `json:"inheritable"`
	Inherits    []string `json:"inherits,omitempty"`
}

// WebhookPlan describes a webhook.
type WebhookPlan struct {
	URL     string `json:"url"`
	Enabled bool   `json:"enabled"`
}

// MemberPlan describes a project member.
type MemberPlan struct {
	Type string `json:"type"`
	Slug string `json:"slug"`
	Role string `json:"role"`
}

// ManualStep represents a category of manual steps the user must perform.
type ManualStep struct {
	Category    string           `json:"category"`
	Title       string           `json:"title"`
	Description string           `json:"description"`
	Items       []ManualStepItem `json:"items"`
}

// ManualStepItem represents a single item within a manual step category.
type ManualStepItem struct {
	Name    string            `json:"name"`
	Detail  string            `json:"detail"`
	Context map[string]string `json:"context,omitempty"`
}

// UserCheckResult describes the outcome of checking user existence on the target.
type UserCheckResult struct {
	Required []string `json:"required"`
	Existing []string `json:"existing"`
	Missing  []string `json:"missing"`
}

// GeneratePlan creates a migration plan from gathered data.
func GeneratePlan(data *MigrationData) *MigrationPlan {
	plan := &MigrationPlan{}

	// Workplace plan
	plan.Workplace = buildWorkplacePlan(data)

	// Project plans
	plan.Projects = buildProjectPlans(data)

	// Manual steps
	plan.ManualSteps = buildManualSteps(data)

	// Summary
	plan.Summary = buildPlanSummary(data, plan.ManualSteps)

	return plan
}

// CheckUsers checks which required users exist on the target instance.
func CheckUsers(ctx context.Context, data *MigrationData, target DopplerClient) (*UserCheckResult, error) {
	// Collect all unique user emails/slugs that are referenced in the migration
	requiredMap := make(map[string]bool)
	for _, g := range data.Groups {
		for _, m := range g.Members {
			if m.Type == "workplace_user" {
				requiredMap[m.Slug] = true
			}
		}
	}
	for _, p := range data.Projects {
		for _, m := range p.Members {
			if m.Type == "workplace_user" {
				requiredMap[m.Slug] = true
			}
		}
	}

	required := make([]string, 0, len(requiredMap))
	for slug := range requiredMap {
		required = append(required, slug)
	}

	// Get users on target
	targetUsers, err := target.ListWorkplaceUsers(ctx)
	if err != nil {
		return nil, fmt.Errorf("check users on target: %w", err)
	}

	// Build lookup of target users by email (the only stable cross-instance identifier)
	targetEmailSet := make(map[string]bool)
	for _, u := range targetUsers {
		if u.User.Email != "" {
			targetEmailSet[u.User.Email] = true
		}
	}

	// Map all source user identifiers (ID, username, email) to their canonical email
	sourceEmailMap := make(map[string]string)
	for _, u := range data.WorkplaceUsers {
		sourceEmailMap[u.ID] = u.User.Email
		sourceEmailMap[u.User.Username] = u.User.Email
		sourceEmailMap[u.User.Email] = u.User.Email
	}

	var existing, missing []string
	for _, slug := range required {
		email := sourceEmailMap[slug]
		if email == "" {
			email = slug // fallback if slug not found in source users
		}
		if targetEmailSet[email] {
			existing = append(existing, email)
		} else {
			missing = append(missing, email)
		}
	}

	return &UserCheckResult{
		Required: required,
		Existing: existing,
		Missing:  missing,
	}, nil
}

func buildWorkplacePlan(data *MigrationData) WorkplacePlan {
	wp := WorkplacePlan{
		Settings: data.Workplace != nil && !data.DeferWorkplaceLevel,
	}

	for _, r := range data.WorkplaceRoles {
		wp.WorkplaceRoles = append(wp.WorkplaceRoles, r.Name)
	}
	for _, r := range data.ProjectRoles {
		wp.ProjectRoles = append(wp.ProjectRoles, r.Name)
	}
	for _, g := range data.Groups {
		wp.Groups = append(wp.Groups, GroupPlanItem{
			Name:        g.Group.Name,
			MemberCount: len(g.Members),
		})
	}
	for _, sa := range data.ServiceAccounts {
		wp.ServiceAccounts = append(wp.ServiceAccounts, sa.Name)
	}
	for _, p := range data.ChangeRequestPolicies {
		targeted := 0
		for _, t := range p.Targets.Projects {
			if t != nil {
				targeted++
			}
		}
		wp.ChangeRequestPolicies = append(wp.ChangeRequestPolicies, ChangeRequestPolicyPlanItem{
			Name:         p.Name,
			RuleCount:    len(p.Rules),
			AllProjects:  p.Targets.AllProjects,
			ProjectCount: targeted,
		})
	}

	return wp
}

func buildProjectPlans(data *MigrationData) []ProjectPlan {
	plans := make([]ProjectPlan, 0, len(data.Projects))

	for _, p := range data.Projects {
		pp := ProjectPlan{
			Name:         p.Project.Name,
			Description:  p.Project.Description,
			Environments: []EnvironmentPlan{},
			Configs:      []ConfigPlanItem{},
			Webhooks:     []WebhookPlan{},
			Members:      []MemberPlan{},
		}

		for _, e := range p.Environments {
			pp.Environments = append(pp.Environments, EnvironmentPlan{
				Name: e.Name,
				Slug: e.Slug,
			})
		}

		for _, c := range p.Configs {
			var inheritsNames []string
			for _, desc := range c.Config.Inherits {
				inheritsNames = append(inheritsNames, desc.Config)
			}
			pp.Configs = append(pp.Configs, ConfigPlanItem{
				Name:        c.Config.Name,
				Environment: c.Config.Environment,
				Root:        c.Config.Root,
				SecretCount: secretsGathered(c),
				TrustedIPs:  len(c.TrustedIPs),
				Locked:      c.Config.Locked,
				Inheritable: c.Config.Inheritable,
				Inherits:    inheritsNames,
			})
		}

		for _, w := range p.Webhooks {
			pp.Webhooks = append(pp.Webhooks, WebhookPlan{
				URL:     w.URL,
				Enabled: w.Enabled,
			})
		}

		for _, m := range p.Members {
			pp.Members = append(pp.Members, MemberPlan{
				Type: m.Type,
				Slug: m.Slug,
				Role: m.Role.Identifier,
			})
		}

		plans = append(plans, pp)
	}

	return plans
}

func buildManualSteps(data *MigrationData) []ManualStep {
	var steps []ManualStep

	// First, because it changes how to read the rest: whatever the source
	// token could not read is missing from every other list too.
	if len(data.Unavailable) > 0 {
		items := make([]ManualStepItem, 0, len(data.Unavailable))
		for _, u := range data.Unavailable {
			ctx := map[string]string{"needs": u.Permission, "source said": u.Message}
			if u.Where > 0 {
				ctx["refused in"] = fmt.Sprintf("%d (%s)", u.Where, strings.Join(u.Examples, ", "))
			}
			items = append(items, ManualStepItem{Name: u.Label, Detail: u.Consequence, Context: ctx})
		}
		steps = append(steps, ManualStep{
			Category: "unavailable",
			Title:    "Not Readable With The Source Token",
			Description: "The source token was refused these, so they are not migrated. Give the token the permission listed " +
				"against each and gather again to include them, or carry on and set them up on the target by hand. " +
				"The Source Token Permissions page lists every permission the migrator uses.",
			Items: items,
		})
	}

	// Integrations
	if len(data.Integrations) > 0 {
		items := make([]ManualStepItem, 0, len(data.Integrations))
		for _, integ := range data.Integrations {
			items = append(items, ManualStepItem{
				Name:   integ.Name,
				Detail: "Recreate on target instance with new credentials",
				Context: map[string]string{
					"type":    integ.Type,
					"enabled": fmt.Sprintf("%t", integ.Enabled),
				},
			})
		}
		steps = append(steps, ManualStep{
			Category:    "integration",
			Title:       "Integrations",
			Description: "Integration credentials are redacted in the API and cannot be automatically migrated. Each integration must be manually reconfigured on the target instance.",
			Items:       items,
		})
	}

	// Service tokens and service account tokens are not manual steps any more.
	// Their values cannot be copied, but the migrator can issue replacements —
	// or record a decision to rebuild them by hand — on the service token page,
	// which cutover waits on. Listing them here as well would make the same
	// work appear in two places with two different ways of marking it done.

	// Rotated secrets — recreating one requires the backing integration and its
	// credentials, neither of which can be read back.
	if len(data.RotatedSecrets) > 0 {
		items := make([]ManualStepItem, 0, len(data.RotatedSecrets))
		for _, rs := range data.RotatedSecrets {
			item := ManualStepItem{
				Name:   rs.Name,
				Detail: fmt.Sprintf("Rotated secret in %s/%s via %s", rs.Project, rs.Config, rs.Integration.Name),
				Context: map[string]string{
					"project":             rs.Project,
					"config":              rs.Config,
					"integration":         rs.Integration.Name,
					"integration_type":    rs.Integration.Type,
					"rotation_period_sec": fmt.Sprintf("%d", rs.RotationPeriodSec),
					"enabled":             fmt.Sprintf("%t", rs.Enabled),
					"status":              rs.Status.Type,
				},
			}
			// A failing rotation carries a reason; surface it so the operator knows
			// the source was unhealthy before recreating it.
			if rs.Status.Error != "" {
				item.Context["status_error"] = rs.Status.Error
			}
			items = append(items, item)
		}
		steps = append(steps, ManualStep{
			Category:    "rotated_secret",
			Title:       "Rotated Secrets",
			Description: "Rotated secrets are tied to an integration and its credentials, which the API does not return. Recreate them on the target instance after reconfiguring integrations, matching the rotation period below.",
			Items:       items,
		})
	}

	// Personal configs — skipped at gather time; called out so nobody expects them.
	if len(data.PersonalConfigs) > 0 {
		items := make([]ManualStepItem, 0, len(data.PersonalConfigs))
		for _, pc := range data.PersonalConfigs {
			items = append(items, ManualStepItem{
				Name:   fmt.Sprintf("%s/%s", pc.Project, pc.Config),
				Detail: "Personal config — not migrated",
				Context: map[string]string{
					"project": pc.Project,
					"config":  pc.Config,
				},
			})
		}
		steps = append(steps, ManualStep{
			Category:    "personal_config",
			Title:       "Personal Configs (not migrated)",
			Description: "These are per-user configs. Each belongs to one person on the source instance and holds their own secret overrides, and the target instance creates its own for whichever users exist there — so there is nothing meaningful to copy. Neither the configs nor their secrets were read. Anyone relying on a personal override will need to set it again on the target.",
			Items:       items,
		})
	}

	// Webhook signing secrets — the API exposes only hasSecret, never the value, so
	// a migrated webhook is created unsigned.
	var signedWebhooks []ManualStepItem
	for _, p := range data.Projects {
		for _, w := range p.Webhooks {
			if !w.HasSecret {
				continue
			}
			signedWebhooks = append(signedWebhooks, ManualStepItem{
				Name:   w.URL,
				Detail: fmt.Sprintf("Signing secret for the webhook in %s", p.Project.Slug),
				Context: map[string]string{
					"project": p.Project.Slug,
					"url":     w.URL,
					"enabled": fmt.Sprintf("%t", w.Enabled),
				},
			})
		}
	}
	if len(signedWebhooks) > 0 {
		steps = append(steps, ManualStep{
			Category:    "webhook_secret",
			Title:       "Webhook Signing Secrets",
			Description: "These webhooks are signed on the source, but the API never returns a signing secret. The migrated webhooks are created unsigned — set each secret again on the target so receivers can keep verifying payloads.",
			Items:       signedWebhooks,
		})
	}

	// Secrets Syncs (extracted from integrations)
	var syncItems []ManualStepItem
	for _, integ := range data.Integrations {
		for _, sync := range integ.Syncs {
			syncItems = append(syncItems, ManualStepItem{
				Name:   sync.Description,
				Detail: fmt.Sprintf("Sync for %s/%s via %s", sync.Project, sync.Config, integ.Name),
				Context: map[string]string{
					"integration": integ.Name,
					"project":     sync.Project,
					"config":      sync.Config,
					"status":      sync.Status,
					"enabled":     fmt.Sprintf("%t", sync.Enabled),
				},
			})
		}
	}
	if len(syncItems) > 0 {
		steps = append(steps, ManualStep{
			Category:    "sync",
			Title:       "Secrets Syncs",
			Description: "Secrets syncs are tied to integrations and must be manually recreated on the target instance after reconfiguring integrations.",
			Items:       syncItems,
		})
	}

	return steps
}

func buildPlanSummary(data *MigrationData, manualSteps []ManualStep) PlanSummary {
	var envCount, configCount, secretCount, webhookCount, trustedIPCount int
	for _, p := range data.Projects {
		envCount += len(p.Environments)
		configCount += len(p.Configs)
		for _, c := range p.Configs {
			secretCount += secretsGathered(c)
			trustedIPCount += len(c.TrustedIPs)
		}
		webhookCount += len(p.Webhooks)
	}

	manualStepCount := 0
	for _, ms := range manualSteps {
		manualStepCount += len(ms.Items)
	}

	return PlanSummary{
		ProjectCount:             len(data.Projects),
		EnvironmentCount:         envCount,
		ConfigCount:              configCount,
		SecretCount:              secretCount,
		RoleCount:                len(data.WorkplaceRoles) + len(data.ProjectRoles),
		GroupCount:               len(data.Groups),
		WebhookCount:             webhookCount,
		ServiceAccountCount:      len(data.ServiceAccounts),
		TrustedIPCount:           trustedIPCount,
		ChangeRequestPolicyCount: len(data.ChangeRequestPolicies),
		PersonalConfigCount:      len(data.PersonalConfigs),
		ManualStepCount:          manualStepCount,
	}
}

// secretsGathered is how many secrets a config holds on the source.
//
// Not len(Secrets): values are dropped once a config is written, so counting
// what is still in memory made a summary built after a run under-count the
// source — a partial run reported "145 of 113 secrets", the first figure from
// the plan made before the run and the second from data the run had cleared.
func secretsGathered(c ConfigData) int {
	return max(c.SecretCount, len(c.Secrets))
}
