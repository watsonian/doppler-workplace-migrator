package migration

import (
	"context"
	"fmt"
	"testing"

	"github.com/doppleruniversity/workplace-migrator/internal/doppler"
)

// mockClient implements DopplerClient for testing.
type mockClient struct {
	workplace                *doppler.Workplace
	workplaceErr             error
	users                    []doppler.WorkplaceUser
	usersErr                 error
	workplaceRoles           []doppler.Role
	workplaceRolesErr        error
	projectRoles             []doppler.Role
	projectRolesErr          error
	groups                   []doppler.Group
	groupsErr                error
	groupMembers             map[string][]doppler.GroupMember
	groupMembersErr          map[string]error
	serviceAccounts          []doppler.ServiceAccount
	serviceAccountsErr       error
	integrations             []doppler.Integration
	integrationsErr          error
	projects                 []doppler.Project
	projectsErr              error
	environments             map[string][]doppler.Environment
	environmentsErr          map[string]error
	configs                  map[string][]doppler.Config
	configsErr               map[string]error
	secrets                  map[string]doppler.SecretsMap // key: "project/config"
	secretsErr               map[string]error
	trustedIPs               map[string][]doppler.TrustedIP
	trustedIPsErr            map[string]error
	serviceTokens            map[string][]doppler.ServiceToken
	serviceTokensErr         map[string]error
	webhooks                 map[string][]doppler.Webhook
	webhooksErr              map[string]error
	members                  map[string][]doppler.ProjectMember
	membersErr               map[string]error
	changeRequestPolicies    []doppler.ChangeRequestPolicy
	changeRequestPoliciesErr error
	saTokens                 map[string][]doppler.ServiceAccountToken
	saTokensErr              map[string]error
	saIdentities             map[string][]doppler.ServiceAccountIdentity
	saIdentitiesErr          map[string]error
	rotatedSecrets           map[string][]doppler.RotatedSecret
	rotatedSecretsErr        map[string]error
}

func (m *mockClient) ListServiceAccountTokens(ctx context.Context, serviceAccount string) ([]doppler.ServiceAccountToken, error) {
	if err := m.saTokensErr[serviceAccount]; err != nil {
		return nil, err
	}
	return m.saTokens[serviceAccount], nil
}

func (m *mockClient) ListServiceAccountIdentities(ctx context.Context, serviceAccount string) ([]doppler.ServiceAccountIdentity, error) {
	if err := m.saIdentitiesErr[serviceAccount]; err != nil {
		return nil, err
	}
	return m.saIdentities[serviceAccount], nil
}

func (m *mockClient) ListRotatedSecrets(ctx context.Context, project, config string) ([]doppler.RotatedSecret, error) {
	key := project + "/" + config
	if err := m.rotatedSecretsErr[key]; err != nil {
		return nil, err
	}
	return m.rotatedSecrets[key], nil
}

func (m *mockClient) ListChangeRequestPolicies(ctx context.Context) ([]doppler.ChangeRequestPolicy, error) {
	if m.changeRequestPoliciesErr != nil {
		return nil, m.changeRequestPoliciesErr
	}
	return m.changeRequestPolicies, nil
}

func newMockClient() *mockClient {
	return &mockClient{
		groupMembers:     make(map[string][]doppler.GroupMember),
		groupMembersErr:  make(map[string]error),
		environments:     make(map[string][]doppler.Environment),
		environmentsErr:  make(map[string]error),
		configs:          make(map[string][]doppler.Config),
		configsErr:       make(map[string]error),
		secrets:          make(map[string]doppler.SecretsMap),
		secretsErr:       make(map[string]error),
		trustedIPs:       make(map[string][]doppler.TrustedIP),
		trustedIPsErr:    make(map[string]error),
		serviceTokens:    make(map[string][]doppler.ServiceToken),
		serviceTokensErr: make(map[string]error),
		webhooks:         make(map[string][]doppler.Webhook),
		webhooksErr:      make(map[string]error),
		members:          make(map[string][]doppler.ProjectMember),
		membersErr:       make(map[string]error),
	}
}

