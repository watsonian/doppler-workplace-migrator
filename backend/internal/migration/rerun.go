package migration

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/doppler/workplace-migrator/internal/doppler"
)

// RerunRequest is a remedy applied to a selection of failures.
//
// The selection is deliberately expressible two ways. Projects is what the
// interface offers, because filtering a class of several hundred by project is
// the only way a selection becomes reviewable; IDs is what it sends once
// individual rows have been unticked.
type RerunRequest struct {
	Class  ErrorClassKey `json:"class"`
	Remedy RemedyKind    `json:"remedy"`

	// Token replaces the credential for the side that failed. RemedyToken only.
	Token string `json:"token,omitempty"`
	// PaceMS is the gap to leave between requests. RemedyPace only.
	PaceMS int `json:"pace_ms,omitempty"`
	// Choice is the option taken for a decision-shaped remedy.
	Choice string `json:"choice,omitempty"`

	Projects []string `json:"projects,omitempty"`
	IDs      []string `json:"ids,omitempty"`
}

// ConfigRef names a config that has to be read from the source again.
type ConfigRef struct {
	Project string `json:"project"`
	Config  string `json:"config"`
}

// RerunPlan is what a remedy will do, worked out before it is done so the
// interface can state the scope and the caller can decide whether to go ahead.
type RerunPlan struct {
	Class  ErrorClassKey `json:"class"`
	Remedy RemedyKind    `json:"remedy"`

	// Records are the failures this will attempt to clear.
	Records []ErrorRecord `json:"-"`

	ItemCount int `json:"item_count"`
	// Weight totals what is at stake, e.g. how many secrets get rewritten.
	Weight int `json:"weight"`

	// RegatherConfigs are configs whose secrets must be read from the source again
	// before the re-run. Secret values are wiped from memory after every write
	// attempt, so a config that failed on the read has nothing left to send.
	RegatherConfigs []ConfigRef `json:"regather_configs,omitempty"`

	// NeedsRun is false when the remedy is settled without touching either
	// instance — keeping the target's version of something, for instance.
	NeedsRun bool `json:"needs_run"`

	// Resolution is the sentence recorded against the class once this succeeds.
	Resolution string `json:"resolution"`

	// Side names which instance the failing requests went to, so the caller knows
	// which client a replacement token belongs to.
	Side string `json:"side"`
}

// PrepareRerun works out what a remedy would do and walks the checkpoint back for
// the affected items, so the next Run retries exactly those.
//
// It does not change either instance and it does not run anything. The caller
// applies the infrastructure half of the remedy — a replacement client, a slower
// pace — because those belong to whoever built the clients.
func (e *Executor) PrepareRerun(req RerunRequest) (*RerunPlan, error) {
	plan, err := e.PlanRerun(req)
	if err != nil {
		return nil, err
	}
	if !plan.NeedsRun {
		// Nothing will re-run, so the checkpoint must be left exactly as it is.
		return plan, nil
	}

	byPhase := map[string][]string{}
	for _, rec := range plan.Records {
		if rec.CheckpointPhase == "" || rec.CheckpointKey == "" {
			continue
		}
		byPhase[rec.CheckpointPhase] = append(byPhase[rec.CheckpointPhase], rec.CheckpointKey)
	}
	for phase, keys := range byPhase {
		e.checkpoint.ResetItems(phase, keys)
	}

	return plan, nil
}

