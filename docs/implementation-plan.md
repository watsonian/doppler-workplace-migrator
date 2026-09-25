# Doppler On-Prem Migrator — Implementation Plan

This document is a self-contained prompt for Claude Code to build the Doppler On-Prem Migration Tool from scratch. It references two companion documents that must be read before starting:

- `docs/technical-design.md` — architecture, API details, data model, checkpoint system
- `docs/styleguide.md` — visual design system, components, colors, typography

---

## Context

### What This Tool Does

A one-shot migration tool that moves an entire Doppler.com workplace to a self-hosted (on-prem) Doppler instance via the Doppler REST API. The on-prem API is identical to Doppler.com but uses a custom host URL.

### Core Requirements

1. **Go backend** serving a **React/TypeScript frontend** via embedded static files.
2. **Wizard-style UI** with step-by-step flow.
3. **Rate-limit aware** API client using `x-ratelimit-remaining`, `x-ratelimit-reset`, and `retry-after` headers.
4. **Idempotent**: safe to re-run without wiping the target. Checks if resources exist before creating.
5. **Checkpoint system**: tracks migration progress in a JSON file (no secret values, only metadata).
6. **Secrets never touch disk**: all secret data in-memory only, cleared after each config migration.
7. **SSE** for real-time progress updates from backend to frontend.
8. **Dev mode**: bypasses target pre-flight checks (no on-prem instance exists yet for testing).
9. **Tests first**: write tests before implementing each feature. Go uses `testing` + `httptest`. React uses Vitest + React Testing Library.
10. **Docker**: multi-stage build producing a minimal distroless image (~25MB).

### Doppler API Reference

**Base URL**: `https://api.doppler.com/v3/` (source) or `https://<custom-host>/v3/` (target)

**Auth**: `Authorization: Bearer <token>` on every request.

**Rate Limits** (per IP + per key, varies by plan):
- Reads: 240-480/min
- Secret reads: 120-480/min
- Writes: 60-240/min
- Headers on every response: `x-ratelimit-limit`, `x-ratelimit-remaining`, `x-ratelimit-reset`
- On 429: `retry-after` header present

**Pagination**: `?page=N&per_page=100`. No total count — paginate until results < per_page.

**Token types** (detected by prefix): `dp.ct.` (CLI), `dp.pt.` (Personal), `dp.st.` (Service), `dp.sa.` (Service Account), `dp.said.` (SA Identity), `dp.scim.` (SCIM), `dp.audit.` (Audit).

---

## Implementation Phases

Build each phase completely (tests + implementation + verification) before moving to the next. Within each phase, follow this pattern:
1. Write tests first.
2. Implement to make tests pass.
3. Verify all tests pass.
4. Commit.

---

### Phase 1: Project Scaffolding

**Goal**: Set up the project structure, build tooling, and Docker configuration. Verify everything compiles and builds.

#### 1.1 Go Backend Setup

Create `backend/` with:
- `go.mod` with module name `github.com/doppler/onprem-migrator`
- `cmd/server/main.go` — entry point that starts HTTP server, embeds frontend
- `embed.go` — `//go:embed` directive for `backend/webui` (the frontend build is copied there)
- `internal/api/routes.go` — router setup using `net/http` ServeMux (Go 1.22 enhanced routing)
- `internal/api/middleware.go` — logging, CORS (dev only), request ID
- `internal/api/handlers.go` — stub handlers returning 501 for all endpoints

Router should serve:
- `/api/v1/*` — API routes
- `/*` — embedded frontend static files (with SPA fallback to index.html)

Backend API endpoints (all stubs initially):
```
POST   /api/v1/configure
POST   /api/v1/preflight
POST   /api/v1/gather
GET    /api/v1/plan
POST   /api/v1/migrate
POST   /api/v1/migrate/pause
POST   /api/v1/migrate/resume
GET    /api/v1/status
GET    /api/v1/checkpoint
GET    /api/v1/report/service-tokens
GET    /api/v1/report/manual-steps
GET    /api/v1/report/summary
GET    /api/v1/events  (SSE)
```

Tests:
- `internal/api/routes_test.go` — verify all routes are registered and stubs return expected status codes.

#### 1.2 React Frontend Setup

Create `frontend/` with:
- `package.json` with dependencies: `react`, `react-dom`, `react-router-dom`, `lucide-react`
- Dev dependencies: `typescript`, `vite`, `@vitejs/plugin-react`, `tailwindcss`, `postcss`, `autoprefixer`, `vitest`, `@testing-library/react`, `@testing-library/jest-dom`, `jsdom`
- `vite.config.ts` — proxy `/api` to `localhost:8080` in dev mode
- `tsconfig.json`
- `tailwind.config.ts` — extend with Doppler color palette from styleguide (primary-50 through primary-900, etc.)
- `src/styles/index.css` — Tailwind directives + Inter font import from Google Fonts + custom CSS variables from styleguide
- `src/main.tsx` — React root
- `src/App.tsx` — React Router with wizard routes
- Stub pages: `WelcomePage.tsx`, `ConfigurationPage.tsx`, `PreflightPage.tsx`, `GatheringPage.tsx`, `PlanReviewPage.tsx`, `ManualStepsPage.tsx`, `MigrationPage.tsx`, `ServiceTokenReportPage.tsx`, `CompletionPage.tsx` — each showing just the page name

Tests:
- `tests/App.test.tsx` — verify App renders, routes are configured.

#### 1.3 Docker Setup

Create:
- `Dockerfile` — multi-stage build (node:24.14-alpine → golang:1.25-alpine → gcr.io/distroless/static-debian12)
- `docker-compose.yml` — dev config with port 8080, DEV_MODE=true, volume for checkpoint
- `.dockerignore` — node_modules, .git, docs, etc.

#### 1.4 Dev Scripts

Create `scripts/dev.sh`:
- Starts frontend dev server (vite on port 5173)
- Starts Go backend (with `go run`, on port 8080)
- Both in background, with trap for cleanup

Create `Makefile` with targets: `dev`, `build`, `test`, `test-backend`, `test-frontend`, `docker-build`, `docker-run`, `clean`.

---

### Phase 2: Doppler API Client

**Goal**: Implement the Go HTTP client for the Doppler API with rate limiting, pagination, retry logic, and all resource operations.

#### 2.1 Core HTTP Client + Rate Limiter

File: `internal/doppler/client.go`

