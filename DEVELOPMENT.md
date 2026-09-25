# Development Guide

This guide covers everything you need to set up a local development environment, run the application, and execute the test suite.

## Prerequisites

| Tool | Version | Purpose |
|------|---------|---------|
| **Go** | 1.25+ | Backend compilation and testing |
| **Node.js** | 24.14+ | Frontend build and testing |
| **pnpm** | 11.22.0 | Frontend dependency management |
| **Docker** | 24+ | Container builds (optional) |

Node and pnpm match the versions used in the main Doppler `server` repo. The Node
version is pinned in `.nvmrc`; pnpm is pinned by the `packageManager` field in
`frontend/package.json`.

Verify your setup:

```bash
go version      # go1.25.0 or later
node --version  # v24.14 or later
pnpm --version  # 11.22.0
```

If you use `nvm`, run `nvm use` in the repo root to pick up `.nvmrc`. If pnpm isn't
installed: `npm install -g pnpm@11.22.0`.

## Project Structure

```
doppler-onprem-migrator/
├── backend/                  # Go backend (API server)
│   ├── cmd/server/main.go    # Entry point
│   ├── embed.go              # Frontend static file embedding
│   ├── webui/                # //go:embed target — built frontend is copied here
│   └── internal/
│       ├── api/              # HTTP handlers, routes, middleware, SSE
│       ├── doppler/          # Doppler API client with rate limiting
│       ├── migration/        # Gather, plan, execute, checkpoint
│       └── preflight/        # Pre-flight validation checks
├── frontend/                 # React + TypeScript + Vite
│   ├── src/
│   │   ├── pages/            # 9 wizard step pages
│   │   ├── components/       # UI + wizard + migration components
│   │   ├── hooks/            # useSSE, useWizard
│   │   ├── api/client.ts     # Backend API client
│   │   └── types/            # Shared TypeScript types
│   ├── tests/                # Vitest test suite
│   ├── pnpm-lock.yaml        # Dependency lockfile
│   └── pnpm-workspace.yaml   # pnpm build-script allowlist
├── scripts/
│   ├── dev.sh                # Start dev environment
│   └── build.sh              # Production build
├── .nvmrc                    # Node version pin (matches server/)
├── Makefile                  # Build orchestration
├── Dockerfile                # Multi-stage container build
└── docker-compose.yml        # Docker Compose config
```

## Getting Started

Everything routine is a Make target. Run `make` (or `make help`) for the full list
with descriptions:

```
Development
  dev            Start the Vite dev server (:5173) and the Go backend (:8080) together

Build & Run
  build          Build the frontend and compile the self-contained binary to backend/server
  run            Run the compiled binary on http://127.0.0.1:8080 (run `make build` first)

Testing
  test           Run the backend and frontend test suites
  test-backend   Run the Go test suite
  test-frontend  Run the Vitest suite

Docker
  docker-build   Build the distroless container image as doppler-migrator
  docker-run     Start the container via Docker Compose on :8080

Housekeeping
  clean          Remove the binary, frontend build output, embedded assets, and node_modules
```

New targets should carry a `## description` comment so they appear there.

### 1. Install Dependencies

```bash
# Frontend dependencies
cd frontend && pnpm install --frozen-lockfile && cd ..
```

The backend uses only the Go standard library — no `go install` or dependency fetch needed beyond having Go itself.

### 2. Start Development Servers

The fastest way to start both servers:

```bash
make dev
```

This runs `scripts/dev.sh`, which starts:

- **Frontend dev server** at `http://localhost:5173` (Vite with hot module reload)
- **Backend API server** at `http://localhost:8080` (Go with `DEV_MODE=true`)

The Vite dev server proxies all `/api/*` requests to the Go backend, so you interact with the app at `http://localhost:5173`.

### 3. Start Servers Individually

If you prefer running them in separate terminals:

**Terminal 1 — Backend:**
```bash
cd backend
DEV_MODE=true go run ./cmd/server
```

**Terminal 2 — Frontend:**
```bash
cd frontend
pnpm run dev
```

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `HOST` | `127.0.0.1` | Bind address for the backend server |
| `ALLOWED_HOSTS` | — | Extra comma-separated hosts the origin guard accepts, beyond loopback |
| `PORT` | `8080` | Port for the backend server |
| `DEV_MODE` | `false` | When `true`, bypasses target pre-flight checks and write operations |
| `CHECKPOINT_DIR` | `./data` | Directory for checkpoint file storage |

