# Doppler On-Prem Migrator — Technical Design Document

## 1. Overview

A one-shot migration tool that moves an entire Doppler.com workplace to a self-hosted (on-prem) Doppler instance. The tool provides a wizard-style web UI that guides the user through configuration, pre-flight checks, data gathering, plan review, migration execution, and post-migration reporting.

### Core Principles

- **Security first**: Tokens and secrets are never persisted to disk. All sensitive data lives in-memory only.
- **Idempotent & resumable**: Safe to re-run without wiping the target instance. Checkpoints track migration progress (metadata only, no secrets).
- **Rate-limit aware**: Respects Doppler API rate limits using response headers (`x-ratelimit-remaining`, `x-ratelimit-reset`, `retry-after`).
- **Transparent**: Every action is explained to the user. Resources that can't be fully auto-migrated are called out with manual steps.

---

## 2. Tech Stack

| Layer | Technology | Rationale |
|-------|-----------|-----------|
| Backend | **Go 1.25+** | Single binary, minimal attack surface, excellent HTTP/concurrency, tiny Docker image, no runtime dependencies |
| Frontend | **React 18 + TypeScript + Vite** | Fast builds, small bundle, strong typing, excellent wizard UI support |
| Styling | **Tailwind CSS 3** | Utility-first matches Doppler's style, easy to maintain, small production CSS |
| Progress Updates | **Server-Sent Events (SSE)** | Simpler than WebSockets, sufficient for one-way server→client progress updates |
| Testing (Go) | **Standard `testing` + `httptest`** | No external test dependencies needed |
| Testing (Frontend) | **Vitest + React Testing Library** | Fast, Vite-native test runner |
| Container | **Multi-stage Docker build** | Stage 1: Node builds frontend → Stage 2: Go builds backend with embedded frontend → Stage 3: Scratch/distroless for minimal image (~25MB) |

### Project Structure

```
doppler-onprem-migrator/
├── CLAUDE.md
├── Dockerfile
├── docker-compose.yml
├── docs/
│   ├── technical-design.md      # This file
│   ├── styleguide.md
│   └── implementation-plan.md
├── backend/
│   ├── go.mod
│   ├── go.sum
│   ├── main.go                  # Entry point, embeds frontend, starts server
│   ├── cmd/
│   │   └── server/
│   │       └── main.go
│   ├── internal/
│   │   ├── api/                 # HTTP handlers for frontend
│   │   │   ├── handlers.go
│   │   │   ├── middleware.go
│   │   │   ├── routes.go
│   │   │   └── sse.go           # SSE event streaming
│   │   ├── doppler/             # Doppler API client
│   │   │   ├── client.go        # HTTP client with rate limiting
│   │   │   ├── client_test.go
│   │   │   ├── types.go         # API request/response types
│   │   │   ├── projects.go      # Project CRUD operations
│   │   │   ├── environments.go
│   │   │   ├── configs.go
│   │   │   ├── secrets.go
│   │   │   ├── workplace.go
│   │   │   ├── roles.go
│   │   │   ├── groups.go
│   │   │   ├── integrations.go
│   │   │   ├── service_accounts.go
│   │   │   ├── service_tokens.go
│   │   │   ├── trusted_ips.go
│   │   │   ├── webhooks.go
│   │   │   └── ratelimit.go     # Rate limit tracking and backoff
│   │   ├── migration/           # Migration orchestration
│   │   │   ├── engine.go        # Core migration engine
│   │   │   ├── engine_test.go
│   │   │   ├── checkpoint.go    # Checkpoint state management
│   │   │   ├── checkpoint_test.go
│   │   │   ├── gatherer.go      # Data gathering from source
│   │   │   ├── gatherer_test.go
│   │   │   ├── planner.go       # Migration plan generation
│   │   │   ├── planner_test.go
│   │   │   ├── executor.go      # Migration execution
│   │   │   ├── executor_test.go
│   │   │   └── types.go         # Migration-specific types
│   │   └── preflight/           # Pre-flight checks
│   │       ├── checker.go
│   │       └── checker_test.go
│   └── embed.go                 # Frontend embedding directive
├── frontend/
│   ├── package.json
│   ├── tsconfig.json
│   ├── vite.config.ts
│   ├── tailwind.config.ts
│   ├── index.html
│   ├── src/
│   │   ├── main.tsx
│   │   ├── App.tsx              # Wizard router
│   │   ├── components/
│   │   │   ├── wizard/          # Wizard framework
│   │   │   │   ├── WizardLayout.tsx
│   │   │   │   ├── WizardStepper.tsx
│   │   │   │   ├── WizardPage.tsx
│   │   │   │   └── WizardNav.tsx
│   │   │   ├── ui/              # Shared UI components
│   │   │   │   ├── Button.tsx
│   │   │   │   ├── Card.tsx
│   │   │   │   ├── Input.tsx
│   │   │   │   ├── Badge.tsx
│   │   │   │   ├── Alert.tsx
│   │   │   │   ├── Progress.tsx
│   │   │   │   ├── Spinner.tsx
│   │   │   │   ├── TreeView.tsx
│   │   │   │   └── StatusIcon.tsx
│   │   │   └── migration/       # Migration-specific components
│   │   │       ├── PlanTree.tsx
│   │   │       ├── ProgressPanel.tsx
│   │   │       ├── ManualSteps.tsx
│   │   │       ├── ServiceTokenReport.tsx
│   │   │       └── RateLimitIndicator.tsx
│   │   ├── pages/
│   │   │   ├── WelcomePage.tsx
│   │   │   ├── ConfigurationPage.tsx
│   │   │   ├── PreflightPage.tsx
│   │   │   ├── GatheringPage.tsx
│   │   │   ├── PlanReviewPage.tsx
│   │   │   ├── ManualStepsPage.tsx
│   │   │   ├── MigrationPage.tsx
│   │   │   ├── ServiceTokenReportPage.tsx
│   │   │   └── CompletionPage.tsx
│   │   ├── hooks/
│   │   │   ├── useSSE.ts        # SSE connection hook
│   │   │   ├── useWizard.ts     # Wizard state management
│   │   │   └── useMigration.ts  # Migration API calls
│   │   ├── api/
│   │   │   └── client.ts        # Backend API client
│   │   ├── types/
│   │   │   └── migration.ts     # Shared type definitions
│   │   └── styles/
│   │       └── index.css        # Tailwind imports + custom styles
│   └── tests/
│       ├── setup.ts
│       ├── components/
│       ├── pages/
│       └── hooks/
└── scripts/
    ├── dev.sh                   # Local development runner
    └── build.sh                 # Production build script
```

