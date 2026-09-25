# Doppler On-Prem Migrator — Build Prompt

You are building the Doppler On-Prem Migrator from scratch. This is a one-shot migration tool that moves an entire Doppler.com workplace to a self-hosted (on-prem) Doppler instance via the Doppler REST API (https://docs.doppler.com/reference/api). The on-prem API is identical to Doppler.com but uses a customer-specified host URL.

## Required Reading

Before writing any code, read these three documents in order. They contain the full architecture, visual design, and step-by-step build plan:

1. **`docs/technical-design.md`** — Architecture, API client design, migration resource matrix, dependency-ordered migration phases, checkpoint system, security model, Docker config, error handling. This is the source of truth for all backend design decisions.

2. **`docs/styleguide.md`** — Complete visual design system inspired by Doppler's brand. Color palette (Doppler purple #6B13F5 primary), Inter font, component specs (buttons, cards, inputs, badges, stepper, tree view, alerts, progress bars), page layouts, iconography (Lucide React), animation guidelines, accessibility requirements. This is the source of truth for all frontend design decisions.

3. **`docs/implementation-plan.md`** — The detailed 9-phase build plan with exact file paths, Go struct definitions, function signatures, test specifications, and frontend component descriptions. **Follow this plan phase by phase.** Each phase must be fully complete (tests passing) before moving to the next.

## Tech Stack

- **Backend**: Go 1.25+ (standard library `net/http` with enhanced routing, no web framework)
- **Frontend**: React 18 + TypeScript + Vite + Tailwind CSS 3 + Lucide React icons
- **Testing**: Go `testing` + `httptest` (backend), Vitest + React Testing Library (frontend)
- **Progress**: Server-Sent Events (SSE) for real-time updates
- **Docker**: Multi-stage build → distroless image (~25MB)
- **Fonts**: Inter (Google Fonts), JetBrains Mono for monospace

## Key Design Decisions (Already Made)

These are settled — do not re-ask or deviate:

1. **Secrets never touch disk.** All secret data is in-memory only. Cleared per-config after migration.
2. **Checkpoints persist to disk** as JSON (metadata only — resource IDs, statuses, errors — never secret values). Default location: `./data/migration-checkpoint.json`.
3. **Idempotent re-runs.** Before creating any resource on the target, check if it already exists (by name/slug). If it does, skip it. For secrets, if the secret exists but has a different value, update it (source is authoritative).
4. **Rate limiting.** Track via `x-ratelimit-remaining`, `x-ratelimit-limit`, `x-ratelimit-reset` response headers. Proactive slowdown at <10% remaining. On 429, wait per `retry-after`. Separate tracking for reads, secret-reads, and writes.
5. **Dev mode.** Togglable via the UI. Bypasses all target pre-flight checks and write operations. Exists because there's no on-prem instance to test against yet.
6. **Users can't be created via API.** Customer uses SCIM. The app checks if required users exist on the target before the access control migration phase. Missing users cause those access assignments to be skipped (not the whole migration).
7. **Service tokens can't be migrated** (values aren't readable). A dedicated report page lists all tokens with metadata and tells the user what to do.
8. **Integrations can't be auto-migrated** (credentials are redacted). Listed in manual steps with instructions.
9. **Secrets syncs can't be listed via API** (no List endpoint). Called out in manual steps.
10. **Migration uses `raw` secret values** (not `computed`) to preserve `${REFERENCE}` syntax.

## How to Work

### Process for each implementation phase:

1. **Write tests first.** For Go, use table-driven tests with `httptest` mock servers. For React, use Vitest + React Testing Library. Tests define the expected behavior.
2. **Implement to make tests pass.** Follow the function signatures and struct definitions in the implementation plan.
3. **Run all tests** (both backend and frontend) to verify nothing is broken.
4. **Commit** with a descriptive message.

### Engineering preferences to follow:

- **DRY**: Flag and eliminate repetition aggressively.
- **Well-tested**: More tests is better than fewer. Cover edge cases, error paths, and boundary conditions.
- **"Engineered enough"**: Not under-engineered (fragile) and not over-engineered (premature abstraction). Find the middle.
- **Handle edge cases**: Be thorough — empty results, pagination boundaries, rate limit exhaustion, partial failures, context cancellation, invalid inputs.
- **Explicit over clever**: Readable, straightforward code. No magic.
- **Security**: Never log tokens or secrets. Never persist secrets to disk. Validate all inputs. Use HTTPS for all external calls.

### Build order (from implementation plan):