// PlanRerun works out what a remedy would do without changing anything at all.
//
// Separate from PrepareRerun because the interface asks this question before it
// asks the user to agree to it — and a dry run that walked the checkpoint back
// would leave the migration altered by a question nobody answered.
func (e *Executor) PlanRerun(req RerunRequest) (*RerunPlan, error) {
	if e.errors == nil {
		return nil, fmt.Errorf("no errors have been recorded")
	}
	if req.Class == "" {
		return nil, fmt.Errorf("no error class given")
	}

	class := LookupErrorClass(req.Class)
	if req.Remedy == "" {
		req.Remedy = class.Remedy.Kind
	}
	if req.Remedy != class.Remedy.Kind {
		return nil, fmt.Errorf("%s is fixed with %s, not %s", class.Title, class.Remedy.Kind, req.Remedy)
	}
	if class.Remedy.Kind == RemedyNone {
		return nil, fmt.Errorf("%s needs no action and cannot be re-run", class.Title)
	}
	if err := validateRemedyInput(req, class); err != nil {
		return nil, err
	}

	records := e.errors.Records(RecordFilter{
		Class:    req.Class,
		Projects: req.Projects,
		IDs:      req.IDs,
		OpenOnly: true,
	})
	if len(records) == 0 {
		return nil, fmt.Errorf("nothing open matches that selection")
	}

	plan := &RerunPlan{
		Class:      req.Class,
		Remedy:     req.Remedy,
		Records:    records,
		ItemCount:  len(records),
		NeedsRun:   true,
		Resolution: describeResolution(req, class, len(records)),
		Side:       records[0].Evidence.Side,
	}
	for _, rec := range records {
		plan.Weight += rec.Weight
	}

	// Keeping the target's version settles the failure without writing anything.
	// Recording the divergence is the whole remedy.
	if req.Remedy == RemedyDecision && req.Choice == "keep_target" {
		plan.NeedsRun = false
		return plan, nil
	}

	// A config whose secrets were never read has nothing in memory to write, so
	// the read has to happen again before the re-run can do anything.
	if needsRegather(req.Class) {
		plan.RegatherConfigs = configRefs(records)
	}

	return plan, nil
}

// FinishRerun marks as resolved the records whose items completed this time, and
// leaves the rest open.
//
// Success is read back from the checkpoint rather than assumed, so a re-run that
// fails the same way again does not quietly report itself as a fix.
func (e *Executor) FinishRerun(plan *RerunPlan) int {
	if plan == nil || e.errors == nil {
		return 0
	}

	if !plan.NeedsRun {
		return e.errors.Resolve(RecordFilter{Class: plan.Class, IDs: recordIDs(plan.Records)}, plan.Resolution)
	}

	cleared := []string{}
	for _, rec := range plan.Records {
		if rec.CheckpointPhase == "" || rec.CheckpointKey == "" {
			continue
		}
		if e.checkpoint.GetItemStatus(rec.CheckpointPhase, rec.CheckpointKey) == StatusCompleted {
			cleared = append(cleared, rec.ID)
		}
	}
	if len(cleared) == 0 {
		return 0
	}
	return e.errors.Resolve(RecordFilter{Class: plan.Class, IDs: cleared}, plan.Resolution)
}

// ReplaceSource swaps the client the executor reads the source with, for a remedy
// that supplies a better token.
func (e *Executor) ReplaceSource(c DopplerClient) {
	if c != nil {
		e.source = c
	}
}

// ReplaceTarget swaps the client the executor writes the target with.
func (e *Executor) ReplaceTarget(rw DopplerReadWriter) {
	if rw != nil {
		e.target = rw
	}
}

// validateRemedyInput checks the caller supplied what the remedy needs. Doing it
// here means a malformed request is refused before the checkpoint is disturbed.
func validateRemedyInput(req RerunRequest, class ErrorClass) error {
	switch req.Remedy {
	case RemedyToken:
		if req.Token == "" {
			return fmt.Errorf("%s needs a replacement token", class.Title)
		}
	case RemedyPace:
		if req.PaceMS <= 0 {
			return fmt.Errorf("re-running slower needs a gap between requests")
		}
		if req.PaceMS > 60_000 {
			return fmt.Errorf("a gap of %dms between requests would take longer than waiting out the limit", req.PaceMS)
		}
	case RemedyDecision:
		if req.Choice == "" {
			return fmt.Errorf("%s needs a choice", class.Title)
		}
		for _, c := range class.Remedy.Choices {
			if c.Value == req.Choice {
				return nil
			}
		}
		return fmt.Errorf("%q is not one of the choices for %s", req.Choice, class.Title)
	}
	return nil
}

// describeResolution writes the sentence that goes in the record and the summary.
// It names what was actually done, because "resolved" on its own tells whoever
// reads the export in three months nothing at all.
func describeResolution(req RerunRequest, class ErrorClass, n int) string {
	items := pluralItems(n)
	switch req.Remedy {
	case RemedyToken:
		return fmt.Sprintf("Supplied %s · %s re-run", tokenHintOf(req.Token), items)
	case RemedyPace:
		return fmt.Sprintf("Re-ran with %dms between requests · %s", req.PaceMS, items)
	case RemedyDecision:
		for _, c := range class.Remedy.Choices {
			if c.Value == req.Choice {
				return fmt.Sprintf("%s · %s", c.Label, items)
			}
		}
		return fmt.Sprintf("%s · %s", req.Choice, items)
	default:
		return fmt.Sprintf("Re-checked and re-ran · %s", items)
	}
}

