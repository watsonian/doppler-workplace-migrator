package preflight

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/doppler/onprem-migrator/internal/doppler"
)

// CheckStatus represents the status of a single pre-flight check.
type CheckStatus string

const (
	StatusPending CheckStatus = "pending"
	StatusRunning CheckStatus = "running"
	StatusPassed  CheckStatus = "passed"
	StatusFailed  CheckStatus = "failed"
	StatusWarning CheckStatus = "warning"
	StatusSkipped CheckStatus = "skipped"
)

// Check represents a single pre-flight check result.
//
// A failing check carries the same three things an error does during the run —
// what went wrong, the exchange that proves it, and how to fix it — so the
// remedy is learned once rather than twice.
type Check struct {
	ID      string      `json:"id"`
	Name    string      `json:"name"`
	Status  CheckStatus `json:"status"`
	Message string      `json:"message,omitempty"`
	Detail  string      `json:"detail,omitempty"`

	// Evidence is the request the check made and what came back. Present only
	// when a check failed against a real exchange.
	Evidence *doppler.Evidence `json:"evidence,omitempty"`

	// Scope is what this would cost if it is not fixed, e.g. how many configs
	// would fail. Nothing is more persuasive than the number.
	Scope *CheckScope `json:"scope,omitempty"`

	// Fix describes the remedy, mirroring the error classes so the same panel
	// renders both.
	Fix *CheckFix `json:"fix,omitempty"`
}

// CheckScope quantifies the consequence of leaving a check unfixed.
type CheckScope struct {
	// Affected and Total are counted in Unit, e.g. 214 of 640 "configs".
	Affected int    `json:"affected"`
	Total    int    `json:"total"`
	Unit     string `json:"unit"`
	// Estimated says the number came from a sample rather than a full count.
	// Reporting an estimate as fact is worse than reporting no number.
	Estimated bool `json:"estimated"`
	// Description is the sentence the interface shows.
	Description string `json:"description"`
}

// CheckFix is how a failing check is cleared before the run starts.
type CheckFix struct {
	// Kind matches the error-class remedy vocabulary: "token", "recheck".
	Kind  string `json:"kind"`
	Label string `json:"label"`
	// Field, when set, names the input the fix needs, e.g. "source_token".
	Field string `json:"field,omitempty"`
	Hint  string `json:"hint,omitempty"`
	// Blocking says continuing past this will definitely cost something, as
	// opposed to a warning that merely might.
	Blocking bool `json:"blocking"`
}

// checkResult is what an individual check returns. A struct rather than a
// handful of unnamed strings, so a check can add evidence without every other
// check's signature changing.
type checkResult struct {
	Status   CheckStatus
	Message  string
	Detail   string
	Evidence *doppler.Evidence
	Scope    *CheckScope
	Fix      *CheckFix
}

// ok, warn and fail build the common results.
func ok_(message string) checkResult { return checkResult{Status: StatusPassed, Message: message} }
func warn(message, detail string) checkResult {
	return checkResult{Status: StatusWarning, Message: message, Detail: detail}
}
func fail(message, detail string) checkResult {
	return checkResult{Status: StatusFailed, Message: message, Detail: detail}
}

// failWith attaches the exchange that proves a failure.
func failWith(err error, side, message, detail string) checkResult {
	ev := doppler.EvidenceFrom(err, side)
	return checkResult{Status: StatusFailed, Message: message, Detail: detail, Evidence: &ev}
}

// Checker runs pre-flight checks against source and target Doppler instances.
type Checker struct {
	source   *doppler.Client
	target   *doppler.Client
	devMode  bool
	onUpdate func(Check)
}

// NewChecker creates a new pre-flight checker.
// target may be nil if devMode is true.
func NewChecker(source, target *doppler.Client, devMode bool, onUpdate func(Check)) *Checker {
	return &Checker{
		source:   source,
		target:   target,
		devMode:  devMode,
		onUpdate: onUpdate,
	}
}

