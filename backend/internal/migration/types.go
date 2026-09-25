package migration

import (
	"context"
	"time"

	"github.com/doppler/onprem-migrator/internal/doppler"
)

// MigrationConfig holds the configuration for a migration session.
// Stored in-memory only — tokens never touch disk.
type MigrationConfig struct {
	SourceHost  string `json:"source_host"`
	SourceToken string `json:"source_token"`
	TargetHost  string `json:"target_host"`
	TargetToken string `json:"target_token"`
	DevMode     bool   `json:"dev_mode"`
	// MigrateWorkplaceName renames the target workplace to match the source.
	// Off by default: the target's name is usually chosen deliberately, and
	// overwriting it is not something to do without being asked.
	MigrateWorkplaceName bool `json:"migrate_workplace_name"`
	// StopOnNewErrorClass pauses the run the first time a kind of failure
	// appears. On by default: without it a bad token burns through the whole
	// workplace before anyone looks.
	StopOnNewErrorClass bool `json:"stop_on_new_error_class"`
}

// DopplerClient defines the interface for Doppler API operations.
// Allows dependency injection for testing.
type DopplerClient interface {
	GetWorkplace(ctx context.Context) (*doppler.Workplace, error)
	ListWorkplaceUsers(ctx context.Context) ([]doppler.WorkplaceUser, error)
	ListWorkplaceRoles(ctx context.Context) ([]doppler.Role, error)
	ListProjectRoles(ctx context.Context) ([]doppler.Role, error)
	ListGroups(ctx context.Context) ([]doppler.Group, error)
	GetGroupMembers(ctx context.Context, groupSlug string) ([]doppler.GroupMember, error)
	ListServiceAccounts(ctx context.Context) ([]doppler.ServiceAccount, error)
	ListIntegrations(ctx context.Context) ([]doppler.Integration, error)
	ListProjects(ctx context.Context) ([]doppler.Project, error)
	ListEnvironments(ctx context.Context, project string) ([]doppler.Environment, error)
	ListConfigs(ctx context.Context, project string) ([]doppler.Config, error)
	ListSecrets(ctx context.Context, project, config string) (doppler.SecretsMap, error)
	ListTrustedIPs(ctx context.Context, project, config string) ([]doppler.TrustedIP, error)
	ListServiceTokens(ctx context.Context, project, config string) ([]doppler.ServiceToken, error)
	ListWebhooks(ctx context.Context, project string) ([]doppler.Webhook, error)
	ListProjectMembers(ctx context.Context, project string) ([]doppler.ProjectMember, error)
	ListChangeRequestPolicies(ctx context.Context) ([]doppler.ChangeRequestPolicy, error)
	ListServiceAccountTokens(ctx context.Context, serviceAccount string) ([]doppler.ServiceAccountToken, error)
	ListServiceAccountIdentities(ctx context.Context, serviceAccount string) ([]doppler.ServiceAccountIdentity, error)
	ListRotatedSecrets(ctx context.Context, project, config string) ([]doppler.RotatedSecret, error)
}

// MigrationData holds all gathered data from the source instance.
type MigrationData struct {
	Workplace       *doppler.Workplace
	WorkplaceUsers  []doppler.WorkplaceUser
	WorkplaceRoles  []doppler.Role // custom only
	ProjectRoles    []doppler.Role // custom only
	Groups          []GroupWithMembers
	ServiceAccounts []doppler.ServiceAccount
	Projects        []ProjectData
	Integrations    []doppler.Integration  // for manual steps report
	ServiceTokens   []doppler.ServiceToken // for report (all service tokens across configs)

	ChangeRequestPolicies []doppler.ChangeRequestPolicy

	// ServiceAccountTokens and RotatedSecrets are report-only: their values and
	// credentials cannot be read back from the API.
	ServiceAccountTokens     []doppler.ServiceAccountToken
	RotatedSecrets           []doppler.RotatedSecret
	ServiceAccountIdentities []doppler.ServiceAccountIdentity

	// PersonalConfigs are configs found on the source that were deliberately not
	// gathered — they belong to individual users and cannot be migrated. Recorded
	// so the plan, the manual steps and the final report can all say so.
	PersonalConfigs []doppler.ConfigDescriptor

	// Unavailable are the kinds of thing the source token was refused, and so
	// the migration leaves out. See UnavailableResource.
	Unavailable []UnavailableResource

	// ExcludedServiceAccount is the source token's own service account, left
	// out of the migration. See ExcludeServiceAccount.
	ExcludedServiceAccount *doppler.ServiceAccount

	// DeferWorkplaceLevel is set on data narrowed for a partial run. The
	// workplace layer — settings, change-request policies, each user's
	// workplace access — changes the whole target rather than the projects
	// being tried, so it waits for the full run.
	DeferWorkplaceLevel bool
}

// GroupWithMembers bundles a group with its members.
type GroupWithMembers struct {
	Group   doppler.Group
	Members []doppler.GroupMember
}

// ProjectData bundles a project with all its sub-resources.
type ProjectData struct {
	Project      doppler.Project
	Environments []doppler.Environment
	Configs      []ConfigData
	Webhooks     []doppler.Webhook
	Members      []doppler.ProjectMember
}

// ConfigData bundles a config with its secrets and trusted IPs.
type ConfigData struct {
	Config  doppler.Config
	Secrets doppler.SecretsMap // held temporarily, cleared after migration
	// SecretCount is how many secrets were gathered, kept after Secrets is cleared
	// so a retry can tell "this config had none" from "these were already wiped".
	SecretCount int
	TrustedIPs  []doppler.TrustedIP
}