`DEV_MODE=true` is set by `make dev`, which lets you run the full wizard flow without a
real on-prem Doppler instance. When it is set the server pins the session to a dry run:
the Dev Mode checkbox is locked on, the Target fields on the Configuration step are
disabled, and nothing is written to the target.

**That is why the Target form does nothing under `make dev`.** To configure a real
target and run an actual migration, start the server without `DEV_MODE`:

```bash
make build && make run
```

Or run the backend directly with the frontend dev server, omitting `DEV_MODE`:

```bash
cd backend && go run ./cmd/server     # terminal 1
cd frontend && pnpm run dev           # terminal 2
```

`docker-compose.yml` deliberately does **not** set it — the README offers
`docker compose up` as a way to run a real migration, and a run under `DEV_MODE` would
report success having written nothing. For a containerized dry run, layer the override:

```bash
docker compose -f docker-compose.yml -f docker-compose.dev.yml up
```

## Dependency Supply Chain

`frontend/pnpm-workspace.yaml` carries the supply-chain policy, mirroring
`server/pnpm-workspace.yaml` so both repos apply the same rules. The Dockerfile
copies it, so container builds are covered too.

| Setting | Effect |
|---------|--------|
| `allowBuilds` | Allowlist of packages permitted to run install scripts — pnpm blocks all of them by default. Entries are pinned to an exact version so a later compromised release doesn't inherit the permission. |
| `strictDepBuilds: true` | A package wanting to run a script while not in `allowBuilds` fails the install instead of being skipped with a warning. |
| `minimumReleaseAge: 4320` | Won't adopt a version published less than 3 days ago. Compromised-maintainer and worm releases are typically pulled within hours. |
| `minimumReleaseAgeStrict: true` | Won't silently write an exclusion to bypass the age gate. |
| `trustPolicy: no-downgrade` | Refuses a resolution that moves a package backwards — the shape of a downgrade attack. |
| `blockExoticSubdeps: true` | Refuses transitive dependencies resolved from git URLs, tarball URLs, or anything but the registry. |
| `verifyDepsBeforeRun: warn` | `pnpm run` never installs implicitly; the dev and build scripts install explicitly. |

Never add `dangerouslyAllowAllBuilds` — it disables the `allowBuilds` gate wholesale.

Every `pnpm install` re-checks the committed lockfile against these policies. If it
reports violations, the lockfile is either stale or was produced somewhere the policy
wasn't applied. Inspect the `pnpm-lock.yaml` diff before trusting it; if the changes
are expected, `pnpm clean --lockfile && pnpm install` rebuilds from a fresh
resolution.

Adding a dependency published in the last three days will be refused. That's the
policy working — pin to the previous release and pick the new one up later, rather
than adding an exclusion.

**The backend has no third-party dependencies at all.** `backend/go.mod` declares no
requires and there is no `go.sum`; the Go code is standard library only. There is no
Go supply chain to harden beyond the toolchain version.

## Building

### Local Production Build

```bash
make build
```

This runs `scripts/build.sh`, which:

1. Builds the frontend: `pnpm install --frozen-lockfile && tsc -b && vite build` → outputs to `frontend/dist/`
2. Copies that output into `backend/webui/` (the `//go:embed` directory)
3. Builds the backend: `CGO_ENABLED=0 go build` → outputs binary to `backend/server`

The Go binary embeds the frontend static files via `//go:embed`, so the final binary is a single self-contained executable.

`backend/webui/` holds one committed file, an empty `.gitkeep`. `//go:embed` treats a
pattern that matches no files as a compile error, so without it `go build` and
`go test ./...` fail on a clean checkout, before any frontend build has run. The build
script clears that directory but leaves `.gitkeep` alone; everything else in it is
git-ignored build output. The static handler refuses to serve dotfiles, so the
keep-file is not reachable over HTTP.

The embed uses the `all:` prefix (`//go:embed all:webui`). The bare `//go:embed webui`
form skips names beginning with `.` or `_`, which would leave the directory with no
embeddable files on a clean checkout and fail to compile for the same reason.

### Running the Built Binary

```bash
make run
```

Serves the app at `http://127.0.0.1:8080`. Requires `make build` first — the target
exits with a message rather than running a stale or missing binary.

It also refuses to run a binary built for a different platform. A devcontainer
shares this checkout with the host, so a binary built inside the container sits in
the host's tree and vice versa; without the check the only symptom is a bare
`cannot execute binary file`. Rebuild with `make build` on whichever side you are
running from. Override the
bind address and port with the `HOST` and `PORT` environment variables:

```bash
PORT=9000 make run
```

### Docker Build

```bash
make docker-build
```