---

## 3. Doppler API Client

### Authentication

All requests use Bearer token auth:
```
Authorization: Bearer <token>
```

Two tokens are required:
- **Source token**: `dp.ct.*` or `dp.pt.*` (CLI or Personal token) with read access to all workplace resources on Doppler.com
- **Target token**: `dp.ct.*` or `dp.pt.*` with full read/write access on the on-prem instance

### Rate Limiting Strategy

Doppler returns rate limit headers on every response:

| Header | Description |
|--------|-------------|
| `x-ratelimit-limit` | Max requests for this window |
| `x-ratelimit-remaining` | Requests remaining |
| `x-ratelimit-reset` | Unix timestamp when window resets |
| `retry-after` | Seconds to wait (only on 429) |

**Strategy:**
1. Track remaining requests from response headers after every call.
2. When `x-ratelimit-remaining` drops below a threshold (10% of limit), proactively slow down with progressive delays.
3. On HTTP 429, wait for `retry-after` seconds (or until `x-ratelimit-reset` timestamp), then retry.
4. Separate tracking for read vs write vs secret-read limits.
5. Surface rate limit status to the frontend via SSE so users see current throughput.

```go
type RateLimiter struct {
    mu            sync.Mutex
    remaining     int
    limit         int
    resetAt       time.Time
    lastResponse  time.Time
}

func (rl *RateLimiter) Wait(ctx context.Context) error {
    rl.mu.Lock()
    defer rl.mu.Unlock()

    if rl.remaining <= 0 && time.Now().Before(rl.resetAt) {
        waitDuration := time.Until(rl.resetAt) + 500*time.Millisecond // small buffer
        select {
        case <-time.After(waitDuration):
        case <-ctx.Done():
            return ctx.Err()
        }
    }

    // Proactive slowdown when approaching limit
    if rl.limit > 0 && rl.remaining < rl.limit/10 {
        time.Sleep(200 * time.Millisecond)
    }

    return nil
}

func (rl *RateLimiter) Update(headers http.Header) {
    // Parse x-ratelimit-remaining, x-ratelimit-limit, x-ratelimit-reset
}
```

### Pagination

