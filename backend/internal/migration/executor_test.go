package migration

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/doppler/onprem-migrator/internal/doppler"
)

// mockReadWriteClient implements DopplerReadWriter for executor testing.
type mockReadWriteClient struct {
	// Recorded by the converge half of the interface.
	updatedWorkplaceRoles  []doppler.Role
	updatedProjectRoles    []doppler.Role
	updatedGroups          []string
	replacedMembers        map[string][]doppler.GroupMember
	updatedServiceAccounts []string
	updatedMembers         []string
	updatedWebhooks        []string
	updatedEnvironments    []string
	deletedSecrets         map[string][]string
	deleteSecretsErr       error
	deletedConfigs         []string
	configLogs             map[string][]doppler.ConfigLogEntry
	configLogsErr          error

	mockClient // embeds the read-only mock

	mu sync.Mutex

	// Write operation tracking
	updatedWorkplace      *doppler.Workplace
	workplaceNameIncluded bool
	createdWpRoles        []string
	createdProjRoles      []string
	createdGroups         []string
	addedGroupMembers     []groupMemberCall
	createdSAs            []string
	createdProjects       []projectCreateCall
	getProjectCalls       map[string]*doppler.Project
	getProjectErr         map[string]error
	createdEnvs           []envCreateCall
	getConfigCalls        map[string]*doppler.Config
	getConfigErr          map[string]error
	createdConfigs        []configCreateCall
	updatedSecrets        []secretsUpdateCall
	updatedNotes          []noteUpdateCall
	addedIPs              []ipAddCall
	removedIPs            []ipAddCall
	lockedConfigs         []string
	inheritableCalls      []inheritableCall
	createdWebhooks       []webhookCreateCall
	addedProjMembers      []projMemberCall
	createdPolicies       []doppler.ChangeRequestPolicy
	secretChanges         map[string][]doppler.SecretChangeRequest
	createdIdentities     []identityCreateCall
	envOrders             map[string][]string
	configInherits        map[string][]doppler.ConfigDescriptor
	userAccess            map[string]string
	tokenInfo             *doppler.TokenInfo

	// Error injection
	updateWorkplaceErr error
	createWpRoleErr    map[string]error
	createProjRoleErr  map[string]error
	createGroupErr     map[string]error
	createSAErr        map[string]error
	createProjectErr   map[string]error
	createEnvErr       map[string]error
	createConfigErr    map[string]error
	updateSecretsErr   map[string]error
	addIPErr           map[string]error
	removeIPErr        map[string]error
	createPolicyErr    map[string]error
	createIdentityErr  map[string]error
	envOrderErr        map[string]error
	inheritsErr        map[string]error
	userAccessErr      map[string]error
	tokenInfoErr       error
	lockConfigErr      map[string]error
	inheritableErr     map[string]error
	createWebhookErr   map[string]error
	addGroupMemberErr  map[string]error
	addProjMemberErr   map[string]error
}

type groupMemberCall struct {
	GroupSlug  string
	MemberType string
	MemberSlug string
}

type projectCreateCall struct {
	Name        string
	Description string
}

type envCreateCall struct {
	Project string
	Name    string
	Slug    string
}

type configCreateCall struct {
	Project     string
	Environment string
	Name        string
}

type secretsUpdateCall struct {
	Project string
	Config  string
	Secrets map[string]string
}

type noteUpdateCall struct {
	Project    string
	SecretName string
	Note       string
}

type ipAddCall struct {
	Project string
	Config  string
	IP      string
}

type inheritableCall struct {
	Project     string
	Config      string
	Inheritable bool
}

type webhookCreateCall struct {
	Project string
	Webhook *doppler.Webhook
}

type projMemberCall struct {
	Project      string
	MemberType   string
	MemberSlug   string
	Role         string
	Environments []string
}

func newMockReadWriteClient() *mockReadWriteClient {
	mc := &mockReadWriteClient{
		mockClient:        *newMockClient(),
		getProjectCalls:   make(map[string]*doppler.Project),
		getProjectErr:     make(map[string]error),
		getConfigCalls:    make(map[string]*doppler.Config),
		getConfigErr:      make(map[string]error),
		createWpRoleErr:   make(map[string]error),
		createProjRoleErr: make(map[string]error),
		createGroupErr:    make(map[string]error),
		createSAErr:       make(map[string]error),
		createProjectErr:  make(map[string]error),
		createEnvErr:      make(map[string]error),
		createConfigErr:   make(map[string]error),
		updateSecretsErr:  make(map[string]error),
		addIPErr:          make(map[string]error),
		lockConfigErr:     make(map[string]error),
		inheritableErr:    make(map[string]error),
		createWebhookErr:  make(map[string]error),
		addGroupMemberErr: make(map[string]error),
		addProjMemberErr:  make(map[string]error),
	}
	return mc
}

func (m *mockReadWriteClient) UpdateWorkplace(ctx context.Context, wp *doppler.Workplace, includeName bool) (*doppler.Workplace, error) {
	m.workplaceNameIncluded = includeName
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updatedWorkplace = wp
	return wp, m.updateWorkplaceErr
}

func (m *mockReadWriteClient) CreateWorkplaceRole(ctx context.Context, name string, permissions []string) (*doppler.Role, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.createdWpRoles = append(m.createdWpRoles, name)
	if err, ok := m.createWpRoleErr[name]; ok {
		return nil, err
	}
	return &doppler.Role{Name: name, Permissions: permissions}, nil
}

func (m *mockReadWriteClient) CreateProjectRole(ctx context.Context, name string, permissions []string) (*doppler.Role, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.createdProjRoles = append(m.createdProjRoles, name)
	if err, ok := m.createProjRoleErr[name]; ok {
		return nil, err
	}
	return &doppler.Role{Name: name, Permissions: permissions}, nil
}

func (m *mockReadWriteClient) CreateGroup(ctx context.Context, group *doppler.Group) (*doppler.Group, error) {
	name := group.Name
	m.mu.Lock()
	defer m.mu.Unlock()
	m.createdGroups = append(m.createdGroups, name)
	if err, ok := m.createGroupErr[name]; ok {
		return nil, err
	}
	return &doppler.Group{Name: name, Slug: name}, nil
}

func (m *mockReadWriteClient) AddGroupMember(ctx context.Context, groupSlug, memberType, memberSlug string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.addedGroupMembers = append(m.addedGroupMembers, groupMemberCall{groupSlug, memberType, memberSlug})
	key := groupSlug + "/" + memberSlug
	if err, ok := m.addGroupMemberErr[key]; ok {
		return err
	}
	return nil
}

