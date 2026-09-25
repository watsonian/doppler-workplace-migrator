package migration

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/doppler/workplace-migrator/internal/doppler"
)

// TrustedIPWork is what mirroring one config's trusted ranges will do.
type TrustedIPWork struct {
	Project string   `json:"project"`
	Config  string   `json:"config"`
	Scope   string   `json:"scope"`
	Add     []string `json:"add,omitempty"`
	Remove  []string `json:"remove,omitempty"`
	// ClosesConfig is true when the mirror removes the allow-all default, which
	// is the moment a migrator outside the remaining ranges loses access.
	ClosesConfig bool `json:"closes_config"`
}

// CutoverPlan is everything a cutover will do, worked out before it does any of
// it so the scope can be stated and agreed to.
type CutoverPlan struct {
	// TrustedIPs are the configs whose ranges do not match the source.
	TrustedIPs []TrustedIPWork `json:"trusted_ips"`
	// AlreadyMatching counts the configs needing nothing. Reported because "648
	// configs need nothing" is what tells you the blast radius of this step is
	// the other three.
	AlreadyMatching int `json:"already_matching"`
	// Matching names them, for the list behind the count.
	Matching []string `json:"matching"`
	// Unreadable are configs whose target ranges could not be read, so
	// nothing is planned for them rather than guessing at what is there.
	Unreadable []string `json:"unreadable"`
	// NotOnTarget counts source configs the target does not have yet, left
	// out of the plan.
	NotOnTarget int `json:"not_on_target"`
	// ClosingCount is how many configs this locks the migrator out of.
	ClosingCount int `json:"closing_count"`

	// MigratorCIDR, when set, is kept in every closing config's list for the
	// duration and removed by the final step.
	MigratorCIDR string `json:"migrator_cidr,omitempty"`
}

// NeedsSelfTrust reports whether this cutover would lock the migrator out of
// anything, and therefore whether an address is worth supplying.
func (p *CutoverPlan) NeedsSelfTrust() bool { return p.ClosingCount > 0 }

// Empty reports whether there is nothing to do.
func (p *CutoverPlan) Empty() bool {
	return len(p.TrustedIPs) == 0
}

// PlanCutover works out the trusted-IP mirror. It removes nothing else:
// deleting what the source no longer has is a top-up option, chosen on Review.
//
// A config whose source list is only the allow-all default has never been
// restricted and produces no work, which is why most workplaces reach this
// screen with nothing on it.
func PlanCutover(data *MigrationData, snap *TargetSnapshot, migratorCIDR string) *CutoverPlan {
	// Empty rather than nil, so each list reaches the browser as [] and not null.
	// A workplace with no trusted-IP restrictions and nothing to delete — most of
	// them — otherwise sent null for all three, and the page, reading .length,
	// crashed to a blank screen.
	plan := &CutoverPlan{
		MigratorCIDR: migratorCIDR,
		TrustedIPs:   []TrustedIPWork{},
		Matching:     []string{},
		Unreadable:   []string{},
	}

	for i := range data.Projects {
		pd := &data.Projects[i]
		ps, hasProject := snap.Projects[pd.Project.Slug]

		for j := range pd.Configs {
			cd := &pd.Configs[j]
			source := doppler.NewTrustedIPSet(cd.TrustedIPs)
			if len(source) == 0 {
				continue
			}

			scope := pd.Project.Slug + "/" + cd.Config.Name
			// A config the target does not have — its project not migrated yet —
			// has no ranges to mirror, and nothing to write them to.
			if snap.Listed {
				if _, ok := ps.Configs[cd.Config.Name]; !hasProject || !ok {
					plan.NotOnTarget++
					continue
				}
			}
			var target doppler.TrustedIPSet
			if hasProject {
				if cs, ok := ps.Configs[cd.Config.Name]; ok {
					if cs.TrustedIPsUnread {
						plan.Unreadable = append(plan.Unreadable, scope)
						continue
					}
					target = cs.TrustedIPs
				}
			}

			// The migrator's own address is kept through the mirror. It is by
			// definition not in the source, so an unqualified mirror would strip
			// it and lock the run out of the config it is in the middle of.
			work := doppler.PlanTrustedIPs(source, target, migratorCIDR)
			if work.Empty() {
				plan.AlreadyMatching++
				plan.Matching = append(plan.Matching, scope)
				continue
			}

			entry := TrustedIPWork{
				Project:      pd.Project.Slug,
				Config:       cd.Config.Name,
				Scope:        scope,
				Add:          work.Add,
				Remove:       work.Remove,
				ClosesConfig: work.RemovesAllowAll(),
			}
			if entry.ClosesConfig {
				plan.ClosingCount++
			}
			plan.TrustedIPs = append(plan.TrustedIPs, entry)
		}
	}

	sort.Slice(plan.TrustedIPs, func(i, j int) bool { return plan.TrustedIPs[i].Scope < plan.TrustedIPs[j].Scope })
	sort.Strings(plan.Matching)
	sort.Strings(plan.Unreadable)

	return plan
}