```go
type Client struct {
    httpClient  *http.Client
    baseURL     string
    token       string
    rateLimiter *RateLimiter
    userAgent   string
}

type ClientOption func(*Client)

func NewClient(baseURL, token string, opts ...ClientOption) *Client
func (c *Client) Get(ctx context.Context, path string, params url.Values) (json.RawMessage, http.Header, error)
func (c *Client) Post(ctx context.Context, path string, body interface{}) (json.RawMessage, http.Header, error)
func (c *Client) Put(ctx context.Context, path string, body interface{}) (json.RawMessage, http.Header, error)
func (c *Client) Patch(ctx context.Context, path string, body interface{}) (json.RawMessage, http.Header, error)
func (c *Client) Delete(ctx context.Context, path string, params url.Values) (json.RawMessage, http.Header, error)
```

File: `internal/doppler/ratelimit.go`

```go
type RateLimitCategory string
const (
    RateLimitRead       RateLimitCategory = "read"
    RateLimitSecretRead RateLimitCategory = "secret_read"
    RateLimitWrite      RateLimitCategory = "write"
)

type RateLimiter struct {
    mu       sync.Mutex
    limits   map[RateLimitCategory]*RateLimitState
    onUpdate func(RateLimitStatus) // callback for SSE
}

type RateLimitState struct {
    Remaining int
    Limit     int
    ResetAt   time.Time
}

type RateLimitStatus struct {
    Category  RateLimitCategory
    Remaining int
    Limit     int
    ResetAt   time.Time
    Waiting   bool
}

func (rl *RateLimiter) Wait(ctx context.Context, category RateLimitCategory) error
func (rl *RateLimiter) Update(category RateLimitCategory, headers http.Header)
func (rl *RateLimiter) Status() map[RateLimitCategory]RateLimitStatus
```

Every HTTP request should:
1. Call `rateLimiter.Wait()` before sending.
2. Send request.
3. Call `rateLimiter.Update()` with response headers.
4. On 429: parse `retry-after`, wait, retry automatically.
5. On 5xx: retry up to 3 times with exponential backoff (1s, 2s, 4s).
6. On 4xx (not 429): return error immediately.

Tests (`internal/doppler/client_test.go`):
- Mock HTTP server using `httptest`.
- Test successful GET/POST/PUT/PATCH/DELETE.
- Test rate limit headers are parsed correctly.
- Test 429 triggers wait and retry.
- Test 5xx triggers exponential backoff retry.
- Test 4xx returns error without retry.
- Test context cancellation stops waiting.

Tests (`internal/doppler/ratelimit_test.go`):
- Test Wait blocks when remaining is 0.
- Test Wait returns immediately when remaining is positive.
- Test Update correctly parses headers.
- Test proactive slowdown when remaining < 10% of limit.
- Test concurrent access is safe.

#### 2.2 Pagination Helper

Add to `client.go`:
```go
func (c *Client) ListAll(ctx context.Context, path string, params url.Values, key string, category RateLimitCategory) ([]json.RawMessage, error)
```

`key` is the JSON response key containing the array (e.g., "projects", "environments", "configs", etc.).

Iterates pages until response array length < per_page (100).

Tests:
- Test pagination with 0 items, 1 page, multiple pages.
- Test pagination stops on empty result.
- Test pagination respects context cancellation.

#### 2.3 API Types

File: `internal/doppler/types.go`

Define Go structs for all API request/response types. Key types:

```go
// Workplace
type Workplace struct {
    ID            string `json:"id"`
    Name          string `json:"name"`
    BillingEmail  string `json:"billing_email"`
    SecurityEmail string `json:"security_email"`
}

// Project
type Project struct {
    ID          string `json:"id"`
    Slug        string `json:"slug"`
    Name        string `json:"name"`
    Description string `json:"description"`
    CreatedAt   string `json:"created_at"`
}

// Environment
type Environment struct {
    ID      string `json:"id"` // This is the slug
    Name    string `json:"name"`
    Slug    string `json:"slug"`
    Project string `json:"project"`
}

// Config
type Config struct {
    Name         string   `json:"name"`
    Root         bool     `json:"root"`
    Locked       bool     `json:"locked"`
    Inheritable  bool     `json:"inheritable"`
    Inheriting   bool     `json:"inheriting"`
    Inherits     []string `json:"inherits"`
    InheritedBy  []string `json:"inherited_by"`
    Environment  string   `json:"environment"`
    Project      string   `json:"project"`
    Slug         string   `json:"slug"`
}

// Secret
type Secret struct {
    Raw               string `json:"raw"`
    Computed          string `json:"computed"`
    Note              string `json:"note"`
    RawVisibility     string `json:"rawVisibility"`
    ComputedVisibility string `json:"computedVisibility"`
}

type SecretsMap map[string]Secret

// Roles
type Role struct {
    Name         string   `json:"name"`
    Permissions  []string `json:"permissions"`
    Identifier   string   `json:"identifier"`
    IsCustomRole bool     `json:"is_custom_role"`
    IsInlineRole bool     `json:"is_inline_role"`
}

// Group
type Group struct {
    Name      string `json:"name"`
    Slug      string `json:"slug"`
    CreatedAt string `json:"created_at"`
}

type GroupMember struct {
    Type string `json:"type"`
    Slug string `json:"slug"`
}

// ServiceAccount
type ServiceAccount struct {
    Name      string `json:"name"`
    Slug      string `json:"slug"`
    CreatedAt string `json:"created_at"`
}

// ServiceToken (metadata only — values are not retrievable)
type ServiceToken struct {
    Name         string  `json:"name"`
    Slug         string  `json:"slug"`
    CreatedAt    string  `json:"created_at"`
    ExpiresAt    *string `json:"expires_at"`
    Project      string  `json:"project"`
    Config       string  `json:"config"`
    Environment  string  `json:"environment"`
    Access       string  `json:"access"` // read or read/write
}

// ProjectMember
type ProjectMember struct {
    Type                  string   `json:"type"`   // workplace_user, group, invite, service_account
    Slug                  string   `json:"slug"`
    Role                  MemberRole `json:"role"`
    AccessAllEnvironments bool     `json:"access_all_environments"`
    Environments          []string `json:"environments"`
}

type MemberRole struct {
    Identifier string `json:"identifier"`
}

// Integration
type Integration struct {
    Slug   string `json:"slug"`
    Name   string `json:"name"`
    Type   string `json:"type"`
    Enabled bool  `json:"enabled"`
}

// Webhook
type Webhook struct {
    ID        string `json:"id"`
    URL       string `json:"url"`
    Secret    string `json:"secret"`
    Enabled   bool   `json:"enabled"`
    EnabledConfigs []string `json:"enabledConfigs"`
}

// TrustedIP
type TrustedIP struct {
    IP string `json:"ip"`
}

// WorkplaceUser
type WorkplaceUser struct {
    ID     string `json:"id"`
    Access string `json:"access"`
    User   struct {
        Email    string `json:"email"`
        Name     string `json:"name"`
        Username string `json:"username"`
    } `json:"user"`
}

// Auth token info
type TokenInfo struct {
    Name        string `json:"name"`
    Slug        string `json:"slug"`
    TokenType   string `json:"token_type"`
    Workplace   string `json:"workplace"`
}

// ChangeRequestPolicy
type ChangeRequestPolicy struct {
    Slug     string `json:"slug"`
    Name     string `json:"name"`
    // Additional fields as returned by API
}
```