// RunAll runs all pre-flight checks and returns the results.
// Each check is run independently — a failure in one does not prevent others.
func (ch *Checker) RunAll(ctx context.Context) []Check {
	checks := []struct {
		id   string
		name string
		fn   func(ctx context.Context) checkResult
	}{
		{"source_token", "Source token valid", ch.checkSourceToken},
		{"source_workplace", "Source workplace accessible", ch.checkSourceWorkplace},
		{"source_projects", "Source can list projects", ch.checkSourceProjects},
		{"target_reachable", "Target host reachable", ch.checkTargetReachable},
		{"target_token", "Target token valid", ch.checkTargetToken},
		{"target_workplace", "Target workplace accessible", ch.checkTargetWorkplace},
		{"target_write", "Target has write access", ch.checkTargetWrite},
		{"source_restricted_secrets", "Source token can read restricted secrets", ch.checkSourceCanReadRestricted},
		{"source_permissions", "Source token can read everything migrated", ch.checkSourcePermissions},
		{"users_provisioned", "Users provisioned on target", ch.checkUsersProvisioned},
		{"target_config_logs", "Target token can read config logs", ch.checkTargetConfigLogs},
	}

	results := make([]Check, 0, len(checks))

	for _, c := range checks {
		check := Check{
			ID:     c.id,
			Name:   c.name,
			Status: StatusRunning,
		}

		ch.emit(check)

		// Check for context cancellation
		if ctx.Err() != nil {
			check.Status = StatusFailed
			check.Message = "Cancelled"
			check.Detail = ctx.Err().Error()
			ch.emit(check)
			results = append(results, check)
			continue
		}

		res := c.fn(ctx)
		check.Status = res.Status
		check.Message = res.Message
		check.Detail = res.Detail
		check.Evidence = res.Evidence
		check.Scope = res.Scope
		check.Fix = res.Fix

		ch.emit(check)
		results = append(results, check)
	}

	return results
}

func (ch *Checker) emit(check Check) {
	if ch.onUpdate != nil {
		ch.onUpdate(check)
	}
}

// --- Individual checks ---

func (ch *Checker) checkSourceToken(ctx context.Context) checkResult {
	info, err := ch.source.GetTokenInfo(ctx)
	if err != nil {
		return fail("Failed to validate source token", err.Error())
	}

	msg := fmt.Sprintf("Authenticated as %s", info.Name)
	if info.Workplace.Name != "" {
		msg = fmt.Sprintf("%s (%s)", msg, info.Workplace.Name)
	}
	return ok_(msg)
}

func (ch *Checker) checkSourceWorkplace(ctx context.Context) checkResult {
	wp, err := ch.source.GetWorkplace(ctx)
	if err != nil {
		return fail("Cannot access source workplace", err.Error())
	}

	return ok_(fmt.Sprintf("Workplace: %s", wp.Name))
}

func (ch *Checker) checkSourceProjects(ctx context.Context) checkResult {
	projects, err := ch.source.ListProjects(ctx)
	if err != nil {
		return fail("Cannot list source projects", err.Error())
	}

	if len(projects) == 0 {
		return warn("No projects found in source workplace", "The source workplace has no projects to migrate.")
	}

	return ok_(fmt.Sprintf("Found %d project(s)", len(projects)))
}

func (ch *Checker) checkTargetReachable(ctx context.Context) checkResult {
	if ch.devMode {
		return checkResult{Status: StatusSkipped, Message: "Skipped — Dev Mode", Detail: "Target checks are skipped in dev mode."}
	}

	if ch.target == nil {
		return fail("Target client not configured", "")
	}

	// Try to get token info as a connectivity check
	_, err := ch.target.GetTokenInfo(ctx)
	if err != nil {
		// If it's an auth error, the host is reachable
		var apiErr *doppler.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == 401 {
			return ok_("Target host is reachable (auth checked separately)")
		}
		return fail("Cannot reach target host", err.Error())
	}

	return ok_("Target host is reachable")
}

