package doppler

import "encoding/json"

// Workplace represents Doppler workplace settings.
type Workplace struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	BillingEmail  string `json:"billing_email"`
	SecurityEmail string `json:"security_email"`
}

// Project represents a Doppler project.
type Project struct {
	ID          string `json:"id"`
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
	CreatedAt   string `json:"created_at"`
}

// Environment represents a Doppler environment within a project.
type Environment struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Slug            string `json:"slug"`
	Project         string `json:"project"`
	PersonalConfigs bool   `json:"personal_configs"`
}

// ConfigDescriptor identifies a config by project and config name.
type ConfigDescriptor struct {
	Project string `json:"project"`
	Config  string `json:"config"`
}

// Config represents a Doppler config (root or branch).
type Config struct {
	Name        string             `json:"name"`
	Root        bool               `json:"root"`
	Locked      bool               `json:"locked"`
	Inheritable bool               `json:"inheritable"`
	Inheriting  bool               `json:"inheriting"`
	Inherits    []ConfigDescriptor `json:"inherits"`
	InheritedBy []ConfigDescriptor `json:"inherited_by"`
	Environment string             `json:"environment"`
	Project     string             `json:"project"`
	Slug        string             `json:"slug"`
}

// SecretValueType is a secret's declared type. The API models it as a
// single-field object, not a bare string:
//
//	"rawValueType": { "type": "json" }
//
// Valid types are string, json, json5, boolean, integer, decimal, email, url,
// uuidv4, cuid2, ulid, datetime8601, date8601, yaml, directReference and xml.
// Carried across verbatim rather than validated here — an unknown type added
// upstream should migrate, not be rejected by this tool.
type SecretValueType struct {
	Type string `json:"type"`
}

// Secret represents a single secret with its raw and computed values.
// Secret is one secret as the API returns it.
//
// The response also carries computed, computedVisibility and computedValueType —
// the same secret with its ${REFERENCE} expressions resolved. They are
// deliberately not declared here, so they are discarded at parse time rather
// than held: the migration writes raw values so that references survive as
// references, and a second copy of every secret value in memory for the length
// of a run is a cost with nothing on the other side of it.
//
// Nothing is lost by dropping them. For a static secret, computed is raw with
// references resolved. The only secrets whose value lives solely in computed are
// dynamic secrets (raw is "") and the DOPPLER_* reserved ones — and the gather
// excludes both, the first via include_dynamic_secrets=false and the second by
// name. If a future change starts migrating either, it needs the value from
// somewhere other than this struct anyway.
type Secret struct {
	Raw           string           `json:"raw"`
	Note          string           `json:"note"`
	RawVisibility string           `json:"rawVisibility"`
	RawValueType  *SecretValueType `json:"rawValueType,omitempty"`
}

// SecretsMap is a map of secret name to Secret.
type SecretsMap map[string]Secret

// SecretChangeRequest represents a single secret change in the change_requests
// format, which is the only write path that carries visibility.
//
// Name, OriginalName and Value are serialized unconditionally: the API's schema
// marks originalName and value as nullable but *required*, so omitting them (as
// `omitempty` would for a nil pointer) is rejected outright. OriginalName is null
// for a secret that does not already exist, which is every secret on a fresh target.
type SecretChangeRequest struct {
	Name           string           `json:"name"`
	OriginalName   *string          `json:"originalName"`
	Value          *string          `json:"value"`
	Visibility     *string          `json:"visibility,omitempty"`
	ValueType      *SecretValueType `json:"valueType,omitempty"`
	ShouldDelete   bool             `json:"shouldDelete,omitempty"`
	ShouldConverge bool             `json:"shouldConverge,omitempty"`
}

// Role represents a Doppler workplace or project role.
type Role struct {
	Name         string   `json:"name"`
	Permissions  []string `json:"permissions"`
	Identifier   string   `json:"identifier"`
	IsCustomRole bool     `json:"is_custom_role"`
	IsInlineRole bool     `json:"is_inline_role"`
}

// GroupDefaultRole represents the default project role for a group.
type GroupDefaultRole struct {
	Identifier string `json:"identifier"`
}

// Group represents a Doppler workplace group.
type Group struct {
	Name          string            `json:"name"`
	Slug          string            `json:"slug"`
	CreatedAt     string            `json:"created_at"`
	DefaultRole   *GroupDefaultRole `json:"default_project_role,omitempty"`
	WorkplaceRole *GroupDefaultRole `json:"workplace_role,omitempty"`
}