#### 2.4 Resource Operations

Create one file per resource type in `internal/doppler/`. Each file implements read and write operations.

**File: `workplace.go`**
```go
func (c *Client) GetWorkplace(ctx context.Context) (*Workplace, error)
func (c *Client) UpdateWorkplace(ctx context.Context, wp *Workplace) (*Workplace, error)
```

**File: `projects.go`**
```go
func (c *Client) ListProjects(ctx context.Context) ([]Project, error)
func (c *Client) GetProject(ctx context.Context, slug string) (*Project, error)
func (c *Client) CreateProject(ctx context.Context, name, description string) (*Project, error)
```

**File: `environments.go`**
```go
func (c *Client) ListEnvironments(ctx context.Context, project string) ([]Environment, error)
func (c *Client) CreateEnvironment(ctx context.Context, project, name, slug string) (*Environment, error)
```

**File: `configs.go`**
```go
func (c *Client) ListConfigs(ctx context.Context, project string) ([]Config, error)
func (c *Client) GetConfig(ctx context.Context, project, config string) (*Config, error)
func (c *Client) CreateConfig(ctx context.Context, project, environment, name string) (*Config, error)
func (c *Client) LockConfig(ctx context.Context, project, config string) error
func (c *Client) SetConfigInheritable(ctx context.Context, project, config string, inheritable bool) error
```

**File: `secrets.go`**
```go
func (c *Client) ListSecrets(ctx context.Context, project, config string) (SecretsMap, error)
func (c *Client) UpdateSecrets(ctx context.Context, project, config string, secrets map[string]interface{}) error
// Note: use change_requests format to preserve visibility and notes
func (c *Client) UpdateSecretsWithMetadata(ctx context.Context, project, config string, changes []SecretChangeRequest) error
func (c *Client) UpdateSecretNote(ctx context.Context, project, config, secretName, note string) error
```

**File: `roles.go`**
```go
func (c *Client) ListWorkplaceRoles(ctx context.Context) ([]Role, error)
func (c *Client) CreateWorkplaceRole(ctx context.Context, name string, permissions []string) (*Role, error)
func (c *Client) ListProjectRoles(ctx context.Context) ([]Role, error)
func (c *Client) CreateProjectRole(ctx context.Context, name string, permissions []string) (*Role, error)
```

**File: `groups.go`**
```go
func (c *Client) ListGroups(ctx context.Context) ([]Group, error)
func (c *Client) CreateGroup(ctx context.Context, name string) (*Group, error)
func (c *Client) GetGroupMembers(ctx context.Context, groupSlug string) ([]GroupMember, error)
func (c *Client) AddGroupMember(ctx context.Context, groupSlug string, memberType, memberSlug string) error
```

**File: `service_accounts.go`**
```go
func (c *Client) ListServiceAccounts(ctx context.Context) ([]ServiceAccount, error)
func (c *Client) CreateServiceAccount(ctx context.Context, name string) (*ServiceAccount, error)
```

**File: `service_tokens.go`**
```go
func (c *Client) ListServiceTokens(ctx context.Context, project, config string) ([]ServiceToken, error)
// Note: no CreateServiceToken — we only read for the report
```

**File: `trusted_ips.go`**
```go
func (c *Client) ListTrustedIPs(ctx context.Context, project, config string) ([]TrustedIP, error)
func (c *Client) AddTrustedIP(ctx context.Context, project, config, ip string) error
```

**File: `webhooks.go`**
```go
func (c *Client) ListWebhooks(ctx context.Context, project string) ([]Webhook, error)
func (c *Client) CreateWebhook(ctx context.Context, project string, webhook *Webhook) (*Webhook, error)
```

**File: `integrations.go`**
```go
func (c *Client) ListIntegrations(ctx context.Context) ([]Integration, error)
// Note: no Create — integrations require manual setup (credentials are redacted)
```

**File: `project_members.go`**
```go
func (c *Client) ListProjectMembers(ctx context.Context, project string) ([]ProjectMember, error)
func (c *Client) AddProjectMember(ctx context.Context, project string, memberType, memberSlug, role string, environments []string) error
```

**File: `users.go`**
```go
func (c *Client) ListWorkplaceUsers(ctx context.Context) ([]WorkplaceUser, error)
```

**File: `auth.go`**
```go
func (c *Client) GetTokenInfo(ctx context.Context) (*TokenInfo, error) // GET /v3/me
```

Tests for each file:
- Mock API responses with `httptest`.
- Test successful operations.
- Test error responses.
- Test pagination where applicable.
- Test empty results.

---

### Phase 3: Pre-flight & Configuration

**Goal**: Implement the configuration and pre-flight check systems.

#### 3.1 Configuration State

File: `internal/migration/types.go`

```go
type MigrationConfig struct {
    SourceToken string
    TargetHost  string
    TargetToken string
    DevMode     bool
}
```

The migration config is stored in memory on the backend. The `/api/v1/configure` endpoint accepts and validates it.

#### 3.2 Pre-flight Checker

File: `internal/preflight/checker.go`

Checks to run:
1. **Source token valid**: `GET /v3/me` on source. Verify token type is `dp.ct.` or `dp.pt.` (CLI or Personal token).
2. **Source workplace accessible**: `GET /v3/workplace` on source. Verify we get a valid response.
3. **Source can list projects**: `GET /v3/projects?per_page=1` on source. Verify read access.
4. **Target host reachable** (skip in dev mode): HTTP HEAD or GET request to the target host.
5. **Target token valid** (skip in dev mode): `GET /v3/me` on target.
6. **Target workplace accessible** (skip in dev mode): `GET /v3/workplace` on target.
7. **Target can write** (skip in dev mode): Attempt to read project list on target (verify write-capable token type).