All list endpoints use page-based pagination:
- Request: `?page=N&per_page=100` (max 100 per page for efficiency)
- No total count in response — paginate until result count < per_page or empty array

```go
func (c *Client) PaginateAll(ctx context.Context, endpoint string, params url.Values) ([]json.RawMessage, error) {
    var all []json.RawMessage
    page := 1
    perPage := 100
    for {
        params.Set("page", strconv.Itoa(page))
        params.Set("per_page", strconv.Itoa(perPage))
        result, err := c.Get(ctx, endpoint, params)
        if err != nil {
            return nil, err
        }
        items := extractItems(result)
        all = append(all, items...)
        if len(items) < perPage {
            break
        }
        page++
    }
    return all, nil
}
```

---

## 4. Migration Resources

### Full Auto-Migration (read from source, create on target)

| Resource | Scope | API Read | API Write | Notes |
|----------|-------|----------|-----------|-------|
| Workplace Settings | Workplace | GET /v3/workplace | POST /v3/workplace | Name, billing_email, security_email |
| Custom Workplace Roles | Workplace | GET /v3/workplace/roles | POST /v3/workplace/roles | Filter to `is_custom_role: true` only |
| Custom Project Roles | Workplace | GET /v3/projects/roles | POST /v3/projects/roles | Filter to `is_custom_role: true` only |
| Groups | Workplace | GET /v3/workplace/groups | POST /v3/workplace/groups | Create groups; member assignment depends on users existing |
| Projects | Workplace | GET /v3/projects | POST /v3/projects | Name and description |
| Environments | Project | GET /v3/environments | POST /v3/environments | Name, slug; per project |
| Configs | Project | GET /v3/configs | POST /v3/configs | Root and branch configs; lock status; inheritance |
| Secrets | Config | GET /v3/configs/config/secrets | POST /v3/configs/config/secrets | Use `raw` values to preserve `${REFERENCE}` syntax; include notes and visibility |
| Trusted IPs | Config | GET /v3/configs/config/trusted_ips | POST /v3/configs/config/trusted_ips | CIDR notation |
| Webhooks | Project | GET /v3/webhooks | POST /v3/webhooks | URL, enabled status; note URLs may need updating |
| Service Accounts | Workplace | GET /v3/workplace/service_accounts | POST /v3/workplace/service_accounts | Name; tokens are separate (see below) |
| Project Members | Project | GET /v3/projects/project/members | POST /v3/projects/project/members | Role, environments; requires users/groups to exist on target |
| Group Members | Group | GET per group | POST per group | Requires users to exist on target |
| Change Request Policies | Workplace | GET /v3/workplace/change_request_policies | POST /v3/workplace/change_request_policies | Rules and target manifest; reviewer slugs are remapped per instance |

### Partial Migration (manual steps required)

| Resource | Issue | Manual Step |
|----------|-------|-------------|
| **Integrations** | Credentials/connection data is redacted in API response. On-prem needs fresh credentials for external services (AWS, GCP, etc.) | List all integrations with type and name. User must recreate with new credentials on on-prem. |
| **Secrets Syncs** | No List endpoint exists. Syncs depend on integrations which can't be auto-migrated. | Advise user to document all syncs from Doppler.com dashboard before migration. Recreate after integrations are set up. |
| **Service Tokens** | Token values are only returned at creation time. Cannot read existing token values. | Generate a report listing all service tokens by config with name, creation date, and expiry. User must create new tokens on on-prem and update deployments. |
| **Service Account Tokens** | Same as service tokens — values not retrievable. | Include in the service token report. |
| **Users** | No Create User API endpoint. Users are provisioned via SCIM or dashboard invites. | Pre-flight check verifies required users exist on target. User sets up SCIM or manually invites users before migration. |

### Not Migrated

| Resource | Reason |
|----------|--------|
| Activity Logs | Historical audit data; read-only API; not meaningful on new instance |
| Config Logs | Historical change log; not meaningful on new instance |
| Dynamic Secrets | Temporary credential leases; ephemeral by design |
| Invites | List-only API; users should be provisioned via SCIM on on-prem |

---

## 5. Migration Order (Dependency Graph)

Resources must be migrated in dependency order. Resources at the same level can be parallelized.