// DopplerWriter defines the interface for write operations on the target instance.
type DopplerWriter interface {
	UpdateWorkplace(ctx context.Context, wp *doppler.Workplace, includeName bool) (*doppler.Workplace, error)
	CreateWorkplaceRole(ctx context.Context, name string, permissions []string) (*doppler.Role, error)
	CreateProjectRole(ctx context.Context, name string, permissions []string) (*doppler.Role, error)
	CreateGroup(ctx context.Context, group *doppler.Group) (*doppler.Group, error)
	AddGroupMember(ctx context.Context, groupSlug string, memberType, memberSlug string) error
	CreateServiceAccount(ctx context.Context, account *doppler.ServiceAccount) (*doppler.ServiceAccount, error)
	CreateProject(ctx context.Context, name, description string) (*doppler.Project, error)
	GetProject(ctx context.Context, slug string) (*doppler.Project, error)
	CreateEnvironment(ctx context.Context, project, name, slug string, personalConfigs bool) (*doppler.Environment, error)
	GetConfig(ctx context.Context, project, config string) (*doppler.Config, error)
	CreateConfig(ctx context.Context, project, environment, name string) (*doppler.Config, error)
	UpdateSecretsWithMetadata(ctx context.Context, project, config string, changes []doppler.SecretChangeRequest) error
	UpdateSecretNote(ctx context.Context, project, secretName, note string) error
	AddTrustedIP(ctx context.Context, project, config, ip string) error
	RemoveTrustedIP(ctx context.Context, project, config, ip string) error
	LockConfig(ctx context.Context, project, config string) error
	SetConfigInheritable(ctx context.Context, project, config string, inheritable bool) error
	CreateWebhook(ctx context.Context, project string, webhook *doppler.Webhook) (*doppler.Webhook, error)
	AddProjectMember(ctx context.Context, project string, memberType, memberSlug, role string, environments []string) error
	CreateChangeRequestPolicy(ctx context.Context, policy *doppler.ChangeRequestPolicy) (*doppler.ChangeRequestPolicy, error)
	CreateServiceAccountIdentity(ctx context.Context, serviceAccount string, identity *doppler.ServiceAccountIdentity) (*doppler.ServiceAccountIdentity, error)
	SetEnvironmentOrder(ctx context.Context, project string, order []string) error
	SetConfigInherits(ctx context.Context, project, config string, inherits []doppler.ConfigDescriptor) error
	UpdateWorkplaceUserAccess(ctx context.Context, userSlug, access string) error
	GetTokenInfo(ctx context.Context) (*doppler.TokenInfo, error)

	// The update and delete half. A migration that only ever creates can carry a
	// workplace across once; keeping one in step needs these.
	UpdateWorkplaceRole(ctx context.Context, identifier, name string, permissions []string) (*doppler.Role, error)
	UpdateProjectRole(ctx context.Context, identifier, name string, permissions []string) (*doppler.Role, error)
	UpdateGroup(ctx context.Context, slug string, group *doppler.Group) (*doppler.Group, error)
	ReplaceGroupMembers(ctx context.Context, slug string, members []doppler.GroupMember) error
	UpdateServiceAccount(ctx context.Context, slug string, account *doppler.ServiceAccount) (*doppler.ServiceAccount, error)
	UpdateProjectMember(ctx context.Context, project, memberType, memberSlug, role string, environments []string) error
	UpdateWebhook(ctx context.Context, project, slug string, webhook *doppler.Webhook) (*doppler.Webhook, error)
	UpdateEnvironment(ctx context.Context, project, slug, name string) (*doppler.Environment, error)
	DeleteSecrets(ctx context.Context, project, config string, names []string) error
	DeleteConfig(ctx context.Context, project, config string) error

	// ListConfigLogsSince is how drift is detected: the instance's own record of
	// who changed what, rather than a record this tool keeps.
	ListConfigLogsSince(ctx context.Context, project, config string, since time.Time) ([]doppler.ConfigLogEntry, error)
}

// DopplerReadWriter combines read and write interfaces.
// The executor uses this to both read from the target (for idempotency checks)
// and write to it.
type DopplerReadWriter interface {
	DopplerClient
	DopplerWriter
}

// Verify at compile time that *doppler.Client satisfies DopplerReadWriter.
var _ DopplerReadWriter = (*doppler.Client)(nil)

// MigrateProgress represents a progress update during migration execution.
type MigrateProgress struct {
	Phase    int    `json:"phase"`
	Name     string `json:"name"`
	Resource string `json:"resource"`
	Item     string `json:"item"`
	Status   string `json:"status"` // "started", "completed", "skipped", "failed"
	Current  int    `json:"current"`
	Total    int    `json:"total"`
	Error    string `json:"error,omitempty"`
	// Reason says why a phase was skipped.
	Reason string `json:"reason,omitempty"`

	// ErrorClass and ErrorID are filled in by the executor when a failure is
	// filed, so a live subscriber can nest the failure under its phase without
	// re-fetching the whole error list.
	ErrorClass ErrorClassKey `json:"error_class,omitempty"`
	ErrorID    string        `json:"error_id,omitempty"`

	// err carries the failure itself so the error store can take its evidence —
	// the request, the token, the response. Unexported, so it never serializes:
	// a response body can hold anything, and the store decides what is safe to
	// publish. class overrides classification for failures the executor
	// recognizes itself and that have no HTTP response to classify.
	err   error
	class ErrorClassKey

	// ckPhase and ckKey are the checkpoint coordinates of the item that failed.
	// Carrying them means a re-run can reset exactly the items chosen, rather
	// than resetting a whole phase and retrying things nobody selected.
	ckPhase string
	ckKey   string
}

// GatherProgress represents a progress update during data gathering.
type GatherProgress struct {
	Resource string `json:"resource"`
	Current  int    `json:"current"`
	Total    int    `json:"total"` // 0 if unknown
	Message  string `json:"message"`
}