```go
type Check struct {
    ID      string
    Name    string
    Status  CheckStatus // pending, running, passed, failed, warning, skipped
    Message string
    Detail  string
}

type CheckStatus string

type Checker struct {
    source  *doppler.Client
    target  *doppler.Client
    devMode bool
    checks  []Check
    onUpdate func(Check) // callback for SSE
}

func (ch *Checker) RunAll(ctx context.Context) ([]Check, error)
```

Tests (`internal/preflight/checker_test.go`):
- Test all checks pass with valid mock responses.
- Test source token invalid → check fails.
- Test source token wrong type → check fails.
- Test target unreachable → check fails.
- Test dev mode skips target checks.
- Test one check failing doesn't prevent others from running.

#### 3.3 API Handlers for Configure & Pre-flight

Update `internal/api/handlers.go`:

`POST /api/v1/configure`:
- Accept JSON body: `{ sourceToken, targetHost, targetToken, devMode }`.
- Validate inputs (non-empty, targetHost is valid URL, token prefixes look correct).
- Store in-memory.
- Return 200 with validation result.

`POST /api/v1/preflight`:
- Create Doppler clients from stored config.
- Run `Checker.RunAll()`.
- Stream results via SSE (each check emits an event as it completes).
- Return final results as JSON.

Tests:
- Test configure with valid input.
- Test configure with missing fields → 400.
- Test configure with invalid URL → 400.
- Test preflight endpoint triggers checks and returns results.

#### 3.4 Frontend: Configuration Page

File: `frontend/src/pages/ConfigurationPage.tsx`

Three-section form:

**Source section** (card):
- Label: "Source (Doppler.com)"
- Token input: password-type input with show/hide toggle. Monospace font. Placeholder: "dp.ct.xxxx or dp.pt.xxxx"
- Inline validation: detect token prefix, show badge ("CLI Token" / "Personal Token" / "Unknown"). Show error if prefix is not dp.ct. or dp.pt.

**Target section** (card):
- Label: "Target (On-Prem Doppler)"
- Host URL input: text input. Placeholder: "https://doppler.internal.company.com". Validate URL format.
- Token input: same as source token input.

**Options section** (card):
- Dev mode toggle: checkbox/switch with label "Dev Mode" and helper text: "Skip target connection checks. Use this when the on-prem instance isn't available yet."

**Next button**: disabled until all required fields are valid (source token + target host + target token, or source token + dev mode).

Tests:
- Renders all form fields.
- Validates token prefix format.
- Shows token type badge.
- Validates URL format.
- Next button disabled when form invalid.
- Next button enabled when form valid.
- Submits configuration to backend.

#### 3.5 Frontend: Pre-flight Page

File: `frontend/src/pages/PreflightPage.tsx`

Vertical checklist showing each check:
- Each row: status icon + check name + message.
- Status icons: gray circle (pending) → spinner (running) → green check (passed) → red X (failed) → yellow triangle (warning) → gray dash (skipped).
- Expanding a failed check shows detail text.
- Summary at bottom: "All checks passed" (success alert) or "N checks failed" (error alert).
- "Next" button only enabled when all checks passed (or only warnings).
- "Back" button to return to configuration.
- In dev mode, skipped checks show "(Skipped — Dev Mode)" note.

Subscribe to SSE endpoint to get real-time check results.

Tests:
- Renders check list.
- Updates check status in real-time from SSE events.
- Shows detail on expand.
- Next button state matches check results.
- Dev mode shows skipped checks correctly.

---

### Phase 4: Data Gathering

**Goal**: Implement the data gathering engine that reads all resources from the source Doppler instance.

#### 4.1 Gatherer

File: `internal/migration/gatherer.go`

The gatherer reads all migratable resources from the source instance and assembles them into a `MigrationData` struct.

```go
type MigrationData struct {
    Workplace         *doppler.Workplace
    WorkplaceUsers    []doppler.WorkplaceUser
    WorkplaceRoles    []doppler.Role    // custom only
    ProjectRoles      []doppler.Role    // custom only
    Groups            []GroupWithMembers
    ServiceAccounts   []doppler.ServiceAccount
    Projects          []ProjectData
    Integrations      []doppler.Integration   // for manual steps report
    ServiceTokens     []doppler.ServiceToken  // for report
}

type GroupWithMembers struct {
    Group   doppler.Group
    Members []doppler.GroupMember
}

type ProjectData struct {
    Project      doppler.Project
    Environments []doppler.Environment
    Configs      []ConfigData
    Webhooks     []doppler.Webhook
    Members      []doppler.ProjectMember
}

type ConfigData struct {
    Config     doppler.Config
    Secrets    doppler.SecretsMap  // held temporarily, cleared after plan generation
    TrustedIPs []doppler.TrustedIP
}

type GatherProgress struct {
    Resource string
    Current  int
    Total    int // may be 0 if unknown
    Message  string
}

type Gatherer struct {
    source   *doppler.Client
    onProgress func(GatherProgress)
}

func (g *Gatherer) GatherAll(ctx context.Context) (*MigrationData, error)
```

Gathering order:
1. Workplace info
2. Workplace users (for user existence check later)
3. Workplace roles → filter to custom only
4. Project roles → filter to custom only
5. Groups → for each, get members
6. Service accounts
7. Integrations (for manual steps report)
8. Projects (paginated)
9. For each project:
   a. Environments
   b. Configs
   c. For each config: secrets, trusted IPs, service tokens
   d. Webhooks
   e. Project members

Emit progress events throughout.

**Secret handling**: Secrets are gathered using the `raw` field (preserves `${REFERENCE}` syntax). After the migration plan is generated and approved by the user, secrets remain in memory for the migration phase. After each config's secrets are migrated to the target, clear the secret data for that config from memory.

Tests (`internal/migration/gatherer_test.go`):
- Mock the Doppler client (interface-based).
- Test gathering with multiple projects, environments, configs.
- Test progress events are emitted correctly.
- Test filtering custom roles only.
- Test handling of empty workplaces.
- Test context cancellation mid-gather.

#### 4.2 API Handler for Gather