```
Phase 1: Project Scaffolding
  → Go backend structure, React frontend structure, Docker, Makefile, dev scripts
  → Stub all API routes and pages

Phase 2: Doppler API Client
  → Core HTTP client with rate limiting, retry, pagination
  → All resource-specific operations (workplace, projects, environments, configs, secrets, roles, groups, etc.)

Phase 3: Pre-flight & Configuration
  → Configuration state management
  → Pre-flight checker (7 checks, dev mode skips target checks)
  → Configuration page UI (source token, target host+token, dev mode toggle)
  → Pre-flight page UI (animated checklist via SSE)

Phase 4: Data Gathering
  → Gatherer engine (reads all resources from source)
  → Progress streaming via SSE
  → Gathering page UI (per-resource progress, rate limit indicator)

Phase 5: Migration Plan & Manual Steps
  → Plan generator (structured summary from gathered data)
  → User existence check against target
  → Plan review page UI (tree view, summary stats, manual steps warning)
  → Manual steps page UI (integrations, syncs, users)

Phase 6: Migration Execution
  → Checkpoint system (save/load, item status tracking, concurrent-safe)
  → Executor (8-phase dependency-ordered migration with idempotency)
  → Migration page UI (phase progress, per-item status, pause/resume)

Phase 7: Reports & Completion
  → Service token report (table with all tokens by project/config)
  → Migration summary (stats, failures, remaining manual steps)
  → Completion page UI (success/warning state, download report)

Phase 8: Wizard Framework & Welcome Page
  → Wizard state management (step tracking, navigation rules)
  → Wizard layout (header, stepper, content area, footer nav)
  → Welcome page (intro, prerequisites, get started)
  → SSE hook

Phase 9: Integration Testing & Polish
  → Mock Doppler API server for integration tests
  → Full gather→plan→migrate cycle tests
  → Idempotent re-run tests
  → Rate limit handling tests
  → Docker build and test
```

Note: Phase 8 (wizard framework) should be scaffolded early in Phase 1 — the full implementation plan describes building it as part of Phase 1 scaffolding and then completing it in Phase 8. Use your judgment on when the wizard layout/stepper are needed vs when stubs suffice.

## Migration Resource Reference

**Auto-migrated** (8 phases, dependency order):
1. Workplace settings, custom workplace roles, custom project roles
2. Groups (structure only), service accounts
3. Projects, environments
4. Configs (root auto-created with environments; then branch configs)
5. Secrets (raw values + visibility + notes), trusted IPs, config lock status
6. Config inheritance relationships
7. Webhooks, change request policies
8. Group members, project members (requires users to exist on target)

**Manual steps** (called out in UI):
- Integrations (credentials redacted — manual recreation)
- Secrets syncs (no List API — document from dashboard)
- Service tokens (values not readable — create new on on-prem)
- Service account tokens (same as service tokens)
- Users (no Create API — provision via SCIM)

**Not migrated** (by design):
- Activity logs, config logs (historical, read-only)
- Dynamic secrets (ephemeral leases)
- Invites (list-only API)

## Doppler API Quick Reference

Base URL: `https://api.doppler.com/v3/` (source) or `https://<host>/v3/` (target)
Auth: `Authorization: Bearer <token>` (every request)
Pagination: `?page=N&per_page=100`, iterate until results < per_page
Token prefixes: `dp.ct.` (CLI), `dp.pt.` (Personal), `dp.st.` (Service), `dp.sa.` (Service Account)

Key endpoints are listed in the implementation plan under "API Endpoints Quick Reference". Consult the Doppler API docs at https://docs.doppler.com/reference/api for any details not covered.

## Important Gotchas

1. Root configs are auto-created when environments are created. Don't create them manually — verify they exist.
2. Secret notes use a separate endpoint (`POST /v3/configs/config/secrets/note`), not the main secrets update.
3. Use `change_requests` format for secret updates to preserve visibility settings. The simple `secrets` format doesn't support visibility.
4. Lock configs only after all their secrets and trusted IPs are migrated.
5. Pagination responses have no `total_count`. Paginate until result count < per_page.
6. Rate limits differ by operation type: reads, secret reads, and writes each have separate limits.
7. The `inheritable` flag must be set on configs after their secrets are migrated.
8. `GET /v3/me` returns token info — use for pre-flight validation.
9. Group/project member assignment requires the referenced users/groups/service accounts to already exist on the target.
10. Secret `raw` values preserve `${VARIABLE}` references; `computed` values have them resolved. Always migrate `raw`.