func (m *mockReadWriteClient) CreateServiceAccount(ctx context.Context, account *doppler.ServiceAccount) (*doppler.ServiceAccount, error) {
	name := account.Name
	m.mu.Lock()
	defer m.mu.Unlock()
	m.createdSAs = append(m.createdSAs, name)
	if err, ok := m.createSAErr[name]; ok {
		return nil, err
	}
	return &doppler.ServiceAccount{Name: name, Slug: name}, nil
}

func (m *mockReadWriteClient) CreateProject(ctx context.Context, name, description string) (*doppler.Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.createdProjects = append(m.createdProjects, projectCreateCall{name, description})
	if err, ok := m.createProjectErr[name]; ok {
		return nil, err
	}
	return &doppler.Project{Name: name, Slug: name, Description: description}, nil
}

func (m *mockReadWriteClient) GetProject(ctx context.Context, slug string) (*doppler.Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err, ok := m.getProjectErr[slug]; ok {
		return nil, err
	}
	if p, ok := m.getProjectCalls[slug]; ok {
		return p, nil
	}
	return nil, fmt.Errorf("project %q not found", slug)
}

func (m *mockReadWriteClient) CreateEnvironment(ctx context.Context, project, name, slug string, personalConfigs bool) (*doppler.Environment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.createdEnvs = append(m.createdEnvs, envCreateCall{project, name, slug})
	key := project + "/" + slug
	if err, ok := m.createEnvErr[key]; ok {
		return nil, err
	}
	return &doppler.Environment{Name: name, Slug: slug, Project: project}, nil
}

func (m *mockReadWriteClient) GetConfig(ctx context.Context, project, config string) (*doppler.Config, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := project + "/" + config
	if err, ok := m.getConfigErr[key]; ok {
		return nil, err
	}
	if c, ok := m.getConfigCalls[key]; ok {
		return c, nil
	}
	return &doppler.Config{Name: config, Project: project}, nil
}

func (m *mockReadWriteClient) CreateConfig(ctx context.Context, project, environment, name string) (*doppler.Config, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.createdConfigs = append(m.createdConfigs, configCreateCall{project, environment, name})
	key := project + "/" + name
	if err, ok := m.createConfigErr[key]; ok {
		return nil, err
	}
	return &doppler.Config{Name: name, Project: project, Environment: environment}, nil
}

func (m *mockReadWriteClient) UpdateSecretNote(ctx context.Context, project, secretName, note string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updatedNotes = append(m.updatedNotes, noteUpdateCall{Project: project, SecretName: secretName, Note: note})
	return nil
}

func (m *mockReadWriteClient) AddTrustedIP(ctx context.Context, project, config, ip string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.addedIPs = append(m.addedIPs, ipAddCall{project, config, ip})
	key := project + "/" + config + "/" + ip
	if err, ok := m.addIPErr[key]; ok {
		return err
	}
	return nil
}

func (m *mockReadWriteClient) LockConfig(ctx context.Context, project, config string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lockedConfigs = append(m.lockedConfigs, project+"/"+config)
	key := project + "/" + config
	if err, ok := m.lockConfigErr[key]; ok {
		return err
	}
	return nil
}

func (m *mockReadWriteClient) SetConfigInheritable(ctx context.Context, project, config string, inheritable bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inheritableCalls = append(m.inheritableCalls, inheritableCall{project, config, inheritable})
	key := project + "/" + config
	if err, ok := m.inheritableErr[key]; ok {
		return err
	}
	return nil
}

func (m *mockReadWriteClient) CreateWebhook(ctx context.Context, project string, webhook *doppler.Webhook) (*doppler.Webhook, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.createdWebhooks = append(m.createdWebhooks, webhookCreateCall{project, webhook})
	key := project + "/" + webhook.URL
	if err, ok := m.createWebhookErr[key]; ok {
		return nil, err
	}
	return webhook, nil
}

func (m *mockReadWriteClient) RemoveTrustedIP(ctx context.Context, project, config, ip string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.removeIPErr[project+"/"+config+"/"+ip]; err != nil {
		return err
	}
	m.removedIPs = append(m.removedIPs, ipAddCall{Project: project, Config: config, IP: ip})
	return nil
}

func (m *mockReadWriteClient) UpdateSecretsWithMetadata(ctx context.Context, project, config string, changes []doppler.SecretChangeRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := project + "/" + config
	if err := m.updateSecretsErr[key]; err != nil {
		return err
	}
	if m.secretChanges == nil {
		m.secretChanges = map[string][]doppler.SecretChangeRequest{}
	}
	m.secretChanges[key] = changes
	// Mirror into updatedSecrets so existing assertions keep working.
	secrets := map[string]string{}
	for _, c := range changes {
		if c.Value != nil {
			secrets[c.Name] = *c.Value
		}
	}
	m.updatedSecrets = append(m.updatedSecrets, secretsUpdateCall{Project: project, Config: config, Secrets: secrets})
	return nil
}

func (m *mockReadWriteClient) SetConfigInherits(ctx context.Context, project, config string, inherits []doppler.ConfigDescriptor) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := project + "/" + config
	if err := m.inheritsErr[key]; err != nil {
		return err
	}
	if m.configInherits == nil {
		m.configInherits = map[string][]doppler.ConfigDescriptor{}
	}
	m.configInherits[key] = inherits
	return nil
}

func (m *mockReadWriteClient) UpdateWorkplaceUserAccess(ctx context.Context, userSlug, access string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.userAccessErr[userSlug]; err != nil {
		return err
	}
	if m.userAccess == nil {
		m.userAccess = map[string]string{}
	}
	m.userAccess[userSlug] = access
	return nil
}

func (m *mockReadWriteClient) GetTokenInfo(ctx context.Context) (*doppler.TokenInfo, error) {
	if m.tokenInfoErr != nil {
		return nil, m.tokenInfoErr
	}
	if m.tokenInfo != nil {
		return m.tokenInfo, nil
	}
	// Default: a service account token, so no workplace user is the migrator.
	return &doppler.TokenInfo{Principal: doppler.TokenPrincipal{Type: "service_account", Slug: "sa-1"}}, nil
}

type identityCreateCall struct {
	serviceAccount string
	identity       doppler.ServiceAccountIdentity
}