func pluralItems(n int) string {
	if n == 1 {
		return "1 item"
	}
	return fmt.Sprintf("%d items", n)
}

// needsRegather reports whether clearing this class means reading the source
// again. Anything that failed on the read has nothing cached to rewrite.
func needsRegather(class ErrorClassKey) bool {
	switch class {
	case ClassRestrictedRead, ClassStaleGather:
		return true
	}
	return false
}

// configRefs collects the distinct configs a set of records touched, sorted so a
// re-gather runs in a predictable order.
func configRefs(records []ErrorRecord) []ConfigRef {
	seen := map[ConfigRef]struct{}{}
	for _, rec := range records {
		if rec.Project == "" || rec.Config == "" {
			continue
		}
		seen[ConfigRef{Project: rec.Project, Config: rec.Config}] = struct{}{}
	}
	out := make([]ConfigRef, 0, len(seen))
	for ref := range seen {
		out = append(out, ref)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Project != out[j].Project {
			return out[i].Project < out[j].Project
		}
		return out[i].Config < out[j].Config
	})
	return out
}

func recordIDs(records []ErrorRecord) []string {
	out := make([]string, 0, len(records))
	for _, rec := range records {
		out = append(out, rec.ID)
	}
	return out
}

// RerunResult is what happened, for the response and the log.
type RerunResult struct {
	Class      ErrorClassKey `json:"class"`
	Attempted  int           `json:"attempted"`
	Resolved   int           `json:"resolved"`
	StillOpen  int           `json:"still_open"`
	Resolution string        `json:"resolution"`
	FinishedAt time.Time     `json:"finished_at"`
}

// tokenHintOf renders a token for the record. Resolutions are exported and read
// by people who were not in the room, so the token is named but never shown.
func tokenHintOf(token string) string {
	return doppler.TokenHint(token)
}

// RegatherConfigs reads the named configs' secrets from the source again.
//
// Secret values are wiped from memory after every write attempt, so a config that
// failed on the read has nothing left to send. Re-running it without reading it
// again would write an empty config and call that a success, which is the one
// outcome worse than the original failure.
func (e *Executor) RegatherConfigs(ctx context.Context, refs []ConfigRef) error {
	if len(refs) == 0 {
		return nil
	}

	wanted := make(map[ConfigRef]struct{}, len(refs))
	for _, ref := range refs {
		wanted[ref] = struct{}{}
	}

	var failed []string
	for i := range e.data.Projects {
		pd := &e.data.Projects[i]
		for j := range pd.Configs {
			cd := &pd.Configs[j]
			ref := ConfigRef{Project: pd.Project.Slug, Config: cd.Config.Name}
			if _, ok := wanted[ref]; !ok {
				continue
			}
			if err := ctx.Err(); err != nil {
				return err
			}

			secrets, err := e.source.ListSecrets(ctx, ref.Project, ref.Config)
			if err != nil {
				// One config failing to re-read does not invalidate the rest; it
				// simply stays open, and its record says why.
				failed = append(failed, fmt.Sprintf("%s/%s: %v", ref.Project, ref.Config, err))
				continue
			}
			// DOPPLER_ secrets are system-managed and cannot be written back, the
			// same filter the first gather applies.
			for name := range secrets {
				if strings.HasPrefix(name, "DOPPLER_") {
					delete(secrets, name)
				}
			}
			cd.Secrets = secrets
			cd.SecretCount = len(secrets)
		}
	}

	if len(failed) == len(refs) {
		return fmt.Errorf("could not re-read any of the %d configs: %s", len(refs), strings.Join(failed, "; "))
	}
	if len(failed) > 0 {
		log.Printf("[warn] re-gather: %d of %d configs still unreadable: %s",
			len(failed), len(refs), strings.Join(failed, "; "))
	}
	return nil
}