func (m *mockClient) GetWorkplace(ctx context.Context) (*doppler.Workplace, error) {
	return m.workplace, m.workplaceErr
}

func (m *mockClient) ListWorkplaceUsers(ctx context.Context) ([]doppler.WorkplaceUser, error) {
	return m.users, m.usersErr
}

func (m *mockClient) ListWorkplaceRoles(ctx context.Context) ([]doppler.Role, error) {
	return m.workplaceRoles, m.workplaceRolesErr
}

func (m *mockClient) ListProjectRoles(ctx context.Context) ([]doppler.Role, error) {
	return m.projectRoles, m.projectRolesErr
}

func (m *mockClient) ListGroups(ctx context.Context) ([]doppler.Group, error) {
	return m.groups, m.groupsErr
}

func (m *mockClient) GetGroupMembers(ctx context.Context, groupSlug string) ([]doppler.GroupMember, error) {
	if err, ok := m.groupMembersErr[groupSlug]; ok && err != nil {
		return nil, err
	}
	return m.groupMembers[groupSlug], nil
}

func (m *mockClient) ListServiceAccounts(ctx context.Context) ([]doppler.ServiceAccount, error) {
	return m.serviceAccounts, m.serviceAccountsErr
}

func (m *mockClient) ListIntegrations(ctx context.Context) ([]doppler.Integration, error) {
	return m.integrations, m.integrationsErr
}

func (m *mockClient) ListProjects(ctx context.Context) ([]doppler.Project, error) {
	return m.projects, m.projectsErr
}

func (m *mockClient) ListEnvironments(ctx context.Context, project string) ([]doppler.Environment, error) {
	if err, ok := m.environmentsErr[project]; ok && err != nil {
		return nil, err
	}
	return m.environments[project], nil
}

func (m *mockClient) ListConfigs(ctx context.Context, project string) ([]doppler.Config, error) {
	if err, ok := m.configsErr[project]; ok && err != nil {
		return nil, err
	}
	return m.configs[project], nil
}

func (m *mockClient) ListSecrets(ctx context.Context, project, config string) (doppler.SecretsMap, error) {
	key := project + "/" + config
	if err, ok := m.secretsErr[key]; ok && err != nil {
		return nil, err
	}
	return m.secrets[key], nil
}

func (m *mockClient) ListTrustedIPs(ctx context.Context, project, config string) ([]doppler.TrustedIP, error) {
	key := project + "/" + config
	if err, ok := m.trustedIPsErr[key]; ok && err != nil {
		return nil, err
	}
	return m.trustedIPs[key], nil
}

func (m *mockClient) ListServiceTokens(ctx context.Context, project, config string) ([]doppler.ServiceToken, error) {
	key := project + "/" + config
	if err, ok := m.serviceTokensErr[key]; ok && err != nil {
		return nil, err
	}
	return m.serviceTokens[key], nil
}

func (m *mockClient) ListWebhooks(ctx context.Context, project string) ([]doppler.Webhook, error) {
	if err, ok := m.webhooksErr[project]; ok && err != nil {
		return nil, err
	}
	return m.webhooks[project], nil
}

func (m *mockClient) ListProjectMembers(ctx context.Context, project string) ([]doppler.ProjectMember, error) {
	if err, ok := m.membersErr[project]; ok && err != nil {
		return nil, err
	}
	return m.members[project], nil
}