`POST /api/v1/gather`:
- Creates `Gatherer` with source client.
- Runs `GatherAll()`.
- Stores `MigrationData` in-memory.
- Streams progress via SSE.
- Returns summary when complete.

`GET /api/v1/plan`:
- Returns the migration plan derived from `MigrationData`.
- The plan is a structured summary (counts, tree structure) without actual secret values.

Tests:
- Test gather endpoint starts gathering and returns progress.
- Test plan endpoint returns structured plan after gathering.

#### 4.3 Frontend: Gathering Page

File: `frontend/src/pages/GatheringPage.tsx`

- Overall progress bar at top.
- Per-resource progress sections (cards):
  - Workplace info → checkmark when done.
  - Projects: "Fetching projects... (12 found)" with progress bar.
  - For each project: environments, configs, secrets count.
  - Roles, groups, etc.
- Rate limit indicator in top-right corner (small badge showing remaining/limit, color-coded: green > 50%, yellow 10-50%, red < 10%).
- Auto-advances to Plan Review page when complete.
- Error handling: if gathering fails, show error alert with retry button.

Subscribe to SSE for real-time progress.

Tests:
- Renders progress sections.
- Updates from SSE events.
- Shows rate limit indicator.
- Auto-advances on completion.
- Handles errors with retry.

---

### Phase 5: Migration Plan & Manual Steps

**Goal**: Present the gathered data as an approvable migration plan and identify manual steps.

#### 5.1 Planner

File: `internal/migration/planner.go`

```go
type MigrationPlan struct {
    Summary       PlanSummary
    Workplace     WorkplacePlan
    Projects      []ProjectPlan
    ManualSteps   []ManualStep
    UserCheck     UserCheckResult
}

type PlanSummary struct {
    ProjectCount      int
    EnvironmentCount  int
    ConfigCount       int
    SecretCount       int
    RoleCount         int
    GroupCount        int
    WebhookCount      int
    ServiceAccountCount int
    TrustedIPCount    int
    ManualStepCount   int
}

type WorkplacePlan struct {
    Settings        bool  // true if workplace settings will be migrated
    WorkplaceRoles  []string // names of custom roles
    ProjectRoles    []string // names of custom roles
    Groups          []GroupPlanItem
    ServiceAccounts []string
}

type GroupPlanItem struct {
    Name        string
    MemberCount int
}

type ProjectPlan struct {
    Name         string
    Description  string
    Environments []EnvironmentPlan
    Configs      []ConfigPlan
    Webhooks     []WebhookPlan
    Members      []MemberPlan
}

type EnvironmentPlan struct {
    Name string
    Slug string
}

type ConfigPlan struct {
    Name         string
    Environment  string
    Root         bool
    SecretCount  int
    TrustedIPs   int
    Locked       bool
    Inheritable  bool
    Inherits     []string
}

type WebhookPlan struct {
    URL     string
    Enabled bool
}

type MemberPlan struct {
    Type string
    Slug string
    Role string
}

type ManualStep struct {
    Category    string // "integration", "service_token", "sync", "user"
    Title       string
    Description string
    Items       []ManualStepItem
}

type ManualStepItem struct {
    Name    string
    Detail  string
    Context map[string]string // additional key-value info
}

type UserCheckResult struct {
    Required []string // emails needed
    Existing []string // emails found on target (empty in dev mode)
    Missing  []string // emails not found on target
}

func GeneratePlan(data *MigrationData) *MigrationPlan
```

Manual steps generated:
1. **Integrations**: List each integration with name and type. Explain that credentials are not transferable and must be reconfigured on on-prem.
2. **Service Tokens**: List each service token with project, config, name, and expiry. Explain that token values can't be migrated and new tokens must be created.
3. **Service Account Tokens**: Same as service tokens.
4. **Secrets Syncs**: Explain that syncs can't be listed via API. User should document syncs from Doppler.com dashboard and recreate after integrations are configured on on-prem.
5. **Users** (if any missing): List missing user emails. Explain they need to be provisioned via SCIM before access control can be migrated.

Tests (`internal/migration/planner_test.go`):
- Test plan generation with complete data.
- Test summary counts are correct.
- Test manual steps are generated for integrations.
- Test manual steps are generated for service tokens.
- Test user check identifies missing users.
- Test empty workplace generates minimal plan.

#### 5.2 User Existence Check

Before the plan is finalized, if not in dev mode, check target for user existence:
1. Gather all unique user identifiers from: group members (type=workplace_user), project members (type=workplace_user).
2. List users on target via `ListWorkplaceUsers()`.
3. Compare by email.
4. Populate `UserCheckResult`.

In dev mode: skip this check, show a notice that it will be performed at migration time.

#### 5.3 Frontend: Plan Review Page

File: `frontend/src/pages/PlanReviewPage.tsx`

- Summary card at top showing aggregate stats (projects, environments, configs, secrets, etc.) using badges from styleguide.
- Tree view (see styleguide) showing full hierarchy: Workplace → Roles → Groups → Projects → Environments → Configs (with secret counts).
- Each tree node is expandable/collapsible.
- Resource type badges inline (colored per styleguide).
- If manual steps exist: warning card at bottom with count and "View Manual Steps" button.
- If missing users: warning alert listing missing user emails with explanation.
- "Approve & Start Migration" primary button.
- "Back" button.

Tests:
- Renders plan summary.
- Renders tree view with correct hierarchy.
- Tree nodes expand/collapse.
- Shows manual steps warning when applicable.
- Shows user warning when users are missing.
- Approve button advances to migration.

#### 5.4 Frontend: Manual Steps Page

File: `frontend/src/pages/ManualStepsPage.tsx`

Shown after plan review (or accessible from plan review page).

Sections (one card per category):

**Integrations** (if any):
- Info alert explaining why integrations can't be auto-migrated.
- Table: Integration Name | Type | Action Required
- Each row: name, type badge, "Recreate on on-prem with new credentials"

**Secrets Syncs**:
- Warning alert explaining that syncs can't be listed via API.
- Instructions: "Before migrating, document all secrets syncs from your Doppler.com dashboard. After migration, reconfigure integrations on on-prem, then recreate syncs."

**Users** (if any missing):
- Error/warning alert listing missing emails.
- Instructions: "These users must be provisioned on the on-prem instance (via SCIM or manual invite) before access control migration can complete. Migration will skip access assignments for missing users."

"Continue to Migration" button.

Tests:
- Renders each section based on manual step data.
- Hides sections with no items.
- Integration table renders correctly.
- User missing list renders.