// GroupMember represents a member of a group.
type GroupMember struct {
	Type string `json:"type"`
	Slug string `json:"slug"`
}

// ServiceAccount represents a Doppler service account.
type ServiceAccount struct {
	Name      string `json:"name"`
	Slug      string `json:"slug"`
	CreatedAt string `json:"created_at"`
	// WorkplaceRole determines the account's permissions. Without it a migrated
	// account lands on the target's default role, which is a privilege change in
	// whichever direction the default happens to differ.
	WorkplaceRole *GroupDefaultRole `json:"workplace_role,omitempty"`
}

// ServiceToken represents a Doppler service token (metadata only).
type ServiceToken struct {
	Name        string  `json:"name"`
	Slug        string  `json:"slug"`
	CreatedAt   string  `json:"created_at"`
	ExpiresAt   *string `json:"expires_at"`
	Project     string  `json:"project"`
	Config      string  `json:"config"`
	Environment string  `json:"environment"`
	Access      string  `json:"access"`
}

// ProjectMember represents a member assigned to a project.
type ProjectMember struct {
	Type                  string     `json:"type"`
	Slug                  string     `json:"slug"`
	Role                  MemberRole `json:"role"`
	AccessAllEnvironments bool       `json:"access_all_environments"`
	Environments          []string   `json:"environments"`
}

// MemberRole represents a role assignment.
type MemberRole struct {
	Identifier string `json:"identifier"`
}