// fullMockClient returns a mock client pre-populated with realistic test data.
func fullMockClient() *mockClient {
	mc := newMockClient()
	mc.workplace = &doppler.Workplace{ID: "wp1", Name: "Test Workplace"}
	mc.users = []doppler.WorkplaceUser{
		{ID: "u1", User: struct {
			Email    string `json:"email"`
			Name     string `json:"name"`
			Username string `json:"username"`
		}{Email: "alice@test.com", Name: "Alice", Username: "alice"}},
		{ID: "u2", User: struct {
			Email    string `json:"email"`
			Name     string `json:"name"`
			Username string `json:"username"`
		}{Email: "bob@test.com", Name: "Bob", Username: "bob"}},
	}
	mc.workplaceRoles = []doppler.Role{
		{Name: "owner", Identifier: "owner", IsCustomRole: false},
		{Name: "admin", Identifier: "admin", IsCustomRole: false},
		{Name: "custom-role", Identifier: "custom-role", IsCustomRole: true, Permissions: []string{"read", "write"}},
	}
	mc.projectRoles = []doppler.Role{
		{Name: "viewer", Identifier: "viewer", IsCustomRole: false},
		{Name: "deployer", Identifier: "deployer", IsCustomRole: true, Permissions: []string{"deploy"}},
	}
	mc.groups = []doppler.Group{
		{Name: "Engineering", Slug: "engineering"},
		{Name: "DevOps", Slug: "devops"},
	}
	mc.groupMembers["engineering"] = []doppler.GroupMember{
		{Type: "workplace_user", Slug: "u1"},
		{Type: "workplace_user", Slug: "u2"},
	}
	mc.groupMembers["devops"] = []doppler.GroupMember{
		{Type: "workplace_user", Slug: "u2"},
	}
	mc.serviceAccounts = []doppler.ServiceAccount{
		{Name: "CI Bot", Slug: "ci-bot"},
	}
	mc.integrations = []doppler.Integration{
		{Slug: "aws-sm", Name: "AWS Secrets Manager", Type: "aws_secrets_manager", Kind: "sync", Enabled: true, Syncs: []doppler.Sync{
			{Slug: "sync-1", Project: "backend", Config: "dev", Description: "Actions: dev sync", Integration: "aws-sm", Enabled: true, Status: "synced"},
		}},
	}
	mc.projects = []doppler.Project{
		{Slug: "backend", Name: "Backend"},
		{Slug: "frontend", Name: "Frontend"},
	}

	// Backend project
	mc.environments["backend"] = []doppler.Environment{
		{Slug: "dev", Name: "Development", Project: "backend"},
		{Slug: "prd", Name: "Production", Project: "backend"},
	}
	mc.configs["backend"] = []doppler.Config{
		{Name: "dev", Root: true, Environment: "dev", Project: "backend"},
		{Name: "prd", Root: true, Environment: "prd", Project: "backend"},
	}
	mc.secrets["backend/dev"] = doppler.SecretsMap{
		"API_KEY": {Raw: "dev-key-123"},
		"DB_URL":  {Raw: "postgres://dev"},
	}
	mc.secrets["backend/prd"] = doppler.SecretsMap{
		"API_KEY": {Raw: "${dev.API_KEY}"},
	}
	mc.trustedIPs["backend/prd"] = []doppler.TrustedIP{{IP: "10.0.0.1/32"}}
	mc.serviceTokens["backend/dev"] = []doppler.ServiceToken{
		{Name: "ci-token", Slug: "ci-token", Project: "backend", Config: "dev"},
	}
	mc.serviceTokens["backend/prd"] = nil
	mc.webhooks["backend"] = []doppler.Webhook{
		{ID: "wh1", URL: "https://hooks.example.com/deploy", Enabled: true},
	}
	mc.members["backend"] = []doppler.ProjectMember{
		{Type: "workplace_user", Slug: "u1", Role: doppler.MemberRole{Identifier: "admin"}},
	}

	// Frontend project
	mc.environments["frontend"] = []doppler.Environment{
		{Slug: "dev", Name: "Development", Project: "frontend"},
	}
	mc.configs["frontend"] = []doppler.Config{
		{Name: "dev", Root: true, Environment: "dev", Project: "frontend"},
	}
	mc.secrets["frontend/dev"] = doppler.SecretsMap{
		"NEXT_PUBLIC_API": {Raw: "https://api.test.com"},
	}
	mc.webhooks["frontend"] = nil
	mc.members["frontend"] = nil

	return mc
}