// CutoverResult is what a cutover did, including what it has not undone yet.
type CutoverResult struct {
	ConfigsMirrored int `json:"configs_mirrored"`
	RangesAdded     int `json:"ranges_added"`
	RangesRemoved   int `json:"ranges_removed"`
	Failed          int `json:"failed"`

	// SelfTrustOutstanding lists the configs still carrying the migrator's
	// address because removing it failed or has not run yet.
	//
	// This is the field that decides whether a cutover reports as finished.
	// Trusted IPs are bare CIDR strings with nowhere to put a label, so an
	// address left behind is indistinguishable from one somebody meant to add.
	SelfTrustOutstanding []string `json:"self_trust_outstanding,omitempty"`
	MigratorCIDR         string   `json:"migrator_cidr,omitempty"`

	FinishedAt time.Time `json:"finished_at"`
}

// Complete reports whether the cutover left nothing behind. A cutover with the
// migrator's address still in a config is not finished, however well the rest
// of it went.
func (r *CutoverResult) Complete() bool {
	return r.Failed == 0 && len(r.SelfTrustOutstanding) == 0
}

// Cutover applies a cutover plan.
type Cutover struct {
	target     DopplerReadWriter
	plan       *CutoverPlan
	errors     *ErrorStore
	onProgress func(MigrateProgress)
}

// NewCutover creates a cutover over a plan.
func NewCutover(target DopplerReadWriter, plan *CutoverPlan, errors *ErrorStore, onProgress func(MigrateProgress)) *Cutover {
	if onProgress == nil {
		onProgress = func(MigrateProgress) {}
	}
	if errors == nil {
		errors = NewErrorStore()
	}
	return &Cutover{target: target, plan: plan, errors: errors, onProgress: onProgress}
}