func (ch *Checker) checkTargetToken(ctx context.Context) checkResult {
	if ch.devMode {
		return checkResult{Status: StatusSkipped, Message: "Skipped — Dev Mode", Detail: "Target checks are skipped in dev mode."}
	}

	if ch.target == nil {
		return fail("Target client not configured", "")
	}

	info, err := ch.target.GetTokenInfo(ctx)
	if err != nil {
		return fail("Failed to validate target token", err.Error())
	}

	msg := fmt.Sprintf("Authenticated as %s", info.Name)
	if info.Workplace.Name != "" {
		msg = fmt.Sprintf("%s (%s)", msg, info.Workplace.Name)
	}
	return ok_(msg)
}

func (ch *Checker) checkTargetWorkplace(ctx context.Context) checkResult {
	if ch.devMode {
		return checkResult{Status: StatusSkipped, Message: "Skipped — Dev Mode", Detail: "Target checks are skipped in dev mode."}
	}

	if ch.target == nil {
		return fail("Target client not configured", "")
	}

	wp, err := ch.target.GetWorkplace(ctx)
	if err != nil {
		return fail("Cannot access target workplace", err.Error())
	}

	return ok_(fmt.Sprintf("Workplace: %s", wp.Name))
}

func (ch *Checker) checkTargetWrite(ctx context.Context) checkResult {
	if ch.devMode {
		return checkResult{Status: StatusSkipped, Message: "Skipped — Dev Mode", Detail: "Target checks are skipped in dev mode."}
	}

	if ch.target == nil {
		return fail("Target client not configured", "")
	}

	// Listing proves the token reaches the API, not that it can write. Reading the
	// workplace's roles is the closest non-destructive proxy: the migration's first
	// real write is a workplace update, and a read-only token fails here rather
	// than partway through phase 1.
	if _, err := ch.target.ListProjects(ctx); err != nil {
		return fail("Cannot reach the target API", err.Error())
	}

	if _, err := ch.target.ListWorkplaceRoles(ctx); err != nil {
		return fail("Target token cannot read workplace roles",
			"The migration creates workplace roles, groups and service accounts. "+
				"A token without workplace access will fail in the first phase. "+err.Error())
	}

	return ok_("Target API access confirmed")
}

// restrictedSampleSize bounds how many configs the restricted-read check probes.
//
// Counting exactly would mean reading every config in the workplace, which on a
// large one is the whole gather done twice. Sampling is honest as long as the
// result says it is a sample, which is why CheckScope carries Estimated.
const restrictedSampleSize = 25

// checkSourceCanReadRestricted works out whether the source token can read
// restricted secrets, and how much it would cost if it cannot.
//
// The API decides this from the actor behind the token rather than its
// permissions: a user-backed token (dp.pt., or a dp.ct. CLI token) is limited to
// unmasked and masked secrets. Reading a config that holds a restricted secret
// fails for the whole config rather than returning the readable entries — so one
// restricted secret anywhere costs that entire config.
//
// A token that cannot read them is not on its own a problem: a workplace with no
// restricted secrets migrates perfectly well with a personal token. So rather
// than warn on the token type alone, this probes real configs and reports what
// the run would actually lose.
func (ch *Checker) checkSourceCanReadRestricted(ctx context.Context) checkResult {
	info, err := ch.source.GetTokenInfo(ctx)
	if err != nil {
		return failWith(err, "source", "Could not determine the source token type",
			"The check needs to know what kind of token this is before it can say what it may read.")
	}

	if info.Principal.Type != "workplace_user" {
		return ok_("Source token can read all secret visibilities")
	}

	sampled, denied, total, lastErr := ch.probeRestrictedReads(ctx)
	if sampled == 0 {
		// Nothing to probe, or the projects would not list. Fall back to saying
		// what the token is, without inventing a number.
		return warn("Source token cannot read restricted secrets",
			"This is a user-backed token. If any config contains a restricted secret, reading "+
				"that config fails entirely. Use a service account token (dp.sa.) for the source "+
				"if your workplace uses restricted secrets.")
	}

	if denied == 0 {
		return checkResult{
			Status:  StatusPassed,
			Message: "Source token read every config it was given",
			Detail: fmt.Sprintf("This is a user-backed token, which cannot read restricted secrets, "+
				"but none of the %d configs sampled contained any. If the workplace does hold "+
				"restricted secrets elsewhere, those configs will fail during the run.", sampled),
		}
	}

	estimated := scaleUp(denied, sampled, total)
	scope := &CheckScope{
		Affected:  estimated,
		Total:     total,
		Unit:      "configs",
		Estimated: sampled < total,
		Description: fmt.Sprintf("%d of %d configs contain restricted values and would fail during the run",
			estimated, total),
	}
	if scope.Estimated {
		scope.Description = fmt.Sprintf(
			"%d of %d configs sampled could not be read. At that rate roughly %d of %d configs would fail during the run.",
			denied, sampled, estimated, total)
	}

	res := checkResult{
		Status:  StatusFailed,
		Message: "Source token cannot read restricted secrets",
		Detail: "This is a user-backed token. Personal and CLI tokens cannot read secrets marked " +
			"restricted, and Doppler rejects the whole config rather than returning the readable " +
			"entries — so a config with one restricted value fails entirely, not partially.",
		Scope: scope,
		Fix: &CheckFix{
			Kind:     "token",
			Label:    "Save And Re-check",
			Field:    "source_token",
			Hint:     "Create a service account token (dp.sa.) in the source workplace with read access to the affected projects.",
			Blocking: true,
		},
	}
	if lastErr != nil {
		ev := doppler.EvidenceFrom(lastErr, "source")
		res.Evidence = &ev
	}
	return res
}