```
Phase 1: Workplace Foundation
├── 1.1 Workplace Settings
├── 1.2 Custom Workplace Roles
└── 1.3 Custom Project Roles

Phase 2: Groups & Service Accounts
├── 2.1 Groups (create groups, skip member assignment)
└── 2.2 Service Accounts

Phase 3: Projects & Environments
├── 3.1 Projects
└── 3.2 Environments (per project, after project exists)

Phase 4: Configs
├── 4.1 Root Configs (auto-created with environments, verify they exist)
└── 4.2 Branch Configs (depends on root configs existing)

Phase 5: Config Data
├── 5.1 Secrets (per config; use raw values)
├── 5.2 Trusted IPs (per config)
└── 5.3 Config Lock Status (lock configs that were locked on source)

Phase 6: Config Relationships
└── 6.1 Config Inheritance (set inheritable flag, link inheritance chains)

Phase 7: Project Metadata
├── 7.1 Webhooks (per project)
└── 7.2 Change Request Policies

Phase 8: Access Control (requires users to exist on target)
├── 8.1 Group Members (assign users to groups)
└── 8.2 Project Members (assign users/groups/service accounts to projects)
```

### User Existence Check

Before Phase 8, the tool performs a user existence check:
1. Gather all unique user emails/IDs referenced in source group memberships and project members.
2. List users on target instance via `GET /v3/workplace/users`.
3. Compare: identify missing users.
4. If any users are missing:
   - Show a warning with the list of missing users.
   - Allow the user to proceed (skip access control migration for missing users) or abort.
   - Access control items referencing missing users are marked as "skipped" in the checkpoint with the reason.

---

## 6. Checkpoint System

### Design

Checkpoints track migration progress without storing any secret values. They are persisted to a JSON file on disk that can survive process restarts.

```go
type Checkpoint struct {
    Version       int                    `json:"version"`
    StartedAt     time.Time              `json:"started_at"`
    UpdatedAt     time.Time              `json:"updated_at"`
    SourceHost    string                 `json:"source_host"`
    TargetHost    string                 `json:"target_host"`
    CurrentPhase  int                    `json:"current_phase"`
    Resources     map[string]*ResourceCheckpoint `json:"resources"`
}

type ResourceCheckpoint struct {
    Status  ResourceStatus           `json:"status"`  // pending, in_progress, completed, failed
    Items   map[string]*ItemStatus   `json:"items"`
    Error   string                   `json:"error,omitempty"`
}

type ItemStatus struct {
    Status    ResourceStatus `json:"status"`
    SourceID  string         `json:"source_id,omitempty"`
    TargetID  string         `json:"target_id,omitempty"`
    Error     string         `json:"error,omitempty"`
    Skipped   bool           `json:"skipped,omitempty"`
    SkipReason string        `json:"skip_reason,omitempty"`
    Timestamp time.Time      `json:"timestamp"`
}

type ResourceStatus string

const (
    StatusPending    ResourceStatus = "pending"
    StatusInProgress ResourceStatus = "in_progress"
    StatusCompleted  ResourceStatus = "completed"
    StatusFailed     ResourceStatus = "failed"
    StatusSkipped    ResourceStatus = "skipped"
)
```

### Checkpoint File Location

`migration-checkpoint.json` inside `CHECKPOINT_DIR`, which defaults to `./data`
(mountable Docker volume; the image sets `CHECKPOINT_DIR=/data`). The directory is
created on first write.

### Idempotency Rules

When the tool encounters a resource during migration:

1. **Check checkpoint**: If the item is marked `completed` or `skipped`, skip it.
2. **Check target**: If the item exists on the target (by name/slug), mark as `completed` in checkpoint and skip.
3. **Create**: If the item doesn't exist on target, create it. On success, mark `completed`. On failure, mark `failed` with error.
4. **Secrets special case**: For secrets, compare raw values. If a secret exists on target with a different value, update it (the source is authoritative).

---

## 7. Backend API Endpoints