---

### Phase 6: Migration Execution

**Goal**: Implement the migration engine that creates resources on the target instance.

#### 6.1 Checkpoint System

File: `internal/migration/checkpoint.go`

```go
type Checkpoint struct {
    Version      int                             `json:"version"`
    StartedAt    time.Time                       `json:"started_at"`
    UpdatedAt    time.Time                       `json:"updated_at"`
    SourceHost   string                          `json:"source_host"`
    TargetHost   string                          `json:"target_host"`
    CurrentPhase int                             `json:"current_phase"`
    Phases       map[int]*PhaseCheckpoint        `json:"phases"`
}

type PhaseCheckpoint struct {
    Name    string                      `json:"name"`
    Status  ResourceStatus              `json:"status"`
    Items   map[string]*ItemCheckpoint  `json:"items"`
}

type ItemCheckpoint struct {
    Status     ResourceStatus `json:"status"`
    SourceID   string         `json:"source_id,omitempty"`
    TargetID   string         `json:"target_id,omitempty"`
    Error      string         `json:"error,omitempty"`
    Skipped    bool           `json:"skipped,omitempty"`
    SkipReason string         `json:"skip_reason,omitempty"`
    Children   map[string]*ItemCheckpoint `json:"children,omitempty"`
    Timestamp  time.Time      `json:"timestamp"`
}

type CheckpointManager struct {
    mu         sync.Mutex
    checkpoint *Checkpoint
    filePath   string
}

func NewCheckpointManager(filePath string) *CheckpointManager
func (cm *CheckpointManager) Load() error                               // Load from disk
func (cm *CheckpointManager) Save() error                               // Save to disk
func (cm *CheckpointManager) GetItemStatus(phase int, key string) ResourceStatus
func (cm *CheckpointManager) SetItemStatus(phase int, key string, status ItemCheckpoint) error
func (cm *CheckpointManager) SetPhaseStatus(phase int, status ResourceStatus) error
func (cm *CheckpointManager) GetCheckpoint() *Checkpoint                // Clone for reading
func (cm *CheckpointManager) IsComplete(phase int, key string) bool     // Check if already done
```

Checkpoint is saved to disk after every status update (debounced, max once per second). Contains only metadata — never secret values.

Tests (`internal/migration/checkpoint_test.go`):
- Test save and load round-trip.
- Test item status get/set.
- Test IsComplete returns true for completed items.
- Test concurrent access is safe.
- Test checkpoint file format is valid JSON.
- Test resuming from existing checkpoint.

#### 6.2 Migration Executor

File: `internal/migration/executor.go`

```go
type Executor struct {
    source      *doppler.Client
    target      *doppler.Client
    data        *MigrationData
    plan        *MigrationPlan
    checkpoint  *CheckpointManager
    devMode     bool
    paused      atomic.Bool
    onProgress  func(MigrateProgress)
}

type MigrateProgress struct {
    Phase    int
    Resource string
    Item     string
    Status   ResourceStatus
    Current  int
    Total    int
    Error    string
}

func (e *Executor) Execute(ctx context.Context) error
func (e *Executor) Pause()
func (e *Executor) Resume()
```

Execution follows the phase order from the technical design:

**Phase 1: Workplace Foundation**
- 1.1: Update workplace settings on target (name, billing_email, security_email).
- 1.2: Create custom workplace roles. For each: check if exists by name on target → create if not.
- 1.3: Create custom project roles. Same idempotency check.

**Phase 2: Groups & Service Accounts**
- 2.1: Create groups (without member assignment). For each: check if exists by slug → create if not.
- 2.2: Create service accounts. For each: check if exists by name → create if not.

**Phase 3: Projects & Environments**
- 3.1: Create projects. For each: check if exists by name/slug → create if not.
- 3.2: For each project, create environments. Check if exists by slug → create if not.

**Phase 4: Configs**
- 4.1: Root configs are auto-created with environments. Verify they exist.
- 4.2: Create branch configs. For each: check if exists by name → create if not.

**Phase 5: Config Data**
- 5.1: For each config, migrate secrets. Use the change_requests format to set values, visibility, and notes.
  - Fetch current secrets from target to determine what's new vs. what needs updating.
  - Use `raw` values from source to preserve references.
  - After a config's secrets are migrated, clear the in-memory secret data for that config.
  - Update secret notes separately via the notes endpoint.
- 5.2: For each config, add trusted IPs. Check if IP already exists → add if not.
- 5.3: Lock configs that were locked on source (after secrets are migrated).

**Phase 6: Config Relationships**
- 6.1: Set inheritable flag on configs that had it set.
- Note: The inheritance chain itself may be auto-established based on config naming conventions, or may need explicit API calls. Test this during development.

**Phase 7: Project Metadata**
- 7.1: Create webhooks per project. Check if webhook with same URL exists → create if not.
- 7.2: Migrate change request policies if applicable.

**Phase 8: Access Control** (skip if dev mode or if users are missing)
- 8.1: Add members to groups. For each group, for each member: check if already a member → add if not. Skip members whose users don't exist on target.
- 8.2: Add project members. For each project, for each member: check if already assigned → add if not. Skip members whose users don't exist on target.

**For each item in each phase:**
1. Check checkpoint → skip if already completed.
2. Check if resource exists on target → mark completed if so.
3. Create/update resource on target.
4. On success → mark completed in checkpoint.
5. On failure → mark failed in checkpoint, log error, continue to next item.
6. Check if paused → wait if so.

**Dev mode behavior**: In dev mode, skip phases 6, 7, 8 (or at least the parts requiring a live target). The gather and plan phases work normally against the real source. The actual write operations are skipped with a log message "Dev mode: would create X on target."

Tests (`internal/migration/executor_test.go`):
- Test full migration with mock source and target clients.
- Test idempotency: resource already exists on target → skipped.
- Test checkpoint resume: items marked completed are skipped.
- Test individual phase failures don't halt other phases.
- Test pause/resume.
- Test dev mode skips write operations.
- Test secret data is cleared after config migration.
- Test locked configs are locked after secrets are migrated.

#### 6.3 API Handlers for Migration

`POST /api/v1/migrate`:
- Start migration execution.
- Stream progress via SSE.
- Return final result.

`POST /api/v1/migrate/pause`:
- Signal executor to pause.

`POST /api/v1/migrate/resume`:
- Signal executor to resume.