This produces a minimal distroless image (~11MB) through a multi-stage build:

1. **Stage 1** (`node:24.14-alpine`): Builds the frontend with pnpm
2. **Stage 2** (`golang:1.25-alpine`): Compiles the Go binary with embedded frontend
3. **Stage 3** (`distroless/static-debian12`): Copies only the binary — no shell, no OS packages

### Run with Docker Compose

```bash
make docker-run
```

This starts the container with port 8080 exposed and a `./data` volume for checkpoint
persistence. Access the app at `http://localhost:8080`. Writes to the target are real —
see the dry-run override above.

## API Endpoints

All endpoints are prefixed with `/api/v1/`:

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/v1/configure` | Store source token, target host, target token |
| `POST` | `/api/v1/preflight` | Run pre-flight checks |
| `POST` | `/api/v1/gather` | Start gathering data from the source workplace (returns 202; runs in the background) |
| `GET` | `/api/v1/gather` | Gather status, for a client that missed the SSE completion event |
| `GET` | `/api/v1/plan` | Get the migration plan |
| `POST` | `/api/v1/migrate` | Start migration execution |
| `POST` | `/api/v1/migrate/pause` | Pause a running migration |
| `POST` | `/api/v1/migrate/resume` | Resume from checkpoint |
| `GET` | `/api/v1/status` | Current migration status |
| `GET` | `/api/v1/checkpoint` | Checkpoint state |
| `GET` | `/api/v1/report/service-tokens` | Service token report |
| `GET` | `/api/v1/report/manual-steps` | Manual steps report |
| `GET` | `/api/v1/report/summary` | Migration summary |
| `GET` | `/api/v1/events` | SSE stream for real-time progress |

Non-API routes serve the frontend SPA with fallback to `index.html` for client-side routing.

---

## Continuous Integration

`.github/workflows/ci.yml` runs on every push to `master` and every pull request,
in two jobs:

- **Tests** — `go vet`, a `gofmt` check, `go test ./... -race`, the frontend
  typecheck, and the Vitest suite. The frontend install uses
  `--frozen-lockfile`, so lockfile drift fails the build and every entry is
  re-checked against the supply-chain policy.
- **Container** — builds the image and smoke-tests it: waits for the server, asserts
  `/api/v1/status` answers, confirms `/` serves the real built frontend rather than
  the `//go:embed` placeholder, and checks the origin guard rejects a foreign
  `Origin`.

The container job is the only thing that verifies the Dockerfile's pinned base
images and the embedded-frontend build end to end.

## Verifying Against a Live Instance

Everything in the test suites is verified against the Doppler server source and
mocked HTTP — nothing exercises a real instance. `scripts/verify-against-local.sh`
closes that gap for the wire formats most likely to be wrong, by round-tripping a
throwaway project against a running Doppler instance:

```bash
DOPPLER_TOKEN=<token> bash scripts/verify-against-local.sh
```

It defaults to the local dev instance (`https://api.dopplerlocal.com:3030`);
override with `DOPPLER_API`. Use a **service account token** — a user-backed token
cannot read restricted secrets, which is one of the things being checked.

It confirms `create_default_environments`, `personal_configs`, and that
`change_requests` sets `valueType` and `visibility` at creation time and both
round-trip on read. The project is deleted on exit.

## Running Tests

### Run Everything

```bash
make test
```

This runs both backend and frontend test suites sequentially.

### Backend Tests

```bash
make test-backend
```

Or directly:

```bash
cd backend && go test ./... -v
```

**What's tested (329 tests across 4 packages, all passing):**

| Package | Tests | What's Covered |
|---------|-------|---------------|
| `internal/api` | 93 | HTTP handlers, routing, middleware, SSE broker, JSON serialization, error responses, static/SPA file serving, checkpoint path resolution |
| `internal/doppler` | 86 | API client, rate limiter, pagination, retry logic, all resource operations |
| `internal/migration` | 125 | Gatherer, planner, executor, checkpoint manager, full integration cycles |
| `internal/preflight` | 25 | Token validation, connectivity checks, target write access, restricted-secret readability, user provisioning |

**Key integration tests in `internal/migration`:**

- **Full cycle**: Gather → plan → execute with multi-project data, verifies checkpoint completion and resource creation
- **Idempotent re-run**: Execute twice with same data, confirms second run creates nothing new
- **Partial failure + resume**: Injects error mid-migration, verifies checkpoint tracks partial progress, then resumes and completes
- **Dev mode**: Full cycle with writes bypassed, verifies plan generation without target mutations
- **Large dataset**: 10 projects × 3 environments × 5 configs × 20 secrets, verifies correct counts and completion
- **Gather → plan accuracy**: Verifies plan summary counts match gathered data exactly
- **Manual step generation**: Verifies correct manual steps for integrations, service tokens, and syncs
- **User check**: Verifies missing user detection against target instance