// Integration represents a Doppler integration (metadata only, credentials redacted).
type Integration struct {
	Slug    string `json:"slug"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Kind    string `json:"kind"`
	Enabled bool   `json:"enabled"`
	Syncs   []Sync `json:"syncs"`
}

// Sync represents a secrets sync attached to an integration.
type Sync struct {
	Slug         string `json:"slug"`
	Project      string `json:"project"`
	Config       string `json:"config"`
	Description  string `json:"description"`
	Integration  string `json:"integration"`
	Enabled      bool   `json:"enabled"`
	LastSyncedAt string `json:"lastSyncedAt"`
	Status       string `json:"status"`
}

// Webhook represents a Doppler webhook.
type Webhook struct {
	ID  string `json:"id"`
	URL string `json:"url"`
	// Secret is only ever set on write. The API reports whether a signing secret
	// exists via HasSecret but never returns the value, so a migrated webhook is
	// created unsigned and the secret becomes a manual step.
	Secret         string   `json:"secret,omitempty"`
	HasSecret      bool     `json:"hasSecret,omitempty"`
	Enabled        bool     `json:"enabled"`
	EnabledConfigs []string `json:"enabledConfigs"`
}

// TrustedIP represents a trusted IP for a config.
type TrustedIP struct {
	IP string `json:"ip"`
}

// WorkplaceUser represents a user in the workplace.
type WorkplaceUser struct {
	ID     string `json:"id"`
	Access string `json:"access"`
	User   struct {
		Email    string `json:"email"`
		Name     string `json:"name"`
		Username string `json:"username"`
	} `json:"user"`
}

// TokenInfo represents the response from GET /v3/me.
type TokenInfo struct {
	Name         string         `json:"name"`
	Slug         string         `json:"slug"`
	Type         string         `json:"type"`
	Workplace    TokenWorkplace `json:"workplace"`
	TokenPreview string         `json:"token_preview,omitempty"`
	// Principal identifies who the token acts as. For a personal token its Type is
	// "workplace_user" and its Slug is that user's workplace slug — which is what
	// lets the migration avoid changing its own account's access level.
	Principal TokenPrincipal `json:"principal"`
}

// TokenPrincipal is the identity a token acts as.
type TokenPrincipal struct {
	Type string `json:"type"`
	Slug string `json:"slug"`
}

// TokenWorkplace represents the workplace object nested in the /v3/me response.
type TokenWorkplace struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// Change request policy subject types.
const (
	SubjectWorkplaceUser  = "WorkplaceUser"
	SubjectGroup          = "Group"
	SubjectServiceAccount = "ServiceAccount"
)

// Change request policy rule types.
const (
	RuleRequiredReviewer    = "RequiredReviewer"
	RuleDisallowSelfReview  = "DisallowSelfReview"
	RuleAutoAssignReviewers = "AutoAssignReviewers"
)

// ChangeRequestPolicySubject identifies who can satisfy a review rule. Slug is the
// subject's public id, which differs between instances and must be remapped on
// migration. Name is returned on read and ignored on write.
type ChangeRequestPolicySubject struct {
	Type string `json:"type"`
	Slug string `json:"slug"`
	Name string `json:"name,omitempty"`
}

// ChangeRequestPolicyRule is one rule in a policy. The API models these as a
// discriminated union on Type, so which fields apply depends on it:
//
//	RequiredReviewer     — Count, and Subjects (nil means any reviewer qualifies)
//	DisallowSelfReview   — no further fields
//	AutoAssignReviewers  — Strategy ("all" or "matchCount")
type ChangeRequestPolicyRule struct {
	Type string `json:"type"`

	// RequiredReviewer
	Count int `json:"count,omitempty"`
	// Subjects must be omitted rather than sent empty: the API rejects an empty
	// list but treats an absent one as "any reviewer".
	Subjects []ChangeRequestPolicySubject `json:"subjects,omitempty"`

	// AutoAssignReviewers
	Strategy string `json:"strategy,omitempty"`
}

// ChangeRequestPolicyTarget describes what within a project a policy covers.
// When All is true the API requires EnvSlugs and ConfigNames to be absent, which
// omitempty handles.
type ChangeRequestPolicyTarget struct {
	All         bool     `json:"all"`
	EnvSlugs    []string `json:"envSlugs,omitempty"`
	ConfigNames []string `json:"configNames,omitempty"`
}

// ChangeRequestPolicyTargets is a policy's target manifest. Projects is keyed by
// project slug; a nil value means the project is named but nothing is targeted.
type ChangeRequestPolicyTargets struct {
	AllProjects bool                                  `json:"allProjects"`
	Projects    map[string]*ChangeRequestPolicyTarget `json:"projects"`
}

// ChangeRequestPolicy is a workplace-level change request policy.
// Endpoints are workplace-scoped: /v3/workplace/change_request_policies
type ChangeRequestPolicy struct {
	ID          string                     `json:"id,omitempty"`
	Name        string                     `json:"name"`
	Description string                     `json:"description"`
	Rules       []ChangeRequestPolicyRule  `json:"rules"`
	Targets     ChangeRequestPolicyTargets `json:"targets"`
}

// ServiceAccountToken is an API token belonging to a service account.
// As with config service tokens, the value is only returned at creation time, so
// these can be reported but not migrated.
type ServiceAccountToken struct {
	Name       string  `json:"name"`
	Slug       string  `json:"slug"`
	CreatedAt  string  `json:"created_at"`
	LastSeenAt *string `json:"last_seen_at"`
	ExpiresAt  *string `json:"expires_at"`

	// ServiceAccount is filled in by the gatherer — the API nests tokens under a
	// service account rather than naming it in the payload.
	ServiceAccount string `json:"service_account"`
}

// ServiceAccountIdentity is a keyless auth identity (currently OIDC) attached to a
// service account.
//
// Config is carried verbatim rather than modeled: it holds the auth method's
// matching rules — for OIDC, a discovery URL and the claims to match — which contain
// no secrets and round-trip unchanged. Passing it through also means a new auth
// method migrates without a change here.
type ServiceAccountIdentity struct {
	Slug       string          `json:"slug,omitempty"`
	Name       string          `json:"name"`
	Method     string          `json:"method"`
	TTLSeconds int             `json:"ttl_seconds"`
	Config     json.RawMessage `json:"config,omitempty"`

	// ServiceAccount is filled in by the gatherer; see ServiceAccountToken.
	ServiceAccount string `json:"service_account,omitempty"`
}

// RotatedSecretStatus is a rotated secret's current state.
type RotatedSecretStatus struct {
	Type  string `json:"type"`
	Error string `json:"error,omitempty"`
}

// RotatedSecret is a managed credential that Doppler rotates on a schedule.
// Creating one requires the backing integration and its credentials, so these are
// reported for manual recreation rather than migrated.
type RotatedSecret struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
	// Status is a discriminated union, not a string: {type:"normal"},
	// {type:"inProgress"}, or {type:"retrying"|"integrationDisabled"|
	// "secretDisabled", error:"..."}.
	Status            RotatedSecretStatus `json:"status"`
	Enabled           bool                `json:"enabled"`
	RotationPeriodSec int                 `json:"rotation_period_sec"`
	NextRotationAt    string              `json:"next_rotation_at"`
	Project           string              `json:"project"`
	Config            string              `json:"config"`
	Integration       struct {
		Slug string `json:"slug"`
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"integration"`
}