`GET /api/v1/status`:
- Return current migration status (phase, progress, errors).

`GET /api/v1/checkpoint`:
- Return checkpoint data.

#### 6.4 Frontend: Migration Page

File: `frontend/src/pages/MigrationPage.tsx`

- Overall progress bar showing phase completion (8 phases).
- Current phase card showing:
  - Phase name and number.
  - Per-item progress list with status icons.
  - Current item highlighted with spinner.
  - Failed items shown in red with expandable error detail.
  - Skipped items shown in gray.
- Completed phases shown as collapsed sections with green checkmark.
- Pause/Resume button.
- Rate limit indicator.
- Error recovery: if migration fails, show what failed and option to retry.

Subscribe to SSE for real-time progress.

Tests:
- Renders phase progress.
- Updates from SSE events.
- Shows per-item status.
- Pause/Resume buttons work.
- Error items are expandable.
- Completed phases collapse.

---

### Phase 7: Reports & Completion

**Goal**: Implement the service token report and completion summary.

#### 7.1 Service Token Report

`GET /api/v1/report/service-tokens`:
- Return all service tokens gathered from source, grouped by project/config.

Frontend: `frontend/src/pages/ServiceTokenReportPage.tsx`

- Explanation card at top:
  - "Service tokens cannot be automatically migrated because token values are only available at creation time."
  - "You will need to create new service tokens on your on-prem Doppler instance for each token listed below, then update your applications and CI/CD pipelines to use the new tokens."
- Table with columns: Project | Config | Environment | Token Name | Access Level | Created | Expires
- All columns sortable.
- Info alert: "After creating new tokens on on-prem, update your deployments to use the new token values."
- For service account tokens: separate section with same format.

Tests:
- Renders explanation.
- Renders table with token data.
- Columns sort correctly.
- Handles empty token list.

#### 7.2 Manual Steps Report

`GET /api/v1/report/manual-steps`:
- Return all manual steps identified during planning.

The Manual Steps page (built in Phase 5.4) serves this purpose. It should also be accessible from the completion page.

#### 7.3 Migration Summary

`GET /api/v1/report/summary`:
- Return complete migration summary: what was migrated, what failed, what was skipped, manual steps remaining.

Frontend: `frontend/src/pages/CompletionPage.tsx`

- Large success icon (green checkmark in circle) at top — or warning icon if there were failures.
- Heading: "Migration Complete" or "Migration Complete with Warnings".
- Summary statistics card:
  - Migrated: X projects, Y environments, Z configs, N secrets, etc.
  - Failed: count with link to details.
  - Skipped: count with link to details.
- If failures: expandable error section with each failure and error message.
- Manual steps remaining card:
  - Count of remaining manual steps.
  - "View Manual Steps" button → navigates to manual steps page.
  - "View Service Token Report" button → navigates to service token report page.
- "Download Report" button: downloads a JSON file with the full checkpoint and summary (no secret values).

Tests:
- Renders success state.
- Renders warning state with failures.
- Summary statistics are correct.
- Download report button works.
- Links to manual steps and service token report work.

---

### Phase 8: Wizard Framework & Welcome Page

**Goal**: Implement the wizard layout, stepper, navigation, and welcome page.

Note: This phase can be partially implemented earlier (during Phase 3) as a scaffold. It's listed here to ensure all wizard behavior is complete and polished.

#### 8.1 Wizard State Management

File: `frontend/src/hooks/useWizard.ts`

```typescript
type WizardStep = {
  id: string;
  label: string;
  path: string;
  isComplete: boolean;
  isAccessible: boolean; // can navigate to this step
};

type WizardState = {
  steps: WizardStep[];
  currentStep: number;
  canGoBack: boolean;
  canGoForward: boolean;
  goToStep: (index: number) => void;
  goNext: () => void;
  goBack: () => void;
  markComplete: (stepId: string) => void;
};
```

Steps:
1. Welcome (`/`)
2. Configuration (`/configure`)
3. Pre-flight (`/preflight`)
4. Data Gathering (`/gathering`)
5. Plan Review (`/plan`)
6. Manual Steps (`/manual-steps`)
7. Migration (`/migrate`)
8. Service Token Report (`/service-tokens`)
9. Completion (`/complete`)

Navigation rules:
- Can only go to completed steps or the current step.
- Cannot skip ahead to incomplete steps.
- Back button always available (except on Welcome and during Migration execution).
- The "Migration" step cannot be navigated back from once started (only forward to Completion).

#### 8.2 Wizard Layout

File: `frontend/src/components/wizard/WizardLayout.tsx`

```
┌──────────────────────────────────────────────────────────────┐
│  Header: Logo/Title                                          │
├──────────────────────────────────────────────────────────────┤
│  Stepper: ● ── ● ── ● ── ○ ── ○ ── ○ ── ○ ── ○ ── ○      │
├──────────────────────────────────────────────────────────────┤
│                                                              │
│  [Page Content — rendered via React Router Outlet]           │
│                                                              │
├──────────────────────────────────────────────────────────────┤
│  Footer: [Back]                                [Next/Action] │
└──────────────────────────────────────────────────────────────┘
```

- Header is simple: app name "Doppler Migration Tool" on the left, maybe a small lock icon for security branding.
- Stepper follows the styleguide design.
- Footer buttons are controlled by the current page (some pages override button text, e.g., "Start Migration", "Approve Plan").

#### 8.3 Welcome Page

File: `frontend/src/pages/WelcomePage.tsx`

Content:
- Display heading: "Doppler Migration Tool"
- Subtitle: "Migrate your Doppler.com workplace to your on-prem Doppler instance."
- What this tool does (bulleted list):
  - Reads all projects, environments, configs, and secrets from your Doppler.com workplace
  - Recreates them on your on-prem Doppler instance
  - Migrates roles, groups, service accounts, webhooks, and trusted IPs
  - Preserves secret references and visibility settings
  - Identifies items requiring manual setup
- Prerequisites (info alert):
  - A Doppler.com CLI or Personal token with read access to all workplace resources
  - Your on-prem Doppler instance URL and a CLI or Personal token with write access
  - Users provisioned on on-prem (via SCIM) if migrating access control
- "Get Started" primary button.

Tests:
- Renders welcome content.
- Get Started button navigates to configuration page.

#### 8.4 SSE Hook

File: `frontend/src/hooks/useSSE.ts`

```typescript
function useSSE(url: string, handlers: Record<string, (data: any) => void>): {
  connected: boolean;
  error: string | null;
  close: () => void;
}
```