// Run mirrors the trusted IPs, keeping the migrator's own address in place
// throughout, then removes it.
//
// The order is the whole point. Restrict first so the target is protected from
// the moment the step runs, keep one hole for the machine doing the work, and
// close that hole last.
func (c *Cutover) Run(ctx context.Context) (*CutoverResult, error) {
	result := &CutoverResult{MigratorCIDR: c.plan.MigratorCIDR}
	defer func() { result.FinishedAt = time.Now().UTC() }()

	selfTrusted := make([]string, 0, len(c.plan.TrustedIPs))

	for _, work := range c.plan.TrustedIPs {
		if err := ctx.Err(); err != nil {
			result.SelfTrustOutstanding = selfTrusted
			return result, err
		}

		c.emit(work.Scope, "started", "")

		// Add before removing, always. A config left with neither its old ranges
		// nor its new ones is closed to everyone including whoever has to fix it.
		added := 0
		failed := false
		for _, ip := range work.Add {
			if err := c.target.AddTrustedIP(ctx, work.Project, work.Config, ip); err != nil && !isAlreadyExistsError(err) {
				c.fail(work, fmt.Sprintf("add %s", ip), err)
				result.Failed++
				failed = true
				break
			}
			added++
		}
		if failed {
			continue
		}

		// Self-trust goes on before the default comes off, so there is never an
		// instant where this migrator has no way back into the config.
		if c.plan.MigratorCIDR != "" && work.ClosesConfig {
			if err := c.target.AddTrustedIP(ctx, work.Project, work.Config, c.plan.MigratorCIDR); err != nil && !isAlreadyExistsError(err) {
				c.fail(work, "add the migrator's own address", err)
				result.Failed++
				continue
			}
			selfTrusted = append(selfTrusted, work.Scope)
		}

		removed := 0
		for _, ip := range work.Remove {
			if err := c.target.RemoveTrustedIP(ctx, work.Project, work.Config, ip); err != nil {
				var apiErr *doppler.APIError
				if !errors.As(err, &apiErr) || apiErr.StatusCode != 404 {
					c.fail(work, fmt.Sprintf("remove %s", ip), err)
					result.Failed++
					failed = true
					break
				}
			}
			removed++
		}
		if failed {
			continue
		}

		result.ConfigsMirrored++
		result.RangesAdded += added
		result.RangesRemoved += removed
		c.emit(work.Scope, "completed", "")
	}

	result.SelfTrustOutstanding = c.removeSelfTrust(ctx, selfTrusted, result)
	return result, nil
}

// removeSelfTrust closes the hole the cutover opened, and returns whatever it
// could not close.
func (c *Cutover) removeSelfTrust(ctx context.Context, scopes []string, result *CutoverResult) []string {
	if c.plan.MigratorCIDR == "" || len(scopes) == 0 {
		return nil
	}

	var outstanding []string
	for _, scope := range scopes {
		project, config, ok := splitScope(scope)
		if !ok {
			continue
		}
		if err := ctx.Err(); err != nil {
			return append(outstanding, scope)
		}

		c.emit(scope, "started", "removing the migrator's address")
		if err := c.target.RemoveTrustedIP(ctx, project, config, c.plan.MigratorCIDR); err != nil {
			var apiErr *doppler.APIError
			if errors.As(err, &apiErr) && apiErr.StatusCode == 404 {
				// Already gone, which is the desired state.
				continue
			}
			// Left behind, and it has to be findable: a bare CIDR in a trusted
			// list looks exactly like one somebody added deliberately.
			log.Printf("[error] cutover: %s still trusts %s: %v", scope, c.plan.MigratorCIDR, err)
			c.errors.Record(RecordInput{
				Err:           err,
				Side:          SideTarget,
				PhaseName:     "cutover",
				Resource:      "trusted_ips",
				ResourceLabel: "Trusted IPs",
				Item:          scope,
				Message: fmt.Sprintf("could not remove this migrator's address %s from %s — it still has access and the address will not be identifiable later",
					c.plan.MigratorCIDR, scope),
			})
			result.Failed++
			outstanding = append(outstanding, scope)
			continue
		}
		c.emit(scope, "completed", "removed the migrator's address")
	}
	return outstanding
}

func (c *Cutover) emit(scope, status, note string) {
	c.onProgress(MigrateProgress{
		Name: "cutover", Resource: "trusted_ips", Item: scope, Status: status, Error: note,
	})
}

func (c *Cutover) fail(work TrustedIPWork, what string, err error) {
	c.errors.Record(RecordInput{
		Err:           err,
		Side:          SideTarget,
		PhaseName:     "cutover",
		Resource:      "trusted_ips",
		ResourceLabel: "Trusted IPs",
		Item:          work.Scope,
		Project:       work.Project,
		Config:        work.Config,
		Message:       "could not " + what,
	})
	c.emit(work.Scope, "failed", "could not "+what)
}

func splitScope(scope string) (project, config string, ok bool) {
	for i := 0; i < len(scope); i++ {
		if scope[i] == '/' {
			return scope[:i], scope[i+1:], true
		}
	}
	return "", "", false
}