**Running a single package:**

```bash
cd backend && go test ./internal/migration/... -v
cd backend && go test ./internal/doppler/... -v
cd backend && go test ./internal/api/... -v
cd backend && go test ./internal/preflight/... -v
```

**Running a specific test:**

```bash
cd backend && go test ./internal/migration/... -v -run TestIntegrationFullCycle
```

### Frontend Tests

```bash
make test-frontend
```

Or directly:

```bash
cd frontend && pnpm test
```

**What's tested (263 tests across 23 test files, all passing):**

| Category | Files | Tests | What's Covered |
|----------|-------|-------|---------------|
| **UI Components** | 7 | 62 | Button, Alert, Card, Input, ProgressBar, StatusIcon, Badge — variants, states, props, ARIA attributes |
| **Wizard Components** | 2 | 14 | WizardStepper navigation and step states, step definitions |
| **Pages** | 9 | 74 | All 9 wizard pages — rendering, form validation, API interactions, error states, navigation |
| **Hooks** | 1 | 5 | useSSE connection lifecycle, event handling, cleanup |
| **App Routing** | 1 | 11 | Route resolution, default redirects, 404 handling |
| **Integration** | 1 | 4 | Full wizard flow (Welcome → Configure → Preflight), error recovery, stepper tracking, back navigation |
| **Accessibility** | 1 | 15 | ARIA roles, attributes, heading hierarchy, keyboard accessibility, focus management |
| **Setup** | 1 | — | Test utilities and custom matchers |

**Watch mode** (re-runs on file changes):

```bash
cd frontend && pnpm run test:watch
```

**Running a specific test file:**

```bash
cd frontend && pnpm exec vitest run tests/integration/wizardFlow.test.tsx
```

**Running tests matching a pattern:**

```bash
cd frontend && pnpm exec vitest run -t "stepper"
```

### Test Configuration

**Backend:**
- Uses Go's standard `testing` package and `net/http/httptest`
- No external test dependencies
- Mock interfaces for Doppler API client (`mockClient`, `mockReadWriteClient`)
- Tests run in parallel where safe

**Frontend (`frontend/vitest.config.ts`):**
- **Runner**: Vitest 5.x
- **Environment**: jsdom (browser DOM simulation)
- **Globals**: `true` — `describe`, `it`, `expect` are available without imports
- **Setup file**: `tests/setup.ts` (loads `@testing-library/jest-dom` matchers)
- **Test location**: `tests/**/*.test.{ts,tsx}`

## Code Architecture

### Backend

The backend is a single Go binary with no external dependencies. Key design decisions:

- **Standard library only**: No frameworks, no ORMs — just `net/http`, `encoding/json`, and friends
- **Secrets never touch disk**: All sensitive data is in-memory only, cleared after each config's secrets are migrated
- **Idempotent execution**: Check-before-create pattern on the target instance — safe to re-run
- **Rate limit awareness**: Tracks Doppler API rate limits via response headers, proactively slows down at 10% remaining, waits on 429
- **Checkpoint system**: JSON file with metadata only (no secrets), enables pause/resume across restarts
- **SSE for progress**: Server-Sent Events stream real-time updates to the frontend during gather and migrate phases

### Frontend

The frontend is a React SPA with a 9-step wizard flow:

1. **Welcome** — Overview and prerequisites
2. **Configure** — Source token, target host, target token
3. **Pre-flight** — Validate tokens and connectivity
4. **Gathering** — Collect data from source (with progress)
5. **Plan Review** — Review what will be migrated (tree view)
6. **Manual Steps** — Items that can't be auto-migrated
7. **Migration** — Execute migration (with real-time progress)
8. **Service Token Report** — Tokens that need manual recreation
9. **Completion** — Summary and next steps

Key patterns:
- **Vite** for dev server and builds, with API proxy to backend
- **Tailwind CSS** with a custom Doppler-inspired color theme
- **React Router** for wizard navigation
- **SSE hook** (`useSSE`) for real-time progress updates
- **Minimal dependencies**: React, React Router, Lucide icons — nothing else

## Cleaning Up

```bash
make clean
```

Removes the `backend/server` binary, `frontend/dist/`, `frontend/node_modules/`, and
the copied assets in `backend/webui/` (leaving `.gitkeep`, so the build still
compiles afterwards).
