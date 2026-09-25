package migration

import (
	"fmt"
	"strings"
)

// emit publishes a progress update, filing it in the error store first when it
// reports a failure.
//
// Every failure in the executor goes through here rather than through
// twenty-five separate recording calls. That is deliberate: the phases are long
// and repetitive, and a store that has to be remembered at each site is a store
// that ends up with gaps in it.
func (e *Executor) emit(p MigrateProgress) {
	newClass := false
	if p.Status == "failed" && e.errors != nil {
		newClass = e.errors.IsNewClass(classOf(p))
		p = e.fileFailure(p)
	}
	e.onProgress(p)

	// Pausing on the first failure of a kind is the difference between finding
	// out about a bad token after one config and after six hundred. Repeats of a
	// class already seen do not pause: by then the operator has been told, and
	// stopping again on every one of two hundred identical failures would make
	// the run unusable.
	if newClass && e.stopOnNewErrorClass && !e.IsPaused() {
		e.Pause()
		e.onProgress(MigrateProgress{
			Phase:      p.Phase,
			Name:       p.Name,
			Status:     "paused",
			Error:      fmt.Sprintf("Paused: %s is a failure this run has not seen before.", LookupErrorClass(p.ErrorClass).Title),
			ErrorClass: p.ErrorClass,
		})
	}
}

// classOf works out which class a failure would be filed under, without filing
// it. Needed to tell a first occurrence from a repeat, since filing it first
// would make every class look like one already seen.
func classOf(p MigrateProgress) ErrorClassKey {
	if p.class != "" {
		return p.class
	}
	return ClassifyError(p.err, "")
}

// fileFailure records a failed progress event and returns it annotated with the
// class it was filed under.
func (e *Executor) fileFailure(p MigrateProgress) MigrateProgress {
	project, config := splitItem(p.Resource, p.Item)

	rec := e.errors.Record(RecordInput{
		Err:           p.err,
		Side:          e.sideFor(p),
		Phase:         p.Phase,
		PhaseName:     p.Name,
		Resource:      p.Resource,
		ResourceLabel: resourceLabel(p.Resource),
		Item:          p.Item,
		Project:       project,
		Config:        config,
		Size:          e.sizeOf(project, config),
		Weight:        e.weightOf(p.Resource, project, config),
		Class:         p.class,
		Message:       p.Error,

		CheckpointPhase: p.ckPhase,
		CheckpointKey:   p.ckKey,
	})

	p.ErrorClass = rec.Class
	p.ErrorID = rec.ID
	return p
}

// sideFor works out which instance a failure came from.
//
// The URL on the error is the reliable signal — it is the actual request that
// failed, so it cannot disagree with reality the way a hand-set flag at each call
// site eventually would. Without a URL to go on, the phase decides: only the
// secrets phase reads from the source while writing, so everything else is a
// write to the target.
func (e *Executor) sideFor(p MigrateProgress) string {
	ev := EvidenceFrom(p.err, "")
	if ev.URL != "" {
		if e.sourceBaseURL != "" && strings.HasPrefix(ev.URL, e.sourceBaseURL) {
			return SideSource
		}
		if e.targetBaseURL != "" && strings.HasPrefix(ev.URL, e.targetBaseURL) {
			return SideTarget
		}
	}
	return SideTarget
}

// splitItem pulls a project and config out of the item label. Config-scoped
// phases label items "project/config"; everything else is named on its own.
func splitItem(resource, item string) (project, config string) {
	switch resource {
	case "configs", "secrets", "locks", "inheritance", "trusted_ips":
		if p, c, ok := strings.Cut(item, "/"); ok {
			return p, c
		}
		return item, ""
	case "webhooks":
		// Webhooks are labelled "project: url".
		if p, _, ok := strings.Cut(item, ":"); ok {
			return strings.TrimSpace(p), ""
		}
	case "environments":
		if p, _, ok := strings.Cut(item, "/"); ok {
			return p, ""
		}
	case "projects":
		return item, ""
	}
	return "", ""
}

// resourceLabel is how a resource kind reads in a table heading.
func resourceLabel(resource string) string {
	switch resource {
	case "secrets":
		return "Secrets"
	case "configs":
		return "Configs"
	case "projects":
		return "Projects"
	case "environments":
		return "Environments"
	case "groups":
		return "Groups"
	case "service_accounts":
		return "Service accounts"
	case "roles":
		return "Roles"
	case "webhooks":
		return "Webhooks"
	case "members":
		return "Members"
	case "policies":
		return "Policies"
	case "trusted_ips":
		return "Trusted IPs"
	case "inheritance":
		return "Inheritance"
	case "locks":
		return "Config locks"
	case "workplace":
		return "Workplace"
	default:
		if resource == "" {
			return "Other"
		}
		return strings.ToUpper(resource[:1]) + resource[1:]
	}
}

// sizeOf describes what is at stake for a config, e.g. "128 secrets". Shown beside
// the item in triage so a selection is made knowing what it covers.
func (e *Executor) sizeOf(project, config string) string {
	n := e.secretCount(project, config)
	if n <= 0 {
		return ""
	}
	if n == 1 {
		return "1 secret"
	}
	return fmt.Sprintf("%d secrets", n)
}

// weightOf is the same thing as a number, so a selection can be totalled. Only
// secret-bearing resources carry weight; one failed group is not comparable to
// three hundred secrets and should not be summed with them.
func (e *Executor) weightOf(resource, project, config string) int {
	switch resource {
	case "secrets", "configs":
		return e.secretCount(project, config)
	}
	return 0
}

func (e *Executor) secretCount(project, config string) int {
	if project == "" || config == "" || e.data == nil {
		return 0
	}
	for i := range e.data.Projects {
		pd := &e.data.Projects[i]
		if pd.Project.Slug != project {
			continue
		}
		for j := range pd.Configs {
			cd := &pd.Configs[j]
			if cd.Config.Name == config {
				if cd.SecretCount > 0 {
					return cd.SecretCount
				}
				return len(cd.Secrets)
			}
		}
	}
	return 0
}

// Errors exposes the error store so the API can serve triage without reaching
// into the executor's internals.
func (e *Executor) Errors() *ErrorStore { return e.errors }
