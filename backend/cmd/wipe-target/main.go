// Command wipe-target deletes everything a migration creates in a target
// workplace, so a test migration can be run again from a clean slate.
//
//	TARGET_DOPPLER_TOKEN=dp.sa.… go run ./cmd/wipe-target          # dry run: lists what it would delete
//	TARGET_DOPPLER_TOKEN=dp.sa.… go run ./cmd/wipe-target --yes    # deletes it
//
// It refuses to touch any workplace not named exactly "Migrator Target". That
// is the only thing standing between this and a real workplace, so the name is
// read from the target itself on every run rather than taken from a flag.
//
// TARGET_DOPPLER_HOST sets the instance, and defaults to Doppler's own API.
//
// Removed: change-request policies, projects (and with them their
// environments, configs, secrets, trusted IPs, webhooks, members and service
// tokens), groups, service accounts, and custom project and workplace roles.
// Left alone: users, which the API cannot create or delete; built-in roles;
// the workplace's own settings; and the service account the token belongs to,
// if it belongs to one, since deleting that would revoke the token mid-run.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/doppler/workplace-migrator/internal/doppler"
)

// requiredWorkplaceName is the only workplace this will delete from.
const requiredWorkplaceName = "Migrator Target"

const defaultHost = "https://api.doppler.com"

// stateFiles are the migrator's own records of what it has done. Once the
// target is wiped they describe a migration that no longer exists: the next
// run would treat the wiped projects as already migrated, and the token page
// would show tokens as issued that are gone. Kept in step with the list in
// scripts/clear-state.sh, which clears the same files without the wipe.
var stateFiles = []string{"migration-checkpoint.json", "scope-history.json", "service-tokens.json"}

type options struct {
	execute    bool
	clearState bool
	stateDir   string
}