func (m *mockReadWriteClient) CreateServiceAccountIdentity(ctx context.Context, serviceAccount string, identity *doppler.ServiceAccountIdentity) (*doppler.ServiceAccountIdentity, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.createIdentityErr[identity.Name]; err != nil {
		return nil, err
	}
	m.createdIdentities = append(m.createdIdentities, identityCreateCall{serviceAccount: serviceAccount, identity: *identity})
	created := *identity
	created.Slug = "target-identity-" + identity.Name
	return &created, nil
}

func (m *mockReadWriteClient) SetEnvironmentOrder(ctx context.Context, project string, order []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.envOrderErr[project]; err != nil {
		return err
	}
	if m.envOrders == nil {
		m.envOrders = map[string][]string{}
	}
	m.envOrders[project] = order
	return nil
}

func (m *mockReadWriteClient) CreateChangeRequestPolicy(ctx context.Context, policy *doppler.ChangeRequestPolicy) (*doppler.ChangeRequestPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.createPolicyErr[policy.Name]; err != nil {
		return nil, err
	}
	m.createdPolicies = append(m.createdPolicies, *policy)
	created := *policy
	created.ID = "target-id-" + policy.Name
	return &created, nil
}

func (m *mockReadWriteClient) AddProjectMember(ctx context.Context, project, memberType, memberSlug, role string, environments []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.addedProjMembers = append(m.addedProjMembers, projMemberCall{project, memberType, memberSlug, role, environments})
	key := project + "/" + memberSlug
	if err, ok := m.addProjMemberErr[key]; ok {
		return err
	}
	return nil
}

// --- Test data helpers ---

func testMigrationData() *MigrationData {
	return &MigrationData{
		Workplace: &doppler.Workplace{ID: "wp1", Name: "Test WP", BillingEmail: "billing@test.com"},
		WorkplaceUsers: []doppler.WorkplaceUser{
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
		},
		WorkplaceRoles: []doppler.Role{
			{Name: "custom-admin", Permissions: []string{"read", "write"}, IsCustomRole: true},
		},
		ProjectRoles: []doppler.Role{
			{Name: "deployer", Permissions: []string{"deploy"}, IsCustomRole: true},
		},
		Groups: []GroupWithMembers{
			{
				Group:   doppler.Group{Name: "Engineering", Slug: "engineering"},
				Members: []doppler.GroupMember{{Type: "workplace_user", Slug: "u1"}},
			},
		},
		ServiceAccounts: []doppler.ServiceAccount{
			{Name: "CI Bot", Slug: "ci-bot"},
		},
		Projects: []ProjectData{
			{
				Project: doppler.Project{Slug: "backend", Name: "Backend", Description: "API"},
				Environments: []doppler.Environment{
					{Slug: "dev", Name: "Development", Project: "backend"},
					{Slug: "prd", Name: "Production", Project: "backend"},
				},
				Configs: []ConfigData{
					{
						Config:  doppler.Config{Name: "dev", Root: true, Environment: "dev", Project: "backend"},
						Secrets: doppler.SecretsMap{"API_KEY": {Raw: "dev-key"}},
					},
					{
						Config:     doppler.Config{Name: "prd", Root: true, Environment: "prd", Project: "backend"},
						Secrets:    doppler.SecretsMap{"API_KEY": {Raw: "${dev.API_KEY}"}, "DB": {Raw: "conn", Note: "production db"}},
						TrustedIPs: []doppler.TrustedIP{{IP: "10.0.0.1/32"}},
					},
					{
						Config:  doppler.Config{Name: "prd_ci", Root: false, Environment: "prd", Project: "backend", Locked: true},
						Secrets: doppler.SecretsMap{"CI_KEY": {Raw: "ci-val"}},
					},
				},
				Webhooks: []doppler.Webhook{{ID: "wh1", URL: "https://hooks.example.com", Enabled: true}},
				Members: []doppler.ProjectMember{
					{Type: "workplace_user", Slug: "u1", Role: doppler.MemberRole{Identifier: "admin"}},
				},
			},
		},
	}
}

func newTestExecutor(data *MigrationData, target *mockReadWriteClient, devMode bool) (*Executor, *CheckpointManager, *[]MigrateProgress) {
	cm := NewCheckpointManager("")
	progress := &[]MigrateProgress{}
	var mu sync.Mutex
	exec := NewExecutor(nil, target, data, cm, devMode, func(p MigrateProgress) {
		mu.Lock()
		*progress = append(*progress, p)
		mu.Unlock()
	})
	return exec, cm, progress
}

// --- Tests ---

