# Doppler On-Prem Migrator

Migrate your entire Doppler.com workplace to a self-hosted on-prem Doppler instance with a guided web UI. One tool, one run, zero secrets on disk.

## Overview

The Doppler On-Prem Migrator is a purpose-built tool that transfers your workplace configuration, secrets, access controls, and policies from Doppler's cloud platform to your self-hosted instance. It provides a step-by-step wizard that walks you through the entire process — from pre-flight validation to a post-migration summary report.

**Key properties:**

- **Secrets never touch disk.** All secret values are held in memory only and cleared immediately after each config is migrated. Checkpoint files track progress metadata — never secret values.
- **Safe to re-run.** The tool is fully idempotent. It checks the target instance before creating anything and tracks progress via checkpoints. If something fails, just run it again.
- **Rate limit aware.** Respects Doppler API rate limits automatically. Proactively slows down as limits approach and waits when throttled — no manual intervention needed.
- **Transparent.** Every migrated resource is visible in the plan review. Items that can't be fully automated (integrations, service tokens, syncs) are called out with clear manual steps.

## What Gets Migrated

| Resource | Method |
|----------|--------|
| Projects, environments, and configs | Fully automated |
| Secrets and secret values | Fully automated (raw values, so `${REFERENCE}` syntax survives) |
| Secret visibility and value types | Fully automated — but reading `restricted` secrets requires a service account token, see below |
| Custom workplace roles | Fully automated |
| Custom project roles | Fully automated |
| Groups and group memberships | Fully automated (requires users to exist on target) |
| Service accounts | Fully automated — except the one the source token belongs to, which exists to run the migration and is left out along with its tokens and memberships |
| Service account identities (OIDC) | Fully automated |
| Environment ordering | Fully automated |
| Webhooks | Fully automated |
| Change request policies | Fully automated (policies whose reviewers don't exist on the target are skipped and reported) |
| Trusted IPs | Fully automated (the target's default allow-all entry is removed when the source doesn't have it) |
| Config locks and inheritance | Fully automated (a config inheriting from one outside the migration is skipped and reported) |
| Project and group member access | Fully automated (requires users to exist on target) |
| Workplace user access levels | Fully automated (requires users to exist on target; the migrating account is skipped) |
| Workplace name | **Opt-in** — off by default, since the target's name is usually chosen deliberately. Enable it on the Configuration step. |
| **Integrations** | Manual — credentials are redacted in the API |
| **Secrets syncs** | Manual — no list API available |
| **Service tokens** | Report generated — token values can't be read after creation |
| **Service account tokens** | Report generated — same constraint as service tokens |
| **Rotated secrets** | Manual — recreating one needs the backing integration and its credentials |
| **Webhook signing secrets** | Manual — the API never returns a secret's value, so migrated webhooks are created unsigned |
| **Personal configs** | Not migrated — each belongs to one user, and the target creates its own |
| **Users** | Provision via SCIM or manual invite before migrating |

## Prerequisites

Before running the migration, you need:

1. **A Doppler.com API token** with read access to all workplace resources on your source instance. **Use a service account token (`dp.sa.xxxx`) if any config contains `restricted` secrets.** The API decides what a token may read from the actor behind it, not from its permissions: a user-backed token (`dp.pt.`, or a `dp.ct.` CLI token) can read `unmasked` and `masked` secrets only, and a config holding a restricted secret fails to read *entirely* — a single restricted secret anywhere will stop the migration. The migrator's **Source Token Permissions** page (linked from Welcome and Configure) lists every permission it uses, with dashboard steps and `curl` commands to create a service account that has them. In short: a service account with the workplace permissions `team`, `service_accounts`, `service_account_api_tokens`, `service_account_identities`, `change_request_policy_read` and `workplace_integrations_list` and `all_enclave_projects`, and a project role, given to the account on every project, with `enclave_project_environment_list_all`, `enclave_project_environment_all`, `enclave_project_config_secrets_read` and `enclave_project_config_rotated_secrets_read` — plus, to migrate everything, `enclave_project_config_service_tokens`, `enclave_project_config_trusted_ips`, `enclave_project_members` and `enclave_project_webhooks`. Those four are Doppler's "manage" permissions, the only ones that can list those things; the migrator never writes to the source. With a read-only token the migration carries on without them and lists them as not migrated.
2. **An on-prem Doppler API token** (`dp.ct.xxxx` or `dp.pt.xxxx`) with full read/write access on your target instance
3. **Users provisioned on the on-prem instance** via SCIM or manual invite — the Doppler API has no Create User endpoint, so users must exist before access controls can be migrated
4. **Integrations and syncs documented** from your Doppler.com dashboard — these require fresh credentials on the on-prem instance and can't be auto-migrated

## Quick Start

### Using Docker (Recommended)

```bash
docker run -p 127.0.0.1:8080:8080 -v ./data:/data doppler-migrator
```

Open `http://localhost:8080` in your browser and follow the wizard.

The `/data` volume persists checkpoint files so you can safely stop and resume the migration.

### Using Docker Compose

```bash
git clone https://github.com/doppler/onprem-migrator.git
cd onprem-migrator
docker compose up
```

Open `http://localhost:8080`.

### Building from Source

Requires Go 1.25+, Node.js 24.14+, and pnpm 11.22.0.

```bash
git clone https://github.com/doppler/onprem-migrator.git
cd onprem-migrator
make build
make run
```

Run `make help` for all available targets.

Open `http://localhost:8080`.

## How It Works

The wizard guides you through 9 steps:

### Step 1: Welcome
Overview of what will be migrated and what you need before starting.

### Step 2: Configure
Enter your source token (Doppler.com), target host (your on-prem URL), and target token. The tool validates token formats before proceeding.

### Step 3: Pre-flight Checks
Validates that both tokens are valid, both instances are reachable, and the target is ready to receive data. It also compares the users on each side.

**If users are missing on the target, this is where you find out.** The Doppler API cannot create users, so neither can this tool — anything naming a missing user (group and project membership, workplace access levels, change request policy reviewers) is skipped, and the migration still reports success. Provision users with SCIM first, then re-check. Re-checking is the default action; you can continue past a warning deliberately.

### Step 4: Gather
Collects everything the migration needs from your source workplace. Every resource type is listed, and anything that comes back empty stays on the list greyed out — "no change request policies" is a result worth seeing, not a row worth hiding. Progress streams in over server-sent events.

### Step 5: Plan Review
Displays a complete tree view of everything that will be migrated, organized by workplace resources and projects. Review and approve before any writes happen to the target.

### Step 6: Manual Steps
Lists items that can't be fully automated — integrations that need fresh credentials, syncs and rotated secrets that must be recreated, service tokens and service account tokens whose values can't be read back, and any other manual actions. Review these so you know what to do after migration.

### Step 7: Migrate
Executes the migration in 10 dependency-ordered phases:

1. Workplace settings and custom roles
2. Groups, service accounts, and service account identities
3. Projects, environments, and environment ordering
4. Configs (root and branch)
5. Secrets and config locks
6. Config inheritable flags, then inheritance relationships
7. Webhooks
8. Group and project member access, then workplace user access levels
9. Change request policies
10. Trusted IPs

Two of those positions matter. Change request policies name users, groups and
service accounts as reviewers, so they run only once every one of those exists and
is fully set up. And trusted IPs come last: restricting a config takes effect
immediately, and the machine running the migration is rarely inside the allowed
ranges — so once a config is restricted, the tool can no longer write to it.

Real-time progress shows each resource as it's created. The migration can be paused and resumed at any time — progress is checkpointed after every successful operation.

The wizard never advances on its own. Each step waits for you to continue, so there
is time to read what happened before moving on.

### Step 8: Service Token Report
Displays all service tokens from the source that need to be manually recreated on the target instance (token values can't be read from the API after creation). Use this report to create new tokens and update your deployments.

### Step 9: Completion
Summary of everything that was migrated, with counts for each resource type and timestamps.

### Waiting To Cut Over
A full migration does not have to be followed by cutover straight away. The summary asks what happens next: **Cut Over Now**, or **Wait, And Top Up Until Then**, which keeps the source in use. Waiting leads to the **Waiting To Cut Over** page. It shows when the target was last brought up to date, every run so far, and what has to be finished before cutover. From there:

- **Top Up Now** reads the source and the target again and runs Compare, Review and Migrate over the whole source. Only what changed is written, and nothing is written before Review. A top-up that finds no changes is recorded as a check and goes back to waiting.
- **Cut Over** applies the trusted IP ranges and ends the migration. When there is nothing to apply, **Finish Migration** records it as done without changing the target.

Each run in the waiting page's list opens to show what it changed, by resource type: names and counts, never values.

The choice to wait, each top-up with what it changed, and the cutover are kept in `scope-history.json` beside the checkpoint, so waiting survives restarts. After the tokens are entered again, Pre-flight continues to the waiting page.

**One migration at a time.** From the end of the full run until cutover, the migration is in progress, and it is the only one: opening the migrator goes to the waiting page (with a note saying why), a partial migration cannot be chosen, and Configure refuses a different source or target. Once it is cut over, another can start. To abandon one instead, stop the server and run `make clear-state`.

### Final Report
The Cutover step offers a report of the whole migration: a self-contained HTML page covering the runs, each phase, the service tokens, cutover, any errors, and what is still to do by hand. The same report is available as JSON, beside the errors CSV and the token worklist CSV. It lists names, counts and outcomes only — no secret values and no token values.

## Migration Safety

### Idempotency

Every resource is checked before creation. If a project, environment, config, role, or other resource already exists on the target (matched by name or slug), it's skipped. Secrets are the exception — if a secret exists on the target with a different value, it's updated (the source is authoritative).

This means:
- Running the migration twice produces the same result as running it once
- A failed migration can be re-run without duplicating resources
- You can safely add resources to the source after a partial migration and re-run

### Checkpoints

The checkpoint file (`migration-checkpoint.json`) tracks:
- Which phases have completed
- Which individual resources have been created, skipped, or failed
- Error messages for any failures

Checkpoints contain **only metadata** — resource names, IDs, and status. Secret values are never written to disk.

### Pause and Resume

Click "Pause" during migration to stop after the current operation completes. The checkpoint is saved. Click "Resume" (or restart the tool) to continue from where you left off. Already-completed resources are skipped.

### Error Handling

| Scenario | Behavior |
|----------|----------|
| Rate limited (HTTP 429) | Waits for the retry period, then continues automatically |
| Server error (HTTP 5xx) | Retries up to 3 times with exponential backoff |
| Client error (HTTP 4xx) | Marks the resource as failed, continues with remaining resources |
| Network failure | Retries up to 3 times with exponential backoff |
| Process killed | Resume from checkpoint — all completed work is preserved |

Individual resource failures don't halt the migration. The summary report shows all failures with error details.

## Configuration

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `HOST` | `127.0.0.1` | Bind address (`0.0.0.0` for Docker) |
| `PORT` | `8080` | Server port |
| `DEV_MODE` | `false` | Dry run: bypass target pre-flight checks and all write operations |
| `CHECKPOINT_DIR` | `./data` | Directory for checkpoint file storage |
| `SOURCE_DOPPLER_TOKEN` | — | Pre-fills the source token on the Configuration page. Only its type and last four characters are shown; typing a token there replaces it |
| `TARGET_DOPPLER_TOKEN` | — | Pre-fills the target token the same way |

### Docker Volumes

Mount a volume at `/data` to persist checkpoint files across container restarts:

```bash
docker run -p 127.0.0.1:8080:8080 -v /path/to/data:/data doppler-migrator
```

## Security

- **Tokens are in-memory only.** Source and target tokens are never written to disk, logs, or checkpoint files, and the API never returns their values. Once a token is entered, the configuration endpoint reports only its type prefix and last four characters (`dp.ct.…a1b2`), enough to recognize which credential is loaded.
- **Secrets are in-memory only.** Secret values are fetched from the source and sent to the target via HTTPS. They're held in memory only for the duration of each config's migration, then cleared.
- **Localhost by default.** The server binds to `127.0.0.1` unless overridden, and the Docker examples publish to `127.0.0.1` rather than every interface. Publishing a bare `-p 8080:8080` would expose the API to the whole network segment — Docker's own iptables rules bypass host firewalls — and the API has no authentication.
- **Origin-checked.** Requests carrying a browser `Origin`, or a `Host` that isn't the loopback address the server is reachable at, are rejected. This stops a page you happen to have open from driving the migration. Set `ALLOWED_HOSTS` if you reach the tool at another address.
- **HTTPS only.** All communication with Doppler APIs uses HTTPS.
- **No external calls.** The tool only communicates with the source Doppler instance (api.doppler.com) and the target instance you specify. No telemetry, no analytics, no third-party services.
- **Minimal attack surface.** The production binary runs in a distroless container with no shell and no OS packages.

## Architecture

The tool is a single self-contained binary. The Go backend embeds the compiled React frontend and serves everything on one port.

```
Browser ──── :8080 ──── Go Backend ──── Doppler.com API (source)
                            │
                            └──── On-Prem Doppler API (target)
```

- **Backend**: Go, standard library only — no frameworks or external dependencies
- **Frontend**: React 18, TypeScript, Tailwind CSS, Vite
- **Progress updates**: Server-Sent Events (SSE) for real-time migration progress
- **Container**: Multi-stage Docker build → distroless runtime (~11MB image)

## Development

See [DEVELOPMENT.md](DEVELOPMENT.md) for instructions on setting up a local development environment, running tests, and building from source.

## License

Copyright Doppler, Inc. All rights reserved.