// probeRestrictedReads reads a sample of configs and counts how many come back
// denied. Returns the sample size, the denials, the total configs in the
// workplace, and the last denial seen so its exchange can be shown as evidence.
func (ch *Checker) probeRestrictedReads(ctx context.Context) (sampled, denied, total int, lastErr error) {
	projects, err := ch.source.ListProjects(ctx)
	if err != nil {
		return 0, 0, 0, err
	}

	type target struct{ project, config string }
	var candidates []target

	for _, project := range projects {
		if ctx.Err() != nil {
			return sampled, denied, total, lastErr
		}
		configs, err := ch.source.ListConfigs(ctx, project.Slug)
		if err != nil {
			continue
		}
		for _, cfg := range configs {
			total++
			// Personal configs are invisible to any other token and are not
			// migrated, so a denial on one says nothing about restricted secrets.
			if doppler.IsPersonalConfig(cfg.Name) {
				continue
			}
			candidates = append(candidates, target{project.Slug, cfg.Name})
		}
	}

	for i, c := range candidates {
		// Spread the sample across the list rather than taking the first few, so
		// one unusual project cannot stand in for the whole workplace.
		if len(candidates) > restrictedSampleSize && i%(len(candidates)/restrictedSampleSize) != 0 {
			continue
		}
		if sampled >= restrictedSampleSize || ctx.Err() != nil {
			break
		}
		sampled++
		if _, err := ch.source.ListSecrets(ctx, c.project, c.config); err != nil {
			var apiErr *doppler.APIError
			if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusForbidden {
				denied++
				lastErr = err
			}
		}
	}

	return sampled, denied, total, lastErr
}

// scaleUp extrapolates a sample to the whole population, never reporting more
// than the population itself.
func scaleUp(denied, sampled, total int) int {
	if sampled == 0 {
		return 0
	}
	est := denied * total / sampled
	if est > total {
		return total
	}
	if est == 0 && denied > 0 {
		return 1
	}
	return est
}

// AllPassed returns true if all checks passed (or were skipped/warning).
func AllPassed(checks []Check) bool {
	for _, c := range checks {
		if c.Status == StatusFailed {
			return false
		}
	}
	return true
}

// HasWarnings returns true if any check produced a warning. A warning does not
// block the migration, but it is something the operator should act on first —
// most importantly, users missing on the target.
func HasWarnings(checks []Check) bool {
	for _, c := range checks {
		if c.Status == StatusWarning {
			return true
		}
	}
	return false
}

// HasFailures returns true if any check failed.
func HasFailures(checks []Check) bool {
	for _, c := range checks {
		if c.Status == StatusFailed {
			return true
		}
	}
	return false
}