func TestExecutorFullHappyPath(t *testing.T) {
	data := testMigrationData()
	target := newMockReadWriteClient()

	// Project doesn't exist on target
	target.getProjectErr["backend"] = fmt.Errorf("not found")

	// Target has the users (different IDs than source to verify slug remapping)
	target.users = []doppler.WorkplaceUser{
		{ID: "target-u1", User: struct {
			Email    string `json:"email"`
			Name     string `json:"name"`
			Username string `json:"username"`
		}{Email: "alice@test.com", Name: "Alice", Username: "alice"}},
	}

	// Target groups and service accounts have different slugs
	target.groups = []doppler.Group{
		{Name: "Engineering", Slug: "target-engineering"},
	}
	target.serviceAccounts = []doppler.ServiceAccount{
		{Name: "CI Bot", Slug: "target-ci-bot"},
	}

	exec, cm, _ := newTestExecutor(data, target, false)

	err := exec.Execute(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify checkpoint completed
	cpData := cm.Data()
	if !cpData.Completed {
		t.Error("expected checkpoint completed")
	}

	// Phase 1: roles
	if len(target.createdWpRoles) != 1 || target.createdWpRoles[0] != "custom-admin" {
		t.Errorf("expected 1 workplace role created, got %v", target.createdWpRoles)
	}
	if len(target.createdProjRoles) != 1 || target.createdProjRoles[0] != "deployer" {
		t.Errorf("expected 1 project role created, got %v", target.createdProjRoles)
	}

	// Phase 2: groups and service accounts
	if len(target.createdGroups) != 1 || target.createdGroups[0] != "Engineering" {
		t.Errorf("expected 1 group, got %v", target.createdGroups)
	}
	if len(target.createdSAs) != 1 || target.createdSAs[0] != "CI Bot" {
		t.Errorf("expected 1 service account, got %v", target.createdSAs)
	}

	// Phase 3: project and environments
	if len(target.createdProjects) != 1 {
		t.Errorf("expected 1 project created, got %d", len(target.createdProjects))
	}
	if len(target.createdEnvs) != 2 {
		t.Errorf("expected 2 environments, got %d", len(target.createdEnvs))
	}

	// Phase 4: configs (1 branch config created, 2 root configs verified)
	if len(target.createdConfigs) != 1 || target.createdConfigs[0].Name != "prd_ci" {
		t.Errorf("expected 1 branch config (prd_ci), got %v", target.createdConfigs)
	}

	// Phase 5: secrets, trusted IPs, locks
	if len(target.updatedSecrets) != 3 { // dev, prd, prd_ci
		t.Errorf("expected 3 secret updates, got %d", len(target.updatedSecrets))
	}
	// Verify raw value preservation
	for _, su := range target.updatedSecrets {
		if su.Config == "prd" {
			if su.Secrets["API_KEY"] != "${dev.API_KEY}" {
				t.Errorf("expected raw reference preserved, got %q", su.Secrets["API_KEY"])
			}
		}
	}
	if len(target.addedIPs) != 1 || target.addedIPs[0].IP != "10.0.0.1/32" {
		t.Errorf("expected 1 trusted IP, got %v", target.addedIPs)
	}
	if len(target.lockedConfigs) != 1 || target.lockedConfigs[0] != "backend/prd_ci" {
		t.Errorf("expected prd_ci locked, got %v", target.lockedConfigs)
	}

	// Phase 5 should have set notes
	foundNote := false
	for _, n := range target.updatedNotes {
		if n.SecretName == "DB" && n.Note == "production db" {
			foundNote = true
		}
	}
	if !foundNote {
		t.Error("expected secret note for DB to be set")
	}

	// Phase 7: webhooks
	if len(target.createdWebhooks) != 1 {
		t.Errorf("expected 1 webhook, got %d", len(target.createdWebhooks))
	}

	// Phase 8: members — verify slug remapping
	if len(target.addedGroupMembers) != 1 {
		t.Fatalf("expected 1 group member, got %d", len(target.addedGroupMembers))
	}
	if target.addedGroupMembers[0].MemberSlug != "target-u1" {
		t.Errorf("expected group member slug remapped to target-u1, got %q", target.addedGroupMembers[0].MemberSlug)
	}
	if target.addedGroupMembers[0].GroupSlug != "target-engineering" {
		t.Errorf("expected group slug remapped to target-engineering, got %q", target.addedGroupMembers[0].GroupSlug)
	}
	if len(target.addedProjMembers) != 1 {
		t.Fatalf("expected 1 project member, got %d", len(target.addedProjMembers))
	}
	if target.addedProjMembers[0].MemberSlug != "target-u1" {
		t.Errorf("expected project member slug remapped to target-u1, got %q", target.addedProjMembers[0].MemberSlug)
	}
}

func TestExecutorIdempotencyResourceExists(t *testing.T) {
	data := testMigrationData()
	target := newMockReadWriteClient()

	// Roles already exist
	target.createWpRoleErr["custom-admin"] = fmt.Errorf("role already exists")
	target.createProjRoleErr["deployer"] = fmt.Errorf("is already taken")

	// Project already exists on target
	target.getProjectCalls["backend"] = &doppler.Project{Slug: "backend", Name: "Backend"}

	// Environments already exist
	target.createEnvErr["backend/dev"] = fmt.Errorf("already exists")
	target.createEnvErr["backend/prd"] = fmt.Errorf("already exists")

	// Target has users (different IDs than source)
	target.users = []doppler.WorkplaceUser{
		{ID: "target-u1", User: struct {
			Email    string `json:"email"`
			Name     string `json:"name"`
			Username string `json:"username"`
		}{Email: "alice@test.com", Username: "alice"}},
	}

	exec, _, _ := newTestExecutor(data, target, false)
	err := exec.Execute(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Project should NOT have been created (GetProject succeeded)
	if len(target.createdProjects) != 0 {
		t.Errorf("expected 0 project creates (already exists), got %d", len(target.createdProjects))
	}
}

func TestExecutorCheckpointResume(t *testing.T) {
	data := testMigrationData()
	target := newMockReadWriteClient()
	target.getProjectErr["backend"] = fmt.Errorf("not found")
	target.users = []doppler.WorkplaceUser{
		{ID: "target-u1", User: struct {
			Email    string `json:"email"`
			Name     string `json:"name"`
			Username string `json:"username"`
		}{Email: "alice@test.com", Username: "alice"}},
	}

	cm := NewCheckpointManager("")

	// Pre-complete phases 1-3
	for _, phase := range []string{
		"workplace_settings_and_roles",
		"groups_and_service_accounts",
		"projects_and_environments",
	} {
		cm.InitPhase(phase)
		cm.CompletePhase(phase)
	}

	var progress []MigrateProgress
	exec := NewExecutor(nil, target, data, cm, false, func(p MigrateProgress) {
		progress = append(progress, p)
	})

	err := exec.Execute(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Phases 1-3 should be skipped
	if len(target.createdWpRoles) != 0 {
		t.Errorf("expected 0 role creates (phase skipped), got %d", len(target.createdWpRoles))
	}
	if len(target.createdGroups) != 0 {
		t.Errorf("expected 0 group creates (phase skipped), got %d", len(target.createdGroups))
	}
	if len(target.createdProjects) != 0 {
		t.Errorf("expected 0 project creates (phase skipped), got %d", len(target.createdProjects))
	}

	// Phase 4+ should have run
	if len(target.updatedSecrets) == 0 {
		t.Error("expected secrets to be updated (phases 4+ ran)")
	}
}

func TestExecutorItemFailureNonFatal(t *testing.T) {
	data := testMigrationData()
	target := newMockReadWriteClient()
	target.updateWorkplaceErr = fmt.Errorf("connection refused")

	// Project doesn't exist on target
	target.getProjectErr["backend"] = fmt.Errorf("not found")

	// Target has users (different IDs than source)
	target.users = []doppler.WorkplaceUser{
		{ID: "target-u1", User: struct {
			Email    string `json:"email"`
			Name     string `json:"name"`
			Username string `json:"username"`
		}{Email: "alice@test.com", Name: "Alice", Username: "alice"}},
	}

	exec, cm, progress := newTestExecutor(data, target, false)

	err := exec.Execute(context.Background())
	// Individual failures should NOT cause Execute to return an error
	if err != nil {
		t.Fatalf("expected no error (individual failures are non-fatal), got: %v", err)
	}

	// Checkpoint should be completed (even though workplace settings failed)
	cpData := cm.Data()
	if !cpData.Completed {
		t.Error("expected checkpoint completed despite individual item failure")
	}

	// Workplace settings should be marked failed in checkpoint
	status := cm.GetItemStatus("workplace_settings_and_roles", "workplace_settings")
	if status != StatusFailed {
		t.Errorf("expected workplace_settings to be failed, got %s", status)
	}

	// Phase 1 should have completed_with_errors status in progress
	foundCompletedWithErrors := false
	for _, p := range *progress {
		if p.Name == "workplace_settings_and_roles" && p.Status == "completed_with_errors" && p.Resource == "" {
			foundCompletedWithErrors = true
			break
		}
	}
	if !foundCompletedWithErrors {
		t.Error("expected phase 1 to have completed_with_errors status")
	}

	// Phase 1 should NOT be marked complete (it has failures) — enables retry
	if cm.IsPhaseComplete("workplace_settings_and_roles") {
		t.Error("expected phase 1 NOT to be marked complete (has item failures)")
	}

	// Other phases should still have run and completed
	if !cm.IsPhaseComplete("groups_and_service_accounts") {
		t.Error("expected phase 2 to complete despite phase 1 having item failures")
	}
	if !cm.IsPhaseComplete("configs") {
		t.Error("expected phase 4 to complete")
	}

	// Checkpoint error should mention phases with errors
	if cpData.Error == "" {
		t.Error("expected checkpoint error to mention phases with failures")
	}
}

func TestExecutorPauseResume(t *testing.T) {
	data := &MigrationData{
		Workplace: &doppler.Workplace{ID: "wp1", Name: "Test"},
		WorkplaceRoles: []doppler.Role{
			{Name: "role1", IsCustomRole: true},
			{Name: "role2", IsCustomRole: true},
		},
	}
	target := newMockReadWriteClient()
	exec, _, _ := newTestExecutor(data, target, false)

	// Pause before starting
	exec.Pause()
	if !exec.IsPaused() {
		t.Error("expected paused")
	}

	done := make(chan error, 1)
	go func() {
		done <- exec.Execute(context.Background())
	}()

	// Resume after a moment
	exec.Resume()
	if exec.IsPaused() {
		t.Error("expected not paused after resume")
	}

	err := <-done
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExecutorDevModeSkipsWrites(t *testing.T) {
	data := testMigrationData()
	target := newMockReadWriteClient()

	exec, _, _ := newTestExecutor(data, target, true)

	err := exec.Execute(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// No writes should have been made
	if target.updatedWorkplace != nil {
		t.Error("expected no workplace update in dev mode")
	}
	if len(target.createdWpRoles) != 0 {
		t.Errorf("expected 0 role creates in dev mode, got %d", len(target.createdWpRoles))
	}
	if len(target.createdProjects) != 0 {
		t.Errorf("expected 0 project creates in dev mode, got %d", len(target.createdProjects))
	}
	if len(target.updatedSecrets) != 0 {
		t.Errorf("expected 0 secret updates in dev mode, got %d", len(target.updatedSecrets))
	}
	if len(target.lockedConfigs) != 0 {
		t.Errorf("expected 0 locked configs in dev mode, got %d", len(target.lockedConfigs))
	}
}

func TestExecutorSecretsClearedFromMemory(t *testing.T) {
	data := testMigrationData()
	target := newMockReadWriteClient()
	target.getProjectErr["backend"] = fmt.Errorf("not found")
	target.users = []doppler.WorkplaceUser{
		{ID: "target-u1", User: struct {
			Email    string `json:"email"`
			Name     string `json:"name"`
			Username string `json:"username"`
		}{Email: "alice@test.com", Username: "alice"}},
	}

	exec, _, _ := newTestExecutor(data, target, false)

	err := exec.Execute(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// After migration, secrets should be cleared
	for _, pd := range data.Projects {
		for _, cd := range pd.Configs {
			if cd.Secrets != nil {
				t.Errorf("expected secrets cleared for %s/%s, still have %d",
					pd.Project.Slug, cd.Config.Name, len(cd.Secrets))
			}
		}
	}
}

func TestExecutorLockedConfigs(t *testing.T) {
	data := &MigrationData{
		Workplace: &doppler.Workplace{ID: "wp1", Name: "Test"},
		Projects: []ProjectData{
			{
				Project: doppler.Project{Slug: "app", Name: "App"},
				Configs: []ConfigData{
					{
						Config:  doppler.Config{Name: "dev", Root: true, Environment: "dev", Project: "app"},
						Secrets: doppler.SecretsMap{"K": {Raw: "v"}},
					},
					{
						Config:  doppler.Config{Name: "prd", Root: false, Environment: "prd", Project: "app", Locked: true},
						Secrets: doppler.SecretsMap{"K": {Raw: "v2"}},
					},
				},
			},
		},
	}
	target := newMockReadWriteClient()

	exec, _, _ := newTestExecutor(data, target, false)
	err := exec.Execute(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Secrets should be updated BEFORE locking
	if len(target.updatedSecrets) != 2 {
		t.Fatalf("expected 2 secret updates, got %d", len(target.updatedSecrets))
	}
	if len(target.lockedConfigs) != 1 || target.lockedConfigs[0] != "app/prd" {
		t.Errorf("expected prd locked, got %v", target.lockedConfigs)
	}
}

func TestExecutorConfigInheritance(t *testing.T) {
	data := &MigrationData{
		Workplace: &doppler.Workplace{ID: "wp1", Name: "Test"},
		Projects: []ProjectData{
			{
				Project: doppler.Project{Slug: "app", Name: "App"},
				Configs: []ConfigData{
					{Config: doppler.Config{Name: "dev", Root: true, Environment: "dev", Project: "app", Inheritable: true}},
					{Config: doppler.Config{Name: "stg", Root: true, Environment: "stg", Project: "app"}},
				},
			},
		},
	}
	target := newMockReadWriteClient()
	exec, _, _ := newTestExecutor(data, target, false)

	err := exec.Execute(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(target.inheritableCalls) != 1 {
		t.Fatalf("expected 1 inheritable call, got %d", len(target.inheritableCalls))
	}
	if target.inheritableCalls[0].Config != "dev" || !target.inheritableCalls[0].Inheritable {
		t.Errorf("unexpected inheritable call: %+v", target.inheritableCalls[0])
	}
}

func TestExecutorMissingUsersSkipped(t *testing.T) {
	data := &MigrationData{
		Workplace: &doppler.Workplace{ID: "wp1", Name: "Test"},
		WorkplaceUsers: []doppler.WorkplaceUser{
			{ID: "u1", User: struct {
				Email    string `json:"email"`
				Name     string `json:"name"`
				Username string `json:"username"`
			}{Email: "alice@test.com", Username: "alice"}},
			{ID: "u2", User: struct {
				Email    string `json:"email"`
				Name     string `json:"name"`
				Username string `json:"username"`
			}{Email: "bob@test.com", Username: "bob"}},
		},
		Groups: []GroupWithMembers{
			{
				Group: doppler.Group{Name: "Team", Slug: "team"},
				Members: []doppler.GroupMember{
					{Type: "workplace_user", Slug: "u1"},
					{Type: "workplace_user", Slug: "u2"},
				},
			},
		},
		Projects: []ProjectData{
			{
				Project: doppler.Project{Slug: "app", Name: "App"},
				Members: []doppler.ProjectMember{
					{Type: "workplace_user", Slug: "u2", Role: doppler.MemberRole{Identifier: "admin"}},
				},
			},
		},
	}
	target := newMockReadWriteClient()
	// Only alice exists on target (with different ID), bob is missing
	target.users = []doppler.WorkplaceUser{
		{ID: "target-u1", User: struct {
			Email    string `json:"email"`
			Name     string `json:"name"`
			Username string `json:"username"`
		}{Email: "alice@test.com", Username: "alice"}},
	}

	exec, cm, _ := newTestExecutor(data, target, false)

	err := exec.Execute(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// alice (u1) should be added with remapped target slug, bob (u2) should be skipped
	if len(target.addedGroupMembers) != 1 {
		t.Fatalf("expected only 1 group member added, got %d", len(target.addedGroupMembers))
	}
	if target.addedGroupMembers[0].MemberSlug != "target-u1" {
		t.Errorf("expected group member slug remapped to target-u1, got %q", target.addedGroupMembers[0].MemberSlug)
	}
	if len(target.addedProjMembers) != 0 {
		t.Errorf("expected u2 skipped for project member, got %v", target.addedProjMembers)
	}

	// Verify bob (u2) was marked as skipped in checkpoint
	status := cm.GetItemStatus("members", "group_member:team/u2")
	if status != StatusSkipped {
		t.Errorf("expected u2 skipped in checkpoint, got %s", status)
	}
	status = cm.GetItemStatus("members", "project_member:app/u2")
	if status != StatusSkipped {
		t.Errorf("expected u2 skipped for project, got %s", status)
	}
}

func TestExecutorContextCancellation(t *testing.T) {
	data := testMigrationData()
	target := newMockReadWriteClient()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	exec, _, _ := newTestExecutor(data, target, false)

	err := exec.Execute(ctx)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestExecutorProgressCallbacks(t *testing.T) {
	data := &MigrationData{
		Workplace:      &doppler.Workplace{ID: "wp1", Name: "Test"},
		WorkplaceRoles: []doppler.Role{{Name: "custom", IsCustomRole: true}},
	}
	target := newMockReadWriteClient()

	var progress []MigrateProgress
	cm := NewCheckpointManager("")
	exec := NewExecutor(nil, target, data, cm, false, func(p MigrateProgress) {
		progress = append(progress, p)
	})

	err := exec.Execute(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(progress) == 0 {
		t.Fatal("expected progress callbacks")
	}

	// Should have phase start/complete for each phase
	phaseStarts := 0
	phaseCompletes := 0
	for _, p := range progress {
		if p.Status == "started" && p.Resource == "" {
			phaseStarts++
		}
		// A phase with nothing to do finishes as skipped rather than completed.
		if (p.Status == "completed" || p.Status == "skipped") && p.Resource == "" {
			phaseCompletes++
		}
	}
	if phaseStarts != 10 {
		t.Errorf("expected 10 phase starts, got %d", phaseStarts)
	}
	if phaseCompletes != 10 {
		t.Errorf("expected 10 phase ends, got %d", phaseCompletes)
	}
}

func TestExecutorMarksPhasesWithNothingToDoAsSkipped(t *testing.T) {
	// A partial run of one project: no roles, and the workplace layer deferred.
	data := &MigrationData{
		Workplace:           &doppler.Workplace{ID: "wp1", Name: "Src"},
		DeferWorkplaceLevel: true,
		Projects: []ProjectData{{
			Project:      doppler.Project{Slug: "backend", Name: "backend"},
			Environments: []doppler.Environment{{Slug: "dev", Name: "Development"}},
			Configs:      []ConfigData{{Config: doppler.Config{Name: "dev", Environment: "dev", Root: true}}},
		}},
	}
	exec, _, _ := newTestExecutor(data, newMockReadWriteClient(), false)
	if err := exec.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}

	states := exec.PhaseStates()
	want := map[string]PhaseState{
		"workplace_settings_and_roles": {Status: "skipped", Reason: "Left for the full migration"},
		"groups_and_service_accounts":  {Status: "skipped", Reason: "Nothing to migrate"},
		"projects_and_environments":    {Status: "completed"},
		"configs":                      {Status: "completed"},
		"webhooks":                     {Status: "skipped", Reason: "Nothing to migrate"},
		"change_request_policies":      {Status: "skipped", Reason: "Left for the full migration"},
	}
	for name, w := range want {
		if states[name] != w {
			t.Errorf("%s = %+v, want %+v", name, states[name], w)
		}
	}
	if len(states) != 10 {
		t.Errorf("every phase should have a state, got %d", len(states))
	}
}

func TestExecutorReportsPhasesFinishedInAnEarlierRun(t *testing.T) {
	// A resumed run used to say nothing about the phases it skipped, which
	// left them reading as not yet run.
	data := &MigrationData{Workplace: &doppler.Workplace{ID: "wp1", Name: "Src"}}
	exec, cm, progress := newTestExecutor(data, newMockReadWriteClient(), false)
	cm.InitPhase("workplace_settings_and_roles")
	cm.CompletePhase("workplace_settings_and_roles")

	if err := exec.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, p := range *progress {
		if p.Name == "workplace_settings_and_roles" && p.Resource == "" {
			if p.Status != "completed" {
				t.Errorf("got %q, want completed", p.Status)
			}
			return
		}
	}
	t.Fatal("the finished phase was not reported")
}

func TestExecutorEmptyData(t *testing.T) {
	data := &MigrationData{
		Workplace: &doppler.Workplace{ID: "wp1", Name: "Empty"},
	}
	target := newMockReadWriteClient()

	exec, cm, _ := newTestExecutor(data, target, false)

	err := exec.Execute(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cpData := cm.Data()
	if !cpData.Completed {
		t.Error("expected completed even with empty data")
	}
}

func TestExecutorWebhookIdempotencyByURL(t *testing.T) {
	data := &MigrationData{
		Workplace: &doppler.Workplace{ID: "wp1", Name: "Test"},
		Projects: []ProjectData{
			{
				Project: doppler.Project{Slug: "app", Name: "App"},
				Webhooks: []doppler.Webhook{
					{URL: "https://existing.example.com", Enabled: true},
					{URL: "https://new.example.com", Enabled: true},
				},
			},
		},
	}
	target := newMockReadWriteClient()
	// Existing webhook on target
	target.webhooks["app"] = []doppler.Webhook{
		{URL: "https://existing.example.com", Enabled: true},
	}

	exec, _, _ := newTestExecutor(data, target, false)

	err := exec.Execute(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Only the new webhook should be created
	if len(target.createdWebhooks) != 1 {
		t.Fatalf("expected 1 webhook created, got %d", len(target.createdWebhooks))
	}
	if target.createdWebhooks[0].Webhook.URL != "https://new.example.com" {
		t.Errorf("expected new webhook, got %s", target.createdWebhooks[0].Webhook.URL)
	}
}

func TestIsAlreadyExistsError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil error", nil, false},
		{"generic error", fmt.Errorf("something went wrong"), false},
		{"already exists string", fmt.Errorf("role already exists"), true},
		{"is already taken", fmt.Errorf("name is already taken"), true},
		{"has already been taken (Rails)", fmt.Errorf("name has already been taken"), true},
		{"APIError 409", &doppler.APIError{StatusCode: 409, Message: "conflict"}, true},
		{"APIError 409 no message", &doppler.APIError{StatusCode: 409, Body: "conflict"}, true},
		{"APIError 400", &doppler.APIError{StatusCode: 400, Message: "bad request"}, false},
		{"APIError 422 with already taken message", &doppler.APIError{StatusCode: 422, Message: "has already been taken"}, true},
		{"wrapped APIError 409", fmt.Errorf("create config: %w", &doppler.APIError{StatusCode: 409, Message: "exists"}), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isAlreadyExistsError(tt.err)
			if got != tt.expected {
				t.Errorf("isAlreadyExistsError(%v) = %v, want %v", tt.err, got, tt.expected)
			}
		})
	}
}

func TestExecutorMultipleItemFailuresContinues(t *testing.T) {
	data := &MigrationData{
		Workplace: &doppler.Workplace{ID: "wp1", Name: "Test"},
		WorkplaceRoles: []doppler.Role{
			{Name: "role-fail", IsCustomRole: true},
			{Name: "role-ok", IsCustomRole: true},
			{Name: "role-also-fail", IsCustomRole: true},
		},
	}
	target := newMockReadWriteClient()
	target.createWpRoleErr["role-fail"] = fmt.Errorf("permission denied")
	target.createWpRoleErr["role-also-fail"] = fmt.Errorf("server error")

	exec, cm, progress := newTestExecutor(data, target, false)

	err := exec.Execute(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// Verify all three were attempted
	if len(target.createdWpRoles) != 3 {
		t.Errorf("expected 3 role creation attempts, got %d", len(target.createdWpRoles))
	}

	// role-ok should be completed, others failed
	if cm.GetItemStatus("workplace_settings_and_roles", "workplace_role:role-fail") != StatusFailed {
		t.Error("expected role-fail to be StatusFailed")
	}
	if cm.GetItemStatus("workplace_settings_and_roles", "workplace_role:role-ok") != StatusCompleted {
		t.Error("expected role-ok to be StatusCompleted")
	}
	if cm.GetItemStatus("workplace_settings_and_roles", "workplace_role:role-also-fail") != StatusFailed {
		t.Error("expected role-also-fail to be StatusFailed")
	}

	// Phase should have completed_with_errors
	foundCWE := false
	for _, p := range *progress {
		if p.Name == "workplace_settings_and_roles" && p.Status == "completed_with_errors" && p.Resource == "" {
			foundCWE = true
		}
	}
	if !foundCWE {
		t.Error("expected completed_with_errors phase status")
	}

	// Migration should still complete overall
	cpData := cm.Data()
	if !cpData.Completed {
		t.Error("expected migration to complete despite individual failures")
	}
}

func TestExecutorSecretsFailSkipsLock(t *testing.T) {
	data := &MigrationData{
		Workplace: &doppler.Workplace{ID: "wp1", Name: "Test"},
		Projects: []ProjectData{
			{
				Project: doppler.Project{Slug: "app", Name: "App"},
				Configs: []ConfigData{
					{
						Config:  doppler.Config{Name: "prd", Root: false, Environment: "prd", Project: "app", Locked: true},
						Secrets: doppler.SecretsMap{"K": {Raw: "v"}},
					},
				},
			},
		},
	}
	target := newMockReadWriteClient()
	target.updateSecretsErr["app/prd"] = fmt.Errorf("write failed")

	exec, cm, _ := newTestExecutor(data, target, false)

	err := exec.Execute(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// Secrets should be marked failed
	secretsStatus := cm.GetItemStatus("secrets_and_locks", "secrets:app/prd")
	if secretsStatus != StatusFailed {
		t.Errorf("expected secrets to be failed, got %s", secretsStatus)
	}

	// Lock should be marked failed (skipped due to secrets failure)
	lockStatus := cm.GetItemStatus("secrets_and_locks", "lock:app/prd")
	if lockStatus != StatusFailed {
		t.Errorf("expected lock to be failed (skipped), got %s", lockStatus)
	}

	// Config should NOT have been locked
	if len(target.lockedConfigs) != 0 {
		t.Errorf("expected 0 lock calls when secrets failed, got %d", len(target.lockedConfigs))
	}

	// Secrets should still be cleared from memory
	for _, pd := range data.Projects {
		for _, cd := range pd.Configs {
			if cd.Secrets != nil {
				t.Error("expected secrets cleared from memory even on failure")
			}
		}
	}
}

func TestExecutorContextCancellationStillFatal(t *testing.T) {
	// Context cancellation should still stop the migration immediately
	data := testMigrationData()
	target := newMockReadWriteClient()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	exec, _, _ := newTestExecutor(data, target, false)

	err := exec.Execute(ctx)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestExecutorAdminAccessSkipped(t *testing.T) {
	data := &MigrationData{
		Workplace: &doppler.Workplace{ID: "wp1", Name: "Test"},
		WorkplaceUsers: []doppler.WorkplaceUser{
			{ID: "u1", User: struct {
				Email    string `json:"email"`
				Name     string `json:"name"`
				Username string `json:"username"`
			}{Email: "admin@test.com", Username: "admin"}},
			{ID: "u2", User: struct {
				Email    string `json:"email"`
				Name     string `json:"name"`
				Username string `json:"username"`
			}{Email: "dev@test.com", Username: "dev"}},
		},
		Projects: []ProjectData{
			{
				Project: doppler.Project{Slug: "app", Name: "App"},
				Members: []doppler.ProjectMember{
					{Type: "workplace_user", Slug: "u1", Role: doppler.MemberRole{Identifier: "admin"}},
					{Type: "workplace_user", Slug: "u2", Role: doppler.MemberRole{Identifier: "viewer"}},
				},
			},
		},
	}
	target := newMockReadWriteClient()
	target.users = []doppler.WorkplaceUser{
		{ID: "target-u1", User: struct {
			Email    string `json:"email"`
			Name     string `json:"name"`
			Username string `json:"username"`
		}{Email: "admin@test.com", Username: "admin"}},
		{ID: "target-u2", User: struct {
			Email    string `json:"email"`
			Name     string `json:"name"`
			Username string `json:"username"`
		}{Email: "dev@test.com", Username: "dev"}},
	}
	// Admin user triggers "admin access to all projects" error
	target.addProjMemberErr["app/target-u1"] = fmt.Errorf("doppler API error (status 400): Cannot set project role for user with admin access to all projects.")

	exec, cm, _ := newTestExecutor(data, target, false)

	err := exec.Execute(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Admin user should be skipped, not failed
	status := cm.GetItemStatus("members", "project_member:app/u1")
	if status != StatusSkipped {
		t.Errorf("expected admin user to be skipped, got %s", status)
	}

	// Normal user should be completed
	status = cm.GetItemStatus("members", "project_member:app/u2")
	if status != StatusCompleted {
		t.Errorf("expected normal user to be completed, got %s", status)
	}

	// Both should have been attempted
	if len(target.addedProjMembers) != 2 {
		t.Errorf("expected 2 project member attempts, got %d", len(target.addedProjMembers))
	}
}

func TestIsAdminAccessError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil error", nil, false},
		{"generic error", fmt.Errorf("something went wrong"), false},
		{"admin access error", fmt.Errorf("Cannot set project role for user with admin access to all projects."), true},
		{"admin access with API prefix", fmt.Errorf("doppler API error (status 400): Cannot set project role for user with admin access to all projects."), true},
		{"partial match", fmt.Errorf("admin access to all projects"), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isAdminAccessError(tt.err)
			if got != tt.expected {
				t.Errorf("isAdminAccessError(%v) = %v, want %v", tt.err, got, tt.expected)
			}
		})
	}
}

// contains is a helper to check if a string contains a substring.
func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsStr(s, sub))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// The converge half of DopplerReadWriter. The executor never calls these — it
// only ever creates — so the mock records them for the tests that do and
// otherwise stays out of the way.
func (m *mockReadWriteClient) UpdateWorkplaceRole(_ context.Context, identifier, name string, permissions []string) (*doppler.Role, error) {
	m.updatedWorkplaceRoles = append(m.updatedWorkplaceRoles, doppler.Role{Identifier: identifier, Name: name, Permissions: permissions})
	return &doppler.Role{Identifier: identifier, Name: name, Permissions: permissions}, nil
}

func (m *mockReadWriteClient) UpdateProjectRole(_ context.Context, identifier, name string, permissions []string) (*doppler.Role, error) {
	m.updatedProjectRoles = append(m.updatedProjectRoles, doppler.Role{Identifier: identifier, Name: name, Permissions: permissions})
	return &doppler.Role{Identifier: identifier, Name: name, Permissions: permissions}, nil
}

func (m *mockReadWriteClient) UpdateGroup(_ context.Context, slug string, group *doppler.Group) (*doppler.Group, error) {
	m.updatedGroups = append(m.updatedGroups, slug)
	return group, nil
}

func (m *mockReadWriteClient) ReplaceGroupMembers(_ context.Context, slug string, members []doppler.GroupMember) error {
	if m.replacedMembers == nil {
		m.replacedMembers = map[string][]doppler.GroupMember{}
	}
	m.replacedMembers[slug] = members
	return nil
}

func (m *mockReadWriteClient) UpdateServiceAccount(_ context.Context, slug string, account *doppler.ServiceAccount) (*doppler.ServiceAccount, error) {
	m.updatedServiceAccounts = append(m.updatedServiceAccounts, slug)
	return account, nil
}

func (m *mockReadWriteClient) UpdateProjectMember(_ context.Context, project, memberType, memberSlug, role string, _ []string) error {
	m.updatedMembers = append(m.updatedMembers, project+"/"+memberType+":"+memberSlug+"="+role)
	return nil
}

func (m *mockReadWriteClient) UpdateWebhook(_ context.Context, project, slug string, webhook *doppler.Webhook) (*doppler.Webhook, error) {
	m.updatedWebhooks = append(m.updatedWebhooks, project+"/"+slug)
	return webhook, nil
}

func (m *mockReadWriteClient) UpdateEnvironment(_ context.Context, project, slug, name string) (*doppler.Environment, error) {
	m.updatedEnvironments = append(m.updatedEnvironments, project+"/"+slug+"="+name)
	return &doppler.Environment{Slug: slug, Name: name}, nil
}

func (m *mockReadWriteClient) DeleteSecrets(_ context.Context, project, config string, names []string) error {
	if m.deletedSecrets == nil {
		m.deletedSecrets = map[string][]string{}
	}
	m.deletedSecrets[project+"/"+config] = append(m.deletedSecrets[project+"/"+config], names...)
	return m.deleteSecretsErr
}

func (m *mockReadWriteClient) DeleteConfig(_ context.Context, project, config string) error {
	m.deletedConfigs = append(m.deletedConfigs, project+"/"+config)
	return nil
}

func (m *mockReadWriteClient) ListConfigLogsSince(_ context.Context, project, config string, _ time.Time) ([]doppler.ConfigLogEntry, error) {
	return m.configLogs[project+"/"+config], m.configLogsErr
}