All endpoints are prefixed with `/api/v1/`.

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/v1/configure` | Set source token, target host, target token, dev mode flag |
| POST | `/api/v1/preflight` | Run pre-flight checks; returns results |
| POST | `/api/v1/gather` | Start data gathering from source |
| GET | `/api/v1/plan` | Get the migration plan after gathering |
| POST | `/api/v1/migrate` | Start migration execution |
| POST | `/api/v1/migrate/pause` | Pause migration |
| POST | `/api/v1/migrate/resume` | Resume migration from checkpoint |
| GET | `/api/v1/status` | Get current migration status |
| GET | `/api/v1/checkpoint` | Get checkpoint data |
| GET | `/api/v1/report/service-tokens` | Get service token report |
| GET | `/api/v1/report/manual-steps` | Get manual steps report |
| GET | `/api/v1/report/summary` | Get migration summary |
| GET | `/api/v1/events` | SSE endpoint for real-time progress |

### SSE Event Types

```typescript
type SSEEvent =
  | { type: "preflight_check";  data: { check: string; status: "running" | "passed" | "failed" | "warning"; message?: string } }
  | { type: "gather_progress";  data: { resource: string; current: number; total?: number; message: string } }
  | { type: "rate_limit";       data: { remaining: number; limit: number; resetAt: string; waiting: boolean } }
  | { type: "migrate_progress"; data: { phase: number; resource: string; item: string; status: string; current: number; total: number } }
  | { type: "migrate_error";    data: { resource: string; item: string; error: string; retrying: boolean } }
  | { type: "phase_complete";   data: { phase: number; name: string } }
  | { type: "migration_complete"; data: { summary: MigrationSummary } }
```

---

## 8. Security Considerations

### Token Handling
- Tokens are accepted via the `/api/v1/configure` endpoint and stored in-memory only.
- Tokens are never written to logs, checkpoint files, or any disk storage.
- Tokens are zeroed from memory when the migration session ends or the process exits.

### Network Security
- The app binds to `localhost` by default (configurable for Docker).
- No external network calls except to the source (api.doppler.com) and target (user-specified) Doppler APIs.
- All Doppler API calls use HTTPS.

### Secret Data
- Secret values are fetched from source and sent to target via HTTPS API calls.
- Secret values are held in memory only for the duration of a config's secret migration.
- After a config's secrets are migrated, the in-memory secret data for that config is cleared.
- Checkpoint files contain only resource identifiers and status — never secret values.

### Frontend
- No sensitive data is sent to the browser beyond what's needed for display (e.g., secret counts, not values).
- The frontend never receives or displays actual secret values.
- CSRF protection on all state-mutating endpoints.

---

## 9. Docker Configuration

### Multi-Stage Build

```dockerfile
# Stage 1: Build frontend
FROM node:24.14-alpine AS frontend-builder
ARG PNPM_VERSION=11.22.0
WORKDIR /app/frontend
RUN npm install -g pnpm@$PNPM_VERSION
COPY frontend/package.json frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile
COPY frontend/ ./
RUN pnpm run build

# Stage 2: Build backend
FROM golang:1.25-alpine AS backend-builder
WORKDIR /app
COPY backend/ ./backend/
COPY --from=frontend-builder /app/frontend/dist ./backend/webui
WORKDIR /app/backend
RUN CGO_ENABLED=0 GOOS=linux go build -o /migrator ./cmd/server

# Stage 3: Runtime
FROM gcr.io/distroless/static-debian12
COPY --from=backend-builder /migrator /migrator
EXPOSE 8080
ENTRYPOINT ["/migrator"]
```

### Docker Compose (Development)

```yaml
version: "3.8"
services:
  migrator:
    build: .
    ports:
      - "8080:8080"
    environment:
      - DEV_MODE=true
    volumes:
      - ./data:/data  # For checkpoint files
```

### Runtime

- Single port: `8080` (serves both API and frontend static files)
- No database, no external dependencies
- Checkpoint file stored in `/data/migration-checkpoint.json` (mountable volume)
- Environment variables:
  - `PORT` — Server port (default: 8080)
  - `HOST` — Bind address (default: 0.0.0.0 in Docker, 127.0.0.1 locally)
  - `DEV_MODE` — Enable dev mode (bypasses target pre-flight checks)
  - `CHECKPOINT_DIR` — Directory for checkpoint files (default: ./data)

---

## 10. Error Handling

### Retry Strategy

| Error Type | Action |
|-----------|--------|
| HTTP 429 (Rate Limited) | Wait per `retry-after` header, then retry |
| HTTP 5xx (Server Error) | Retry up to 3 times with exponential backoff (1s, 2s, 4s) |
| HTTP 4xx (Client Error) | Do not retry; mark item as failed in checkpoint |
| Network Error | Retry up to 3 times with exponential backoff |
| Context Cancelled | Stop immediately; save checkpoint |

### Error Reporting

- Individual resource failures don't halt the migration.
- Failed items are recorded in the checkpoint with error details.
- The migration summary shows all failures with suggested remediation.
- Critical errors (e.g., source/target unreachable, invalid token) halt the migration and prompt user action.