func TestGatherAllComplete(t *testing.T) {
	mc := fullMockClient()
	var progress []GatherProgress
	g := NewGatherer(mc, func(p GatherProgress) {
		progress = append(progress, p)
	})

	data, err := g.GatherAll(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Workplace
	if data.Workplace == nil || data.Workplace.Name != "Test Workplace" {
		t.Error("expected workplace to be gathered")
	}

	// Users
	if len(data.WorkplaceUsers) != 2 {
		t.Errorf("expected 2 users, got %d", len(data.WorkplaceUsers))
	}

	// Custom roles only
	if len(data.WorkplaceRoles) != 1 {
		t.Errorf("expected 1 custom workplace role, got %d", len(data.WorkplaceRoles))
	}
	if data.WorkplaceRoles[0].Name != "custom-role" {
		t.Errorf("expected custom-role, got %q", data.WorkplaceRoles[0].Name)
	}

	if len(data.ProjectRoles) != 1 {
		t.Errorf("expected 1 custom project role, got %d", len(data.ProjectRoles))
	}
	if data.ProjectRoles[0].Name != "deployer" {
		t.Errorf("expected deployer, got %q", data.ProjectRoles[0].Name)
	}

	// Groups with members
	if len(data.Groups) != 2 {
		t.Errorf("expected 2 groups, got %d", len(data.Groups))
	}
	if len(data.Groups[0].Members) != 2 {
		t.Errorf("expected 2 members in engineering, got %d", len(data.Groups[0].Members))
	}
	if len(data.Groups[1].Members) != 1 {
		t.Errorf("expected 1 member in devops, got %d", len(data.Groups[1].Members))
	}

	// Service accounts
	if len(data.ServiceAccounts) != 1 {
		t.Errorf("expected 1 service account, got %d", len(data.ServiceAccounts))
	}

	// Integrations
	if len(data.Integrations) != 1 {
		t.Errorf("expected 1 integration, got %d", len(data.Integrations))
	}

	// Projects
	if len(data.Projects) != 2 {
		t.Errorf("expected 2 projects, got %d", len(data.Projects))
	}

	// Backend project details
	backendProj := data.Projects[0]
	if len(backendProj.Environments) != 2 {
		t.Errorf("expected 2 environments for backend, got %d", len(backendProj.Environments))
	}
	if len(backendProj.Configs) != 2 {
		t.Errorf("expected 2 configs for backend, got %d", len(backendProj.Configs))
	}
	if len(backendProj.Configs[0].Secrets) != 2 {
		t.Errorf("expected 2 secrets in backend/dev, got %d", len(backendProj.Configs[0].Secrets))
	}
	if len(backendProj.Configs[1].TrustedIPs) != 1 {
		t.Errorf("expected 1 trusted IP in backend/prd, got %d", len(backendProj.Configs[1].TrustedIPs))
	}
	if len(backendProj.Webhooks) != 1 {
		t.Errorf("expected 1 webhook for backend, got %d", len(backendProj.Webhooks))
	}
	if len(backendProj.Members) != 1 {
		t.Errorf("expected 1 member for backend, got %d", len(backendProj.Members))
	}

	// Frontend project details
	frontendProj := data.Projects[1]
	if len(frontendProj.Environments) != 1 {
		t.Errorf("expected 1 environment for frontend, got %d", len(frontendProj.Environments))
	}
	if len(frontendProj.Configs) != 1 {
		t.Errorf("expected 1 config for frontend, got %d", len(frontendProj.Configs))
	}
	if len(frontendProj.Configs[0].Secrets) != 1 {
		t.Errorf("expected 1 secret in frontend/dev, got %d", len(frontendProj.Configs[0].Secrets))
	}

	// Service tokens aggregated across all configs
	if len(data.ServiceTokens) != 1 {
		t.Errorf("expected 1 service token total, got %d", len(data.ServiceTokens))
	}

	// Progress events should have been emitted
	if len(progress) == 0 {
		t.Error("expected progress events, got none")
	}
}

func TestGatherProgressEvents(t *testing.T) {
	mc := fullMockClient()
	var progress []GatherProgress
	g := NewGatherer(mc, func(p GatherProgress) {
		progress = append(progress, p)
	})

	_, err := g.GatherAll(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check that we get progress for all major resources
	resources := make(map[string]bool)
	for _, p := range progress {
		resources[p.Resource] = true
	}

	expected := []string{
		"workplace", "users", "workplace_roles", "project_roles",
		"groups", "service_accounts", "integrations", "projects",
	}
	for _, r := range expected {
		if !resources[r] {
			t.Errorf("expected progress for resource %q", r)
		}
	}

	// The final groups progress message should be a summary count, not an individual group name
	var lastGroupsProgress GatherProgress
	for _, p := range progress {
		if p.Resource == "groups" {
			lastGroupsProgress = p
		}
	}
	expectedMsg := fmt.Sprintf("%d groups found", 2)
	if lastGroupsProgress.Message != expectedMsg {
		t.Errorf("expected final groups progress %q, got %q", expectedMsg, lastGroupsProgress.Message)
	}
}

func TestGatherFiltersCustomRolesOnly(t *testing.T) {
	mc := newMockClient()
	mc.workplace = &doppler.Workplace{ID: "wp1", Name: "Test"}
	mc.workplaceRoles = []doppler.Role{
		{Name: "owner", IsCustomRole: false},
		{Name: "admin", IsCustomRole: false},
		{Name: "member", IsCustomRole: false},
		{Name: "custom1", IsCustomRole: true},
		{Name: "custom2", IsCustomRole: true},
	}
	mc.projectRoles = []doppler.Role{
		{Name: "viewer", IsCustomRole: false},
		{Name: "editor", IsCustomRole: false},
		{Name: "custom-proj", IsCustomRole: true},
	}

	g := NewGatherer(mc, nil)
	data, err := g.GatherAll(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(data.WorkplaceRoles) != 2 {
		t.Errorf("expected 2 custom workplace roles, got %d", len(data.WorkplaceRoles))
	}
	if len(data.ProjectRoles) != 1 {
		t.Errorf("expected 1 custom project role, got %d", len(data.ProjectRoles))
	}
}

func TestGatherEmptyWorkplace(t *testing.T) {
	mc := newMockClient()
	mc.workplace = &doppler.Workplace{ID: "wp1", Name: "Empty Workplace"}

	g := NewGatherer(mc, nil)
	data, err := g.GatherAll(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if data.Workplace == nil {
		t.Error("expected workplace to be set")
	}
	if len(data.Projects) != 0 {
		t.Errorf("expected 0 projects, got %d", len(data.Projects))
	}
	if len(data.Groups) != 0 {
		t.Errorf("expected 0 groups, got %d", len(data.Groups))
	}
	if len(data.WorkplaceRoles) != 0 {
		t.Errorf("expected 0 custom roles, got %d", len(data.WorkplaceRoles))
	}
	if len(data.ServiceTokens) != 0 {
		t.Errorf("expected 0 service tokens, got %d", len(data.ServiceTokens))
	}
}

func TestGatherContextCancellation(t *testing.T) {
	mc := fullMockClient()
	ctx, cancel := context.WithCancel(context.Background())

	callCount := 0
	// Override ListProjects to cancel context partway through
	originalProjects := mc.projects
	mc.projects = nil
	mc.projectsErr = nil

	// We'll let workplace, users, roles, groups, service accounts, integrations succeed,
	// then cancel before projects are gathered.
	g := NewGatherer(mc, func(p GatherProgress) {
		callCount++
		if p.Resource == "integrations" && p.Current > 0 {
			// Restore projects but cancel context
			mc.projects = originalProjects
			cancel()
		}
	})

	_, err := g.GatherAll(ctx)
	if err == nil {
		t.Fatal("expected error due to context cancellation")
	}
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestGatherWorkplaceError(t *testing.T) {
	mc := newMockClient()
	mc.workplaceErr = fmt.Errorf("connection refused")

	g := NewGatherer(mc, nil)
	_, err := g.GatherAll(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); got != "gather workplace: connection refused" {
		t.Errorf("unexpected error: %s", got)
	}
}

func TestGatherProjectsError(t *testing.T) {
	mc := newMockClient()
	mc.workplace = &doppler.Workplace{ID: "wp1", Name: "Test"}
	mc.projectsErr = fmt.Errorf("rate limited")

	g := NewGatherer(mc, nil)
	_, err := g.GatherAll(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); got != "gather projects: rate limited" {
		t.Errorf("unexpected error: %s", got)
	}
}

func TestGatherSecretsError(t *testing.T) {
	mc := newMockClient()
	mc.workplace = &doppler.Workplace{ID: "wp1", Name: "Test"}
	mc.projects = []doppler.Project{{Slug: "app", Name: "App"}}
	mc.environments["app"] = []doppler.Environment{{Slug: "dev", Name: "Dev"}}
	mc.configs["app"] = []doppler.Config{{Name: "dev", Root: true, Project: "app"}}
	mc.secretsErr["app/dev"] = fmt.Errorf("forbidden")

	g := NewGatherer(mc, nil)
	_, err := g.GatherAll(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); got != "gather secrets for app/dev: forbidden" {
		t.Errorf("unexpected error: %s", got)
	}
}

func TestGatherGroupMembersError(t *testing.T) {
	mc := newMockClient()
	mc.workplace = &doppler.Workplace{ID: "wp1", Name: "Test"}
	mc.groups = []doppler.Group{{Name: "team", Slug: "team"}}
	mc.groupMembersErr["team"] = fmt.Errorf("not found")

	g := NewGatherer(mc, nil)
	_, err := g.GatherAll(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); got != `gather group members for "team": not found` {
		t.Errorf("unexpected error: %s", got)
	}
}

func TestGatherNilProgressCallback(t *testing.T) {
	mc := newMockClient()
	mc.workplace = &doppler.Workplace{ID: "wp1", Name: "Test"}

	// Should not panic with nil progress callback
	g := NewGatherer(mc, nil)
	data, err := g.GatherAll(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data.Workplace == nil {
		t.Error("expected workplace to be set")
	}
}

func TestGatherMultipleProjectsWithConfigs(t *testing.T) {
	mc := newMockClient()
	mc.workplace = &doppler.Workplace{ID: "wp1", Name: "Test"}
	mc.projects = []doppler.Project{
		{Slug: "api", Name: "API"},
		{Slug: "web", Name: "Web"},
		{Slug: "mobile", Name: "Mobile"},
	}

	for _, proj := range mc.projects {
		mc.environments[proj.Slug] = []doppler.Environment{
			{Slug: "dev", Name: "Dev"},
			{Slug: "stg", Name: "Staging"},
			{Slug: "prd", Name: "Production"},
		}
		mc.configs[proj.Slug] = []doppler.Config{
			{Name: "dev", Root: true, Environment: "dev", Project: proj.Slug},
			{Name: "stg", Root: true, Environment: "stg", Project: proj.Slug},
			{Name: "prd", Root: true, Environment: "prd", Project: proj.Slug},
		}
		for _, cfg := range mc.configs[proj.Slug] {
			key := proj.Slug + "/" + cfg.Name
			mc.secrets[key] = doppler.SecretsMap{
				"KEY": {Raw: "value"},
			}
		}
	}

	g := NewGatherer(mc, nil)
	data, err := g.GatherAll(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(data.Projects) != 3 {
		t.Errorf("expected 3 projects, got %d", len(data.Projects))
	}

	totalConfigs := 0
	totalSecrets := 0
	for _, p := range data.Projects {
		if len(p.Environments) != 3 {
			t.Errorf("project %s: expected 3 environments, got %d", p.Project.Name, len(p.Environments))
		}
		totalConfigs += len(p.Configs)
		for _, c := range p.Configs {
			totalSecrets += len(c.Secrets)
		}
	}
	if totalConfigs != 9 {
		t.Errorf("expected 9 total configs, got %d", totalConfigs)
	}
	if totalSecrets != 9 {
		t.Errorf("expected 9 total secrets, got %d", totalSecrets)
	}
}

func TestFilterCustomRoles(t *testing.T) {
	roles := []doppler.Role{
		{Name: "owner", IsCustomRole: false},
		{Name: "custom1", IsCustomRole: true},
		{Name: "admin", IsCustomRole: false},
		{Name: "custom2", IsCustomRole: true},
	}

	filtered := filterCustomRoles(roles)
	if len(filtered) != 2 {
		t.Fatalf("expected 2 custom roles, got %d", len(filtered))
	}
	if filtered[0].Name != "custom1" {
		t.Errorf("expected custom1, got %s", filtered[0].Name)
	}
	if filtered[1].Name != "custom2" {
		t.Errorf("expected custom2, got %s", filtered[1].Name)
	}
}

func TestFilterCustomRolesEmpty(t *testing.T) {
	filtered := filterCustomRoles(nil)
	if len(filtered) != 0 {
		t.Errorf("expected 0 custom roles from nil input, got %d", len(filtered))
	}

	filtered = filterCustomRoles([]doppler.Role{
		{Name: "owner", IsCustomRole: false},
	})
	if len(filtered) != 0 {
		t.Errorf("expected 0 custom roles when none are custom, got %d", len(filtered))
	}
}

func TestGatherFiltersDopplerReservedSecrets(t *testing.T) {
	mc := newMockClient()
	mc.workplace = &doppler.Workplace{ID: "wp1", Name: "Test"}
	mc.projects = []doppler.Project{{Slug: "app", Name: "App"}}
	mc.environments["app"] = []doppler.Environment{{Slug: "dev", Name: "Dev"}}
	mc.configs["app"] = []doppler.Config{
		{Name: "dev", Root: true, Project: "app"},
	}
	mc.secrets["app/dev"] = doppler.SecretsMap{
		"API_KEY":              {Raw: "my-key"},
		"DB_URL":               {Raw: "postgres://localhost"},
		"DOPPLER_CONFIG":       {Raw: "dev"},
		"DOPPLER_PROJECT":      {Raw: "app"},
		"DOPPLER_ENVIRONMENT":  {Raw: "dev"},
		"DOPPLER_ENCLAVE_TYPE": {Raw: "personal"},
	}

	g := NewGatherer(mc, nil)
	data, err := g.GatherAll(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	secrets := data.Projects[0].Configs[0].Secrets
	// Only user secrets should remain
	if len(secrets) != 2 {
		t.Errorf("expected 2 secrets after filtering, got %d: %v", len(secrets), secretNames(secrets))
	}
	if _, ok := secrets["API_KEY"]; !ok {
		t.Error("expected API_KEY to be present")
	}
	if _, ok := secrets["DB_URL"]; !ok {
		t.Error("expected DB_URL to be present")
	}
	// DOPPLER_* should all be filtered out
	for name := range secrets {
		if name == "DOPPLER_CONFIG" || name == "DOPPLER_PROJECT" || name == "DOPPLER_ENVIRONMENT" || name == "DOPPLER_ENCLAVE_TYPE" {
			t.Errorf("expected %s to be filtered out", name)
		}
	}
}

func secretNames(m doppler.SecretsMap) []string {
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	return names
}

func TestGatherPreservesSecretRawValues(t *testing.T) {
	mc := newMockClient()
	mc.workplace = &doppler.Workplace{ID: "wp1", Name: "Test"}
	mc.projects = []doppler.Project{{Slug: "app", Name: "App"}}
	mc.environments["app"] = []doppler.Environment{{Slug: "dev", Name: "Dev"}}
	mc.configs["app"] = []doppler.Config{
		{Name: "dev", Root: true, Project: "app"},
	}
	mc.secrets["app/dev"] = doppler.SecretsMap{
		"REF_SECRET": {Raw: "${prd.API_KEY}"},
		"PLAIN":      {Raw: "plain-value"},
	}

	g := NewGatherer(mc, nil)
	data, err := g.GatherAll(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	secrets := data.Projects[0].Configs[0].Secrets
	if secrets["REF_SECRET"].Raw != "${prd.API_KEY}" {
		t.Errorf("expected raw reference preserved, got %q", secrets["REF_SECRET"].Raw)
	}
	if secrets["PLAIN"].Raw != "plain-value" {
		t.Errorf("expected plain value preserved, got %q", secrets["PLAIN"].Raw)
	}
}