func main() {
	var opts options
	flag.BoolVar(&opts.execute, "yes", false, "delete for real; without it, only list what would be deleted")
	flag.BoolVar(&opts.clearState, "clear-state", false, "also delete the migrator's local checkpoint, run history and token ledger")
	flag.StringVar(&opts.stateDir, "state-dir", defaultStateDir(), "where the migrator keeps its state (CHECKPOINT_DIR, or ./data)")
	flag.Parse()

	token := strings.TrimSpace(os.Getenv("TARGET_DOPPLER_TOKEN"))
	if token == "" {
		fmt.Fprintln(os.Stderr, "TARGET_DOPPLER_TOKEN is not set. It needs a token with admin access to the target workplace.")
		os.Exit(2)
	}
	host := strings.TrimSpace(os.Getenv("TARGET_DOPPLER_HOST"))
	if host == "" {
		host = defaultHost
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	os.Exit(run(ctx, doppler.NewClient(host, token), host, opts, os.Stdout))
}

func defaultStateDir() string {
	if dir := strings.TrimSpace(os.Getenv("CHECKPOINT_DIR")); dir != "" {
		return dir
	}
	return "./data"
}

// resource is one thing to delete.
type resource struct {
	kind  string // for the listing
	label string
	path  string
	query url.Values
}

// run does the work and returns the process exit code: 0 on success, 1 if
// anything failed to delete, 2 if it refused to start.
func run(ctx context.Context, c *doppler.Client, host string, opts options, out io.Writer) int {
	wp, err := c.GetWorkplace(ctx)
	if err != nil {
		fmt.Fprintf(out, "Could not read the workplace at %s: %v\n", host, err)
		return 2
	}
	if wp.Name != requiredWorkplaceName {
		fmt.Fprintf(out, "Refusing: the workplace at %s is named %q, not %q.\n", host, wp.Name, requiredWorkplaceName)
		fmt.Fprintf(out, "This deletes every project in the workplace, so it only runs against one named exactly %q.\n", requiredWorkplaceName)
		return 2
	}

	// The token's own service account, if it has one, is spared: deleting it
	// revokes the token, and every call after that fails.
	selfAccount := ""
	if info, err := c.GetTokenInfo(ctx); err == nil && info.Principal.Type == "service_account" {
		selfAccount = info.Principal.Slug
	}

	plan, warnings, err := inventory(ctx, c, selfAccount)
	if err != nil {
		fmt.Fprintf(out, "Could not list what is in %q: %v\n", wp.Name, err)
		return 2
	}

	fmt.Fprintf(out, "Workplace %q at %s\n\n", wp.Name, host)
	printPlan(out, plan)
	for _, w := range warnings {
		fmt.Fprintf(out, "Note: %s\n", w)
	}
	if selfAccount != "" {
		fmt.Fprintf(out, "Note: keeping service account %s, which this token belongs to.\n", selfAccount)
	}
	fmt.Fprintln(out, "Note: users, built-in roles and workplace settings are left as they are.")

	if !opts.execute {
		fmt.Fprintln(out, "\nDry run — nothing was deleted. Run again with --yes to delete the above.")
		if opts.clearState {
			fmt.Fprintf(out, "With --yes, --clear-state would also remove the migrator's state in %s.\n", opts.stateDir)
		}
		return 0
	}

	fmt.Fprintln(out)
	failed := 0
	for _, r := range plan {
		if err := ctx.Err(); err != nil {
			fmt.Fprintln(out, "Interrupted.")
			return 1
		}
		if _, _, err := c.Delete(ctx, r.path, r.query); err != nil {
			failed++
			fmt.Fprintf(out, "  failed   %-18s %s: %v\n", r.kind, r.label, err)
			continue
		}
		fmt.Fprintf(out, "  deleted  %-18s %s\n", r.kind, r.label)
	}

	if opts.clearState {
		fmt.Fprintln(out)
		clearState(out, opts.stateDir)
	}

	fmt.Fprintln(out)
	if failed > 0 {
		fmt.Fprintf(out, "Deleted %d of %d. %d failed — see above.\n", len(plan)-failed, len(plan), failed)
		return 1
	}
	fmt.Fprintf(out, "Deleted %d.\n", len(plan))
	if !opts.clearState {
		fmt.Fprintf(out, "The migrator still remembers migrating these. Re-run with --clear-state, or remove its files in %s, before migrating again.\n", opts.stateDir)
	}
	return 0
}

// inventory lists everything to delete, in an order that deletes dependents
// before what they depend on: policies name groups and service accounts as
// reviewers; project members hold roles and groups; groups and service
// accounts hold custom roles.
func inventory(ctx context.Context, c *doppler.Client, selfAccount string) ([]resource, []string, error) {
	var plan []resource
	var warnings []string

	// Change-request policies are a paid feature. A workplace without it
	// refuses the list, which means there are none to delete.
	if policies, err := c.ListChangeRequestPolicies(ctx); err == nil {
		for _, p := range policies {
			plan = append(plan, resource{"policy", p.Name,
				"/workplace/change_request_policies/change_request_policy/" + url.PathEscape(p.ID), nil})
		}
	} else {
		warnings = append(warnings, fmt.Sprintf("could not list change-request policies, so none will be deleted (%v)", err))
	}

	projects, err := c.ListProjects(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("projects: %w", err)
	}
	for _, p := range projects {
		plan = append(plan, resource{"project", p.Slug, "/projects/project", url.Values{"project": {p.Slug}}})
	}

	groups, err := c.ListGroups(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("groups: %w", err)
	}
	for _, g := range groups {
		plan = append(plan, resource{"group", g.Name, "/workplace/groups/group/" + url.PathEscape(g.Slug), nil})
	}

	accounts, err := c.ListServiceAccounts(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("service accounts: %w", err)
	}
	for _, sa := range accounts {
		if sa.Slug == selfAccount {
			continue
		}
		plan = append(plan, resource{"service account", sa.Name,
			"/workplace/service_accounts/service_account/" + url.PathEscape(sa.Slug), nil})
	}

	projectRoles, err := c.ListProjectRoles(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("project roles: %w", err)
	}
	for _, r := range projectRoles {
		if r.IsCustomRole {
			plan = append(plan, resource{"project role", r.Name, "/projects/roles/role/" + url.PathEscape(r.Identifier), nil})
		}
	}

	workplaceRoles, err := c.ListWorkplaceRoles(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("workplace roles: %w", err)
	}
	for _, r := range workplaceRoles {
		if r.IsCustomRole {
			plan = append(plan, resource{"workplace role", r.Name, "/workplace/roles/role/" + url.PathEscape(r.Identifier), nil})
		}
	}

	return plan, warnings, nil
}

func printPlan(out io.Writer, plan []resource) {
	if len(plan) == 0 {
		fmt.Fprintln(out, "Nothing to delete: the workplace holds no projects, groups, service accounts, custom roles or policies.")
		return
	}
	fmt.Fprintf(out, "Will delete %d:\n", len(plan))
	for _, r := range plan {
		fmt.Fprintf(out, "  %-18s %s\n", r.kind, r.label)
	}
	fmt.Fprintln(out)
}

// clearState removes the migrator's own files, and nothing else in the
// directory.
func clearState(out io.Writer, dir string) {
	for _, name := range stateFiles {
		path := filepath.Join(dir, name)
		switch err := os.Remove(path); {
		case err == nil:
			fmt.Fprintf(out, "  removed  %s\n", path)
		case errors.Is(err, os.ErrNotExist):
			// Nothing recorded yet, which is fine.
		default:
			fmt.Fprintf(out, "  failed   %s: %v\n", path, err)
		}
	}
}