// checkUsersProvisioned compares the workplace users on each side.
//
// The Doppler API has no create-user endpoint, so users have to exist on the
// target before the migration runs — provisioned by SCIM or invited. Nothing in
// the migration can create them. If they are missing, every access control that
// names a user is silently skipped: group membership, project membership,
// workplace access levels and change request policy reviewers. The migration
// reports success and the target is left with no one able to reach anything.
//
// That is worth catching before any data is read, not discovered in the summary
// afterwards, so this runs in pre-flight rather than after the gather.
func (ch *Checker) checkUsersProvisioned(ctx context.Context) checkResult {
	if ch.devMode {
		return checkResult{Status: StatusSkipped, Message: "Skipped — Dev Mode", Detail: "Target checks are skipped in dev mode."}
	}
	if ch.target == nil {
		return fail("Target client not configured", "")
	}

	sourceUsers, err := ch.source.ListWorkplaceUsers(ctx)
	if err != nil {
		return warn("Could not list users on the source", err.Error())
	}
	targetUsers, err := ch.target.ListWorkplaceUsers(ctx)
	if err != nil {
		return warn("Could not list users on the target", err.Error())
	}

	// Email is the only identifier stable across instances.
	onTarget := make(map[string]bool, len(targetUsers))
	for _, u := range targetUsers {
		if u.User.Email != "" {
			onTarget[u.User.Email] = true
		}
	}

	var missing []string
	for _, u := range sourceUsers {
		if u.User.Email != "" && !onTarget[u.User.Email] {
			missing = append(missing, u.User.Email)
		}
	}

	if len(sourceUsers) == 0 {
		return ok_("No users on the source to provision")
	}
	if len(missing) == 0 {
		return ok_(fmt.Sprintf("All %d source user(s) exist on the target", len(sourceUsers)))
	}

	detail := fmt.Sprintf(
		"%d of %d source users are missing on the target: %s.\n\n"+
			"The Doppler API cannot create users, so this migration will not create them either. "+
			"Every access control naming a missing user is skipped — group and project membership, "+
			"workplace access levels, and change request policy reviewers — and the migration still "+
			"reports success.\n\n"+
			"Provision them with SCIM (or invite them) on the target, then re-check.",
		len(missing), len(sourceUsers), summarizeEmails(missing))

	if len(targetUsers) == 0 {
		return warn("No users on the target — set up SCIM before migrating", detail)
	}
	return warn(fmt.Sprintf("%d of %d users are missing on the target", len(missing), len(sourceUsers)), detail)
}