- Opens EventSource connection to backend SSE endpoint.
- Dispatches events to typed handlers.
- Auto-reconnects on disconnect.
- Cleans up on unmount.

Tests:
- Connects to SSE endpoint.
- Dispatches events to correct handlers.
- Handles disconnection and reconnection.
- Cleans up on unmount.

---

### Phase 9: Integration Testing & Polish

**Goal**: End-to-end testing, error edge cases, visual polish.

#### 9.1 Integration Tests

Create a mock Doppler API server in `backend/internal/testutil/mockserver.go`:
- Serves realistic responses for all endpoints.
- Configurable: number of projects, environments, configs, secrets.
- Supports pagination.
- Supports rate limit simulation (return 429 after N requests).
- Supports error injection (return 500 for specific endpoints).

Integration tests:
- Full gather → plan → migrate cycle against mock server.
- Verify checkpoint is created and accurate.
- Verify idempotent re-run (second run skips everything).
- Verify partial failure recovery (fail mid-migration, resume, verify completion).
- Verify rate limit handling (429 → wait → retry → succeed).
- Verify dev mode behavior.

#### 9.2 Frontend Integration Tests

Using Vitest with mocked API responses:
- Full wizard flow: Welcome → Configure → Preflight → Gather → Plan → Migrate → Complete.
- Verify page transitions.
- Verify data flows between pages correctly.
- Verify error states are handled on each page.

#### 9.3 Visual Polish

- Verify all pages match the styleguide.
- Test responsive layout at key breakpoints.
- Verify accessibility: keyboard navigation, focus management, ARIA labels.
- Verify loading states and error states on every page.
- Test with large datasets (many projects, many secrets) to verify performance of tree view and tables.

#### 9.4 Docker Final Testing

- Build Docker image.
- Run full migration against mock server from inside Docker.
- Verify checkpoint file persistence with volume mount.
- Verify image size is reasonable (<50MB).

---

## Key Implementation Notes

### Things to Watch Out For

1. **Root configs are auto-created with environments.** When you create an environment, a root config is automatically created. Don't try to create root configs separately — just verify they exist.

2. **Config naming convention.** Branch configs are typically named `<environment>_<branch>` (e.g., `dev_feature1`). The `environment` field on the config tells you which environment it belongs to.

3. **Secret references.** Always use `raw` values when migrating secrets, not `computed`. The `raw` value preserves `${OTHER_SECRET}` references which will resolve correctly on the target once all secrets are migrated.

4. **Secret visibility.** Use the `change_requests` format for secret updates to set visibility (`masked`, `unmasked`, `restricted`). The simple `secrets` format doesn't support visibility.

5. **Secret notes.** Notes must be updated via a separate API call (`POST /v3/configs/config/secrets/note`), not via the main secrets update endpoint.

6. **Locked configs.** Migrate all secrets and trusted IPs before locking. Once locked, the config can't be modified.

7. **Pagination has no total count.** You must paginate until you get fewer results than `per_page`. Always use `per_page=100` for efficiency.

8. **Rate limits differ by operation type.** Secret reads have a separate (often lower) limit than general reads. Writes have the lowest limit. Track them separately.

9. **No List Syncs endpoint.** The syncs list endpoint appears to not exist in the API. This is a known limitation — document it for users.

10. **Integration credentials are redacted.** The API returns integration metadata but not connection credentials. Integrations must be manually reconfigured on on-prem.

11. **User creation impossible via API.** Users can only be listed and updated, not created. The customer will use SCIM for user provisioning.

12. **Service token values are write-once.** Token values are only returned at creation time. Existing token values cannot be retrieved. This is by design for security.

13. **Inheritable flag.** This must be set on configs that should allow other configs to inherit from them. Set it after the config and its secrets are migrated.

14. **The `/v3/me` endpoint** returns token info including type and workplace. Use this for token validation in pre-flight.

### API Endpoints Quick Reference

```
Source (read operations):
GET  /v3/me                                          → Token info
GET  /v3/workplace                                   → Workplace settings
GET  /v3/workplace/roles                             → Workplace roles
GET  /v3/workplace/groups                            → Groups
GET  /v3/workplace/groups/{slug}                     → Group detail
GET  /v3/workplace/users                             → Users
GET  /v3/workplace/service_accounts                  → Service accounts
GET  /v3/projects                                    → Projects (paginated)
GET  /v3/environments?project=X                      → Environments
GET  /v3/configs?project=X                           → Configs (paginated)
GET  /v3/configs/config/secrets?project=X&config=Y   → Secrets
GET  /v3/configs/config/trusted_ips?project=X&config=Y → Trusted IPs
GET  /v3/configs/config/tokens?project=X&config=Y    → Service tokens
GET  /v3/projects/roles                              → Project roles
GET  /v3/projects/project/members?project=X          → Project members
GET  /v3/integrations                                → Integrations
GET  /v3/webhooks?project=X                          → Webhooks

Target (write operations):
POST  /v3/workplace                                  → Update workplace
POST  /v3/workplace/roles                            → Create workplace role
POST  /v3/workplace/groups                           → Create group
POST  /v3/workplace/groups/{slug}/members            → Add group member
POST  /v3/workplace/service_accounts                 → Create service account
POST  /v3/projects                                   → Create project
POST  /v3/environments?project=X                     → Create environment
POST  /v3/configs                                    → Create config
POST  /v3/configs/config/secrets                     → Update secrets
POST  /v3/configs/config/secrets/note                → Update secret note
POST  /v3/configs/config/trusted_ips                 → Add trusted IP
POST  /v3/configs/config/lock                        → Lock config
POST  /v3/configs/config/inheritable                 → Set inheritable
POST  /v3/projects/roles                             → Create project role
POST  /v3/projects/project/members?project=X         → Add project member
POST  /v3/webhooks                                   → Create webhook
```

### Testing Patterns

**Go tests**: Use table-driven tests. Mock the Doppler API with `httptest.NewServer`. Use interfaces for the Doppler client so the executor/gatherer can be tested with mock implementations.

**React tests**: Use React Testing Library with Vitest. Mock API calls with `vi.fn()`. Test user interactions (click, type, submit). Test component rendering with different props/states. Use `@testing-library/jest-dom` for DOM assertions.

**SSE tests**: Mock the EventSource API in frontend tests. Use `httptest` for SSE endpoint tests in Go.
