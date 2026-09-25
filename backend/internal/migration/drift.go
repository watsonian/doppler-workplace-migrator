package migration

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/doppler/onprem-migrator/internal/doppler"
)

// DriftDetector finds changes made to the target by somebody other than this
// migrator, after this migrator wrote.
//
// It reads the instance's own config logs rather than comparing against a
// record of what was written. The record would be a file of digests, one per
// secret, whose entire purpose is answering "is this the value of that secret?"
// — for a workplace full of values like "true", "production" and a port number.
// The instance already logs the change, with a timestamp and an actor, and the
// actor is the more useful answer: not that the target moved, but who moved it.
// A colleague editing a webhook secret is a conversation; a provisioning tool
// writing to a config means something else owns it, and overwriting starts a
// fight the migrator loses on the next apply.
type DriftDetector struct {
	target DopplerLogReader
	// selfIdentities are the names this migrator acts under. A change we made is
	// not drift, and the token can be replaced mid-run, so this is a set.
	selfIdentities map[string]struct{}
}

// DopplerLogReader is the part of the client drift needs.
type DopplerLogReader interface {
	ListConfigLogsSince(ctx context.Context, project, config string, since time.Time) ([]doppler.ConfigLogEntry, error)
}

// NewDriftDetector creates a detector that treats the given identities as its own.
func NewDriftDetector(target DopplerLogReader, selfIdentities ...string) *DriftDetector {
	set := map[string]struct{}{}
	for _, id := range selfIdentities {
		if id = strings.TrimSpace(strings.ToLower(id)); id != "" {
			set[id] = struct{}{}
		}
	}
	return &DriftDetector{target: target, selfIdentities: set}
}

// AddIdentity registers another name this migrator acts under, for a token
// swapped in partway through a run.
func (d *DriftDetector) AddIdentity(identity string) {
	if identity = strings.TrimSpace(strings.ToLower(identity)); identity != "" {
		d.selfIdentities[identity] = struct{}{}
	}
}

// ErrLogsUnavailable means drift could not be determined. It is deliberately
// distinct from "no drift found": reporting the two the same way would let a
// permissions problem read as a clean bill of health.
var ErrLogsUnavailable = errors.New("config logs are not readable")

// ConfigWrite is one config this migrator wrote, and when.
type ConfigWrite struct {
	Project string
	Config  string
	// WrittenAt is the instant after which any change by another actor is drift.
	WrittenAt time.Time
}

// Detect reads the logs for the given configs and returns what changed after we
// wrote, by somebody else.
//
// A failure to read any log at all is reported through DriftUnavailable rather
// than as an error, because a comparison that cannot answer the drift question
// is still a useful comparison — it just must not claim there is none.
func (d *DriftDetector) Detect(ctx context.Context, writes []ConfigWrite) ([]DriftRecord, string) {
	if len(writes) == 0 {
		return nil, ""
	}

	var records []DriftRecord
	var firstErr error
	failures := 0

	for _, w := range writes {
		if err := ctx.Err(); err != nil {
			return records, "Comparison was interrupted before every config could be checked."
		}

		entries, err := d.target.ListConfigLogsSince(ctx, w.Project, w.Config, w.WrittenAt)
		if err != nil {
			failures++
			if firstErr == nil {
				firstErr = err
			}
			continue
		}

		for _, entry := range entries {
			if d.isSelf(entry.User) {
				continue
			}
			records = append(records, DriftRecord{
				Project:   w.Project,
				Config:    w.Config,
				Scope:     w.Project + "/" + w.Config,
				ChangedAt: entry.CreatedAt.Format(time.RFC3339),
				ChangedBy: entry.User.Identity(),
				Summary:   strings.TrimSpace(entry.Text),
			})
		}
	}

	if failures == len(writes) {
		return nil, explainLogFailure(firstErr)
	}
	if failures > 0 {
		return records, fmt.Sprintf(
			"%d of %d configs could not be checked for changes made on the target. Those are reported as unknown rather than unchanged.",
			failures, len(writes))
	}
	return records, ""
}

func (d *DriftDetector) isSelf(actor doppler.LogActor) bool {
	for _, candidate := range []string{actor.Email, actor.Name, actor.Slug} {
		if candidate == "" {
			continue
		}
		if _, ok := d.selfIdentities[strings.ToLower(strings.TrimSpace(candidate))]; ok {
			return true
		}
	}
	return false
}

// explainLogFailure turns a log read failure into something actionable. The
// common case by far is a token without log access, and saying so is the
// difference between a fixable setup problem and an apparent bug.
func explainLogFailure(err error) string {
	var apiErr *doppler.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusForbidden, http.StatusUnauthorized:
			return "The target token cannot read config logs, so changes made on the target cannot be detected. " +
				"Give it log read access and compare again, or continue knowing that a target-side edit will be overwritten without warning."
		case http.StatusNotFound:
			return "This instance does not expose config logs, so changes made on the target cannot be detected."
		}
	}
	if err != nil {
		return "Config logs could not be read, so changes made on the target cannot be detected: " + err.Error()
	}
	return "Changes made on the target could not be determined."
}

// MergeWrites combines lists of writes, keeping the latest time for each
// config.
func MergeWrites(lists ...[]ConfigWrite) []ConfigWrite {
	latest := map[string]ConfigWrite{}
	for _, list := range lists {
		for _, w := range list {
			key := w.Project + "/" + w.Config
			if have, ok := latest[key]; !ok || w.WrittenAt.After(have.WrittenAt) {
				latest[key] = w
			}
		}
	}
	out := make([]ConfigWrite, 0, len(latest))
	for _, w := range latest {
		out = append(out, w)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Project+"/"+out[i].Config < out[j].Project+"/"+out[j].Config
	})
	return out
}

// WritesOnTarget keeps the writes whose config is on the target. A config
// that is not there has no log to read — Doppler answers 404, which read as
// the instance having no config logs at all — and cannot have been edited
// there; a run recreates it anyway.
func WritesOnTarget(writes []ConfigWrite, snap *TargetSnapshot) []ConfigWrite {
	if snap == nil {
		return writes
	}
	out := make([]ConfigWrite, 0, len(writes))
	for _, w := range writes {
		ps, ok := snap.Projects[w.Project]
		if !ok {
			continue
		}
		if _, ok := ps.Configs[w.Config]; !ok {
			continue
		}
		out = append(out, w)
	}
	return out
}

// WritesFromCheckpoint reconstructs which configs this migrator wrote and when,
// from the checkpoint of an earlier run.
//
// This is the only thing drift detection needs to have been kept, and the
// checkpoint was already keeping it.
func WritesFromCheckpoint(cp Checkpoint) []ConfigWrite {
	phase, ok := cp.Phases["secrets_and_locks"]
	if !ok {
		return nil
	}

	// The checkpoint records item status, not per-item timestamps, so the
	// phase's completion time is the boundary. It is the right side of correct:
	// slightly early means a change of ours could look like drift, which is
	// visible and checkable, where slightly late would hide a real one.
	written := cp.StartedAt
	if phase.StartedAt != nil {
		written = *phase.StartedAt
	}

	var writes []ConfigWrite
	for key, item := range phase.Items {
		if item.Status != StatusCompleted {
			continue
		}
		rest, ok := strings.CutPrefix(key, "secrets:")
		if !ok {
			continue
		}
		project, config, ok := strings.Cut(rest, "/")
		if !ok {
			continue
		}
		writes = append(writes, ConfigWrite{Project: project, Config: config, WrittenAt: written})
	}
	return writes
}