// summarizeEmails lists a handful of addresses rather than every one.
func summarizeEmails(emails []string) string {
	const show = 5
	if len(emails) <= show {
		return strings.Join(emails, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(emails[:show], ", "), len(emails)-show)
}

// checkTargetConfigLogs verifies the target token can read config logs.
//
// The logs are how a later run tells "the source changed" from "somebody
// changed the target after we wrote it". Without them a top-up will overwrite a
// target-side edit without ever mentioning it, which is the one failure mode
// nobody would find out about until the value was already gone.
//
// A warning rather than a failure: a first migration does not need drift
// detection, and the run is still correct without it. What it must not do is
// discover this at compare time and report no drift.
func (ch *Checker) checkTargetConfigLogs(ctx context.Context) checkResult {
	if ch.devMode {
		return checkResult{Status: StatusSkipped, Message: "Skipped — Dev Mode", Detail: "Target checks are skipped in dev mode."}
	}
	if ch.target == nil {
		return checkResult{Status: StatusSkipped, Message: "No target configured"}
	}

	project, config, ok := ch.anyTargetConfig(ctx)
	if !ok {
		// Nothing on the target to read a log for, which is the ordinary state
		// of a first migration.
		return ok_("Nothing on the target to check yet")
	}

	// A zero time asks for the whole history; one page is enough to know whether
	// the endpoint answers at all.
	if _, err := ch.target.ListConfigLogsSince(ctx, project, config, time.Time{}); err != nil {
		var apiErr *doppler.APIError
		if errors.As(err, &apiErr) && (apiErr.StatusCode == http.StatusForbidden || apiErr.StatusCode == http.StatusUnauthorized) {
			ev := doppler.EvidenceFrom(err, "target")
			return checkResult{
				Status:  StatusWarning,
				Message: "Target token cannot read config logs",
				Detail: "Config logs are how a later run tells a source change from an edit made on the target. " +
					"Without them, a top-up overwrites a target-side edit without warning. Give this token log read " +
					"access if you plan to run more than once.",
				Evidence: &ev,
				Fix: &CheckFix{
					Kind:     "token",
					Label:    "Save And Re-check",
					Field:    "target_token",
					Hint:     "A target token with log read access, if you intend to top up before cutting over.",
					Blocking: false,
				},
			}
		}
		return warn("Could not read config logs on the target", err.Error())
	}

	return ok_("Target config logs are readable")
}

// checkSourcePermissions tries, on one source project and config, the reads
// that need a permission with no read-only form — webhooks, project members,
// service tokens, trusted IPs — and the change request policies a plan may not
// include. A refusal does not stop the migration; the gather records it and
// leaves that kind of thing out. Said here, before the gather, so it is a
// choice rather than a surprise at the summary.
func (ch *Checker) checkSourcePermissions(ctx context.Context) checkResult {
	project, config, ok := anyConfig(ctx, ch.source)
	if !ok {
		return ok_("Nothing on the source to check")
	}

	probes := []struct {
		label string
		call  func() error
	}{
		{"webhooks", func() error { _, err := ch.source.ListWebhooks(ctx, project); return err }},
		{"project members", func() error { _, err := ch.source.ListProjectMembers(ctx, project); return err }},
		{"service tokens", func() error { _, err := ch.source.ListServiceTokens(ctx, project, config); return err }},
		{"trusted IP ranges", func() error { _, err := ch.source.ListTrustedIPs(ctx, project, config); return err }},
		{"change request policies", func() error { _, err := ch.source.ListChangeRequestPolicies(ctx); return err }},
	}

	var refused []string
	var lastErr error
	for _, p := range probes {
		if err := p.call(); err != nil {
			var apiErr *doppler.APIError
			if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusForbidden {
				refused = append(refused, p.label)
				lastErr = err
			}
		}
	}
	if len(refused) == 0 {
		return ok_("The source token can read everything the migration uses")
	}

	ev := doppler.EvidenceFrom(lastErr, "source")
	return checkResult{
		Status:  StatusWarning,
		Message: fmt.Sprintf("Source token cannot read %s", joinList(refused)),
		Detail: "The migration carries on without them, and they are not migrated — the summary lists them. " +
			"Webhooks, project members, service tokens and trusted IP ranges can only be read with Doppler's " +
			"\"manage\" permissions for them; the migrator never writes to the source. Change request policies " +
			"may also be missing from the workplace's plan. The Source Token Permissions page lists what to grant.",
		Evidence: &ev,
	}
}

// joinList writes "a", "a and b", or "a, b and c".
func joinList(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

// anyConfig finds one non-personal config on an instance to probe with.
func anyConfig(ctx context.Context, c *doppler.Client) (project, config string, ok bool) {
	projects, err := c.ListProjects(ctx)
	if err != nil {
		return "", "", false
	}
	for _, p := range projects {
		configs, err := c.ListConfigs(ctx, p.Slug)
		if err != nil {
			continue
		}
		for _, cfg := range configs {
			if !doppler.IsPersonalConfig(cfg.Name) {
				return p.Slug, cfg.Name, true
			}
		}
	}
	return "", "", false
}

// anyTargetConfig finds one config on the target to probe with. Returns false
// when the target is empty, which is not a failure.
func (ch *Checker) anyTargetConfig(ctx context.Context) (project, config string, ok bool) {
	projects, err := ch.target.ListProjects(ctx)
	if err != nil {
		return "", "", false
	}
	for _, p := range projects {
		configs, err := ch.target.ListConfigs(ctx, p.Slug)
		if err != nil {
			continue
		}
		for _, c := range configs {
			if !doppler.IsPersonalConfig(c.Name) {
				return p.Slug, c.Name, true
			}
		}
	}
	return "", "", false
}
