# Doppler On-Prem Migrator

Copy a Doppler workplace to another Doppler instance with a guided web UI. The usual move is from Doppler.com to a self-hosted instance, but either side can be Doppler.com or self-hosted. Nothing on the source is changed, and no secret value is ever written to disk.

- [Highlights](#highlights)
- [What Gets Migrated](#what-gets-migrated)
- [Before You Start](#before-you-start)
- [Quick Start](#quick-start)
- [Using The Migrator](#using-the-migrator)
- [Safety And Recovery](#safety-and-recovery)
- [Configuration](#configuration)
- [Security](#security)
- [Architecture](#architecture)
- [Development](#development)
- [License](#license)

## Highlights

- **Secrets stay in memory.** Secret values are read from the source, written to the target over HTTPS, and cleared once each config is done. Checkpoints, run history and reports hold names, counts and statuses only.
- **Try it on a few projects first.** A partial migration copies the projects you pick, whole, and leaves the rest for later. A full migration afterwards picks up from there.
- **Keep the source in use until you are ready.** After the full migration, top-ups bring over what changed on the source. Cutover happens when your teams are ready to switch.
- **Safe to run again.** Everything is checked on the target before it is created. An interrupted run resumes from its checkpoint, and a rerun retries only what failed.
- **Nothing is written before you confirm.** Every run stops at Review, which shows what will be created, updated or left alone.
- **Rate-limit aware.** Each token follows the budget Doppler reports and slows down before running out. The top bar shows how much of each token's budget is used and when it resets.
- **Clear about what it cannot do.** Integrations, rotated secrets, webhook signing secrets and token values cannot be read through the API. Each is listed as a manual step, and service tokens can be issued again from the UI.

## What Gets Migrated

| Resource | Method |
|----------|--------|
| Projects, environments, and configs | Fully automated |
| Secrets and secret values | Fully automated (raw values, so `${REFERENCE}` syntax survives) |
| Secret visibility, value types and notes | Fully automated. Reading `restricted` secrets needs a service account token, see [Before You Start](#before-you-start) |
| Custom workplace and project roles | Fully automated |
| Groups and group memberships | Fully automated (needs the users to exist on the target) |
| Service accounts and their OIDC identities | Fully automated, except the account the source token belongs to. It exists to run the migration, so it is left out along with its tokens and memberships |
| Environment ordering | Fully automated |
| Webhooks | Fully automated, without their signing secrets |
| Change request policies | Fully automated (a policy whose reviewers don't exist on the target is skipped and reported) |
| Trusted IPs | Fully automated (the target's default allow-all entry is removed when the source doesn't have it) |
| Config locks and inheritance | Fully automated (a config inheriting from one outside the migration is skipped and reported) |
| Project and group member access | Fully automated (needs the users to exist on the target) |
| Workplace user access levels | Fully automated (needs the users to exist on the target; the migrating account is skipped) |
| Workplace name | **Opt-in**, off by default, since the target's name is usually chosen deliberately. Enable it on the Configure step |
| **Service tokens** | Issued again from the Service Tokens page, or put on a worklist. The API never returns a token's value, so the old one cannot be copied |
| **Service account tokens** | Same as service tokens |
| **Integrations** | Manual. Their credentials are redacted in the API |
| **Secrets syncs** | Manual. There is no API to list them |
| **Rotated and dynamic secrets** | Manual. Each needs its integration and credentials |
| **Webhook signing secrets** | Manual. The API never returns them, so migrated webhooks arrive unsigned |
| **Personal configs** | Not migrated. Each belongs to one user and cannot be seen by any other token |
| **Users** | Not migrated. The API cannot create users; provision them on the target with [SCIM](https://docs.doppler.com/docs/scim) or by invitation first |
| **Change requests in flight, activity logs, dynamic secret leases** | Not migrated |

## Before You Start

1. **A source token from a service account.** Only a service account can read `restricted` secrets. A user-backed token (`dp.pt.`, or a `dp.ct.` CLI token) can read `unmasked` and `masked` secrets only, and a config holding one restricted secret fails to read entirely.

   The account can be read-only. The migrator's **Source Token Permissions** page (linked from Welcome and Configure) lists every permission it uses. It shows how to create the account in the dashboard or with `curl`, and how to give it access to each project. In short:

   - Workplace permissions: `team`, `service_accounts`, `service_account_api_tokens`, `service_account_identities`, `change_request_policy_read`, `workplace_integrations_list` and `all_enclave_projects`.
   - A project role, given to the account on every project, with `enclave_project_environment_list_all`, `enclave_project_environment_all`, `enclave_project_config_secrets_read` and `enclave_project_config_rotated_secrets_read`.
   - To migrate everything, the project role also needs `enclave_project_config_service_tokens`, `enclave_project_config_trusted_ips`, `enclave_project_members` and `enclave_project_webhooks`. These are Doppler's "manage" permissions, the only ones that can list those resources. The migrator never writes to the source. Without them the migration still runs, and Pre-flight lists what will be left out.

2. **A target token with workplace admin access** (`dp.ct.`, `dp.pt.` or `dp.sa.`). It creates everything on the target and reads the target's config logs to spot changes made there between runs.

3. **Users on the target.** Group memberships, project access and change request reviewers can only point at users who already exist. Set up [SCIM](https://docs.doppler.com/docs/scim) or invite them first. Pre-flight tells you how many are missing.

4. **A list of your integrations and syncs.** They need fresh credentials on the target and have to be recreated by hand.

## Quick Start

The server binds to `127.0.0.1:8080`. Open `http://localhost:8080` and follow the wizard.

### Docker

```bash
git clone https://github.com/doppler/onprem-migrator.git
cd onprem-migrator
make docker-build
docker run -p 127.0.0.1:8080:8080 -v ./data:/data doppler-migrator
```

The `/data` volume holds the checkpoint and run history, so a stopped migration can carry on where it left off.

### Docker Compose

```bash
docker compose up
```

Set `SOURCE_DOPPLER_TOKEN` and `TARGET_DOPPLER_TOKEN` in your shell first to have the Configure step filled in; Compose passes them through when they are set. For a dry run that writes nothing to the target, use `docker compose -f docker-compose.yml -f docker-compose.dev.yml up`.

### From Source

Requires Go 1.25+, Node.js 24.14+ and pnpm 11.22.0.

```bash
make build
make run
```

`make help` lists every target.

## Using The Migrator

The wizard has seven steps. It never moves on by itself: each step waits for you, so there is time to read what happened.

**Welcome** lists what is migrated, what you will do by hand, and what is not migrated at all. If a run was interrupted, it offers to resume it. If a partial migration has already run, it offers to carry that migration on instead of starting again.

1. **Configure.** Choose the source and target instances and enter their tokens. Tokens are checked for a valid prefix before anything is sent. Options: migrate the workplace name (off by default), **Stop When A New Kind Of Error Appears** (on by default), and dev mode (a dry run with no writes to the target).
2. **Pre-flight.** Checks that both tokens are valid, that both instances can be reached, that the target token can write, that the source token can read restricted secrets and everything else being migrated, and how many source users are missing on the target. A failed or warning check says how to fix it, and re-checking is the default action.
3. **Gather.** Reads everything the migration needs from the source, with progress shown as it goes. Anything the source token cannot read is listed rather than failing the run.
4. **Scope.** Choose a **Full Migration** or a **Partial Migration** of the projects you pick. Before a top-up, this step compares the source with the target instead.
5. **Review.** Shows the plan: every resource that will be created or updated, and what already matches. Nothing is written to the target until you confirm here.
6. **Migrate.** Runs the plan in ten phases, in dependency order:
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

   Change request policies come after users, groups and service accounts because they name them as reviewers. Trusted IPs come last because restricting a config takes effect immediately, and the machine running the migration is rarely inside the allowed ranges. Once a config is restricted, the migrator can no longer write to it.

   Each resource shows up as it is written. The run can be paused and resumed. Failures are grouped by cause, each with what the API returned and how to fix it, and **Fix And Re-run** retries only what failed.
7. **Summary.** Counts for everything migrated and what is still open. From here: **Service Tokens**, **Manual Steps**, and the choice of what to do next.

### Partial Migrations

A partial migration copies the projects you choose, each whole, with the roles, groups and service accounts their members use. Workplace settings, change request policies and workplace user access are left for the full migration. Run more partials to add projects, then a full migration to bring over the rest. Projects migrated earlier are brought up to date rather than copied again. Cutover is only possible after a full migration.

### Service Tokens

The API never returns a token's value, so each service token and service account token on the source has to be issued again. For each one in a migrated project, choose:

- **Issue** creates it on the target with the same access and placement. The new value is shown once, to copy into wherever it is used, and the migrator never stores it.
- **Skip** puts it on a worklist (CSV) to recreate by hand.
- **Later** sets it aside. It comes back undecided on the next top-up.

Cutover waits until every token is issued or skipped, because cutover removes the write access that issuing needs.

### Waiting To Cut Over

A full migration does not have to be followed by cutover straight away. The summary asks what happens next: **Cut Over Now**, or **Wait, And Top Up Until Then**, which keeps the source in use. The **Waiting To Cut Over** page shows when the target was last brought up to date, every run so far (each opens to show what it changed, by resource type, never values), and what still has to be done before cutover.

- **Top Up Now** reads the source and the target again, compares them, and runs Review and Migrate over the whole source. Only what changed is written. Review lists changes made on the target since the last run, and you choose whether the source overwrites them or the target's values are kept. Deleting secrets and configs that were removed from the source is opt-in. A top-up that finds nothing to change is recorded as a check.
- **Cut Over** applies the source's trusted IP ranges to the target as the last step, with an option to let this machine's address through until the end. When there is nothing to apply, **Finish Migration** marks the migration done without changing the target.

The waiting state, each run and the cutover are kept in `scope-history.json` beside the checkpoint, so they survive a restart. After a restart, enter the tokens again (or set `SOURCE_DOPPLER_TOKEN` and `TARGET_DOPPLER_TOKEN`, which resumes the waiting migration directly).

### Final Report

Cutover offers a report of the whole migration: a self-contained HTML page covering the runs, each phase, the service tokens, cutover, any errors, and what is still to do by hand. It is also available as JSON, next to the errors CSV and the token worklist CSV. The report holds names, counts and outcomes only: no secret values and no token values.

## Safety And Recovery

### One Migration At A Time

The run history holds one migration, from its first run until cutover. While it is unfinished, Configure refuses any other source or target. From the end of the full migration until cutover, opening the migrator goes to the waiting page, and a partial migration cannot be chosen. To abandon a migration instead, stop the server and run `make clear-state`; this forgets the runs without touching either workplace.

### Idempotency

Every resource is checked on the target before it is created. Groups and service accounts are matched by name and users by email, because each instance gives them its own IDs. A resource that already exists is left alone, except for secrets, which are updated to the source's value. Running the migration twice gives the same result as running it once.

### Checkpoints, Pause And Resume

The checkpoint (`migration-checkpoint.json` in `CHECKPOINT_DIR`) records which phases have finished and which resources were created, skipped or failed, with error messages. It contains metadata only: names, IDs and statuses. **Pause** stops after the current operation; **Resume**, or starting the server again, continues from the checkpoint and skips what is already done.

### Errors And Retries

| Situation | What happens |
|-----------|--------------|
| Rate limited (HTTP 429) | Waits until the limit resets, then continues |
| Server error (HTTP 5xx) | Read requests are retried up to 3 times with exponential backoff; writes are recorded as failed |
| Client error (HTTP 4xx) | The resource is recorded as failed, and the run continues with the rest |
| Network failure | Retried up to 3 times with exponential backoff |
| Process stopped | Resume from the checkpoint; finished work is kept |

A failure on one resource doesn't stop the run, unless it is a new kind of error and **Stop When A New Kind Of Error Appears** is on. That option is on by default so that a bad token doesn't fail every resource in the workplace before anyone notices.

## Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `HOST` | `127.0.0.1` | Bind address (`0.0.0.0` inside Docker) |
| `PORT` | `8080` | Server port |
| `CHECKPOINT_DIR` | `./data` | Where the checkpoint (`migration-checkpoint.json`), run history (`scope-history.json`) and service token decisions (`service-tokens.json`) are kept. None of them hold secret or token values |
| `DEV_MODE` | `false` | Dry run: no writes to the target, and target pre-flight checks are skipped |
| `SOURCE_DOPPLER_TOKEN` | — | Fills in the source token on Configure. Only its type and last four characters are shown |
| `TARGET_DOPPLER_TOKEN` | — | Fills in the target token the same way. With both set, a migration waiting to cut over resumes after a restart without re-entering them |
| `ALLOWED_HOSTS` | — | Extra host names the server answers to, comma-separated, if you reach it at an address other than loopback |

## Security

- **Tokens stay in memory.** They are never written to disk, logs or checkpoints, and the API never returns them. Once a token is entered, the server reports only its type and last four characters (`dp.sa.…a1b2`).
- **Secret values stay in memory.** They are held only while their config is migrated, then cleared, and never logged. Error evidence has secret values and tokens redacted.
- **Issued token values are shown once.** A service token issued on the target is returned in that one response and never stored.
- **Localhost by default.** The server binds to `127.0.0.1`, and the Docker examples publish only to `127.0.0.1`. A bare `-p 8080:8080` would expose the API to the whole network, because Docker's iptables rules bypass host firewalls, and the API has no authentication.
- **Origin-checked.** Requests with a browser `Origin`, or a `Host` other than loopback or `ALLOWED_HOSTS`, are rejected, so a page open in another tab cannot drive the migration.
- **No other calls.** The migrator talks only to the source and target instances over HTTPS. There is no telemetry and no third-party service.
- **Small image.** The container runs as a non-root user on a distroless base, with no shell or OS packages.

## Architecture

A single binary: the Go backend embeds the built React frontend and serves both on one port.

```
Browser ──── :8080 ──── Go backend ──── Source Doppler API
                            │
                            └────────── Target Doppler API
```

- **Backend:** Go, standard library only
- **Frontend:** React 18, TypeScript, Tailwind CSS, Vite
- **Progress:** server-sent events
- **Container:** multi-stage Docker build with a distroless runtime

## Development

See [DEVELOPMENT.md](DEVELOPMENT.md) for setting up a development environment, running the tests, and how the code is laid out.

## License

Copyright 2026 Doppler, Inc. Licensed under the [Apache License, Version 2.0](LICENSE).
