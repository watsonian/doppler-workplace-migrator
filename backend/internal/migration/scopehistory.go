package migration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ScopeHistory remembers which runs finished, and what each one covered.
//
// It has to outlive the checkpoint. A checkpoint is thrown away once its run
// completes, but "which projects has a partial run already moved" is exactly
// what the next run needs to know: the full run steps around those projects in
// its first stage, because re-running creation over them would leave their old
// values in place and create every webhook a second time, and then brings them
// up to date in its second.
//
// Keyed to the source and target, like the checkpoint. A history from a
// different pair of instances describes a different target.
type ScopeHistory struct {
	mu   sync.Mutex
	path string

	SourceHost string     `json:"source_host,omitempty"`
	TargetHost string     `json:"target_host,omitempty"`
	Runs       []ScopeRun `json:"runs"`

	// WaitingSince is set when, after a full migration, the choice was to keep
	// the source in use and top up until cutover. Cleared by choosing to cut
	// over. It is what sends Pre-flight and Welcome back to the waiting page.
	WaitingSince *time.Time `json:"waiting_since,omitempty"`
	// CutoverAt is when cutover finished. After it there is nothing to wait for.
	CutoverAt *time.Time `json:"cutover_at,omitempty"`

	// Settings are the run options the migration was configured with, so a
	// session resumed after a restart runs its top-ups the same way.
	Settings *RunSettings `json:"settings,omitempty"`

	// Writes maps each config this migrator has written secrets to
	// ("project/config") to when it last did. Changes made on the target are
	// looked for in every one of them since that time — not only in the
	// configs the latest run happened to write, which after a quiet top-up can
	// be none, or one project.
	Writes map[string]time.Time `json:"writes,omitempty"`
}

// RunSettings are the Configuration page's options that shape a run. No
// tokens: those come from the environment or are entered again.
type RunSettings struct {
	MigrateWorkplaceName bool `json:"migrate_workplace_name"`
	StopOnNewErrorClass  bool `json:"stop_on_new_error_class"`
}

// Run kinds. A full or partial run is the mode it ran under; a top-up is a
// full-scope run after a full migration has already finished, which brings
// the source's changes across rather than migrating anything new.
const (
	RunKindTopUp = "top_up"
)

// ScopeRun is one finished run.
type ScopeRun struct {
	Mode     ScopeMode `json:"mode"`
	Projects []string  `json:"projects"`
	At       time.Time `json:"at"`

	// Kind is RunKindTopUp for a top-up, and empty otherwise.
	Kind string `json:"kind,omitempty"`

	// What the run did. A top-up that found nothing to change is recorded too,
	// with zeros: "checked, nothing changed" is worth knowing.
	Created int `json:"created"`
	Updated int `json:"updated"`
	Failed  int `json:"failed"`

	// Changes is what it wrote, by resource type. Absent on runs recorded
	// before it was kept.
	Changes *RunChanges `json:"changes,omitempty"`
}

// LoadScopeHistory reads the history at path for this source and target. A
// missing file, or one written for another pair of instances, is an empty
// history rather than an error.
func LoadScopeHistory(path, sourceHost, targetHost string) (*ScopeHistory, error) {
	h := &ScopeHistory{path: path, SourceHost: sourceHost, TargetHost: targetHost}

	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return h, nil
		}
		return nil, fmt.Errorf("read scope history: %w", err)
	}

	var stored ScopeHistory
	if err := json.Unmarshal(raw, &stored); err != nil {
		return nil, fmt.Errorf("parse scope history: %w", err)
	}
	if stored.SourceHost == sourceHost && stored.TargetHost == targetHost {
		h.Runs = stored.Runs
		h.WaitingSince = stored.WaitingSince
		h.CutoverAt = stored.CutoverAt
		h.Settings = stored.Settings
		h.Writes = stored.Writes
	}
	return h, nil
}

// PeekScopeHistory reads whatever history is on disk, for whichever instances
// it was written for. Welcome uses it before anything is configured, to offer
// to carry on; nothing that acts on a target reads it.
func PeekScopeHistory(path string) (*ScopeHistory, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &ScopeHistory{}, nil
		}
		return nil, fmt.Errorf("read scope history: %w", err)
	}
	var stored ScopeHistory
	if err := json.Unmarshal(raw, &stored); err != nil {
		return nil, fmt.Errorf("parse scope history: %w", err)
	}
	stored.path = ""
	return &stored, nil
}

// Record appends a finished run and writes the history out. A full-scope run
// after a full migration is marked as a top-up.
func (h *ScopeHistory) Record(run ScopeRun) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	run.Projects = append([]string(nil), run.Projects...)
	sort.Strings(run.Projects)
	if run.Mode == ScopeFull && h.fullDoneLocked() {
		run.Kind = RunKindTopUp
	}
	h.Runs = append(h.Runs, run)
	return h.saveLocked()
}

// RecordWrites notes configs this migrator wrote, keeping the latest time for
// each.
func (h *ScopeHistory) RecordWrites(writes []ConfigWrite) error {
	if len(writes) == 0 {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.Writes == nil {
		h.Writes = map[string]time.Time{}
	}
	for _, w := range writes {
		key := w.Project + "/" + w.Config
		if w.WrittenAt.After(h.Writes[key]) {
			h.Writes[key] = w.WrittenAt.UTC()
		}
	}
	return h.saveLocked()
}

// AllWrites lists every config this migrator has written, sorted.
func (h *ScopeHistory) AllWrites() []ConfigWrite {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]ConfigWrite, 0, len(h.Writes))
	for key, at := range h.Writes {
		project, config, ok := strings.Cut(key, "/")
		if !ok {
			continue
		}
		out = append(out, ConfigWrite{Project: project, Config: config, WrittenAt: at})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Project+"/"+out[i].Config < out[j].Project+"/"+out[j].Config
	})
	return out
}

// RememberSettings keeps the options a run used.
func (h *ScopeHistory) RememberSettings(settings RunSettings) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.Settings = &settings
	return h.saveLocked()
}

// AddToLast adds counts to the most recent run. A run is recorded when its
// first stage finishes, because what that stage created has to be stepped
// around whatever happens next; the second stage's counts arrive after.
func (h *ScopeHistory) AddToLast(created, updated, failed int, changes *RunChanges) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.Runs) == 0 {
		return nil
	}
	last := &h.Runs[len(h.Runs)-1]
	last.Created += created
	last.Updated += updated
	last.Failed += failed
	if !changes.Empty() {
		if last.Changes == nil {
			last.Changes = &RunChanges{Groups: []ChangeGroup{}}
		}
		last.Changes.Merge(changes)
	}
	return h.saveLocked()
}

// SetWaiting records the choice to wait (true) or to cut over (false).
func (h *ScopeHistory) SetWaiting(waiting bool, at time.Time) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if waiting {
		if h.WaitingSince == nil {
			at := at
			h.WaitingSince = &at
		}
	} else {
		h.WaitingSince = nil
	}
	return h.saveLocked()
}

// RecordCutover marks the migration finished.
func (h *ScopeHistory) RecordCutover(at time.Time) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.CutoverAt = &at
	h.WaitingSince = nil
	return h.saveLocked()
}

// FullDone reports whether a full migration has finished since the last
// partial — the point from which cutover is possible and a full-scope run is
// a top-up.
func (h *ScopeHistory) FullDone() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.fullDoneLocked()
}

func (h *ScopeHistory) fullDoneLocked() bool {
	done := false
	for _, run := range h.Runs {
		done = run.Mode == ScopeFull
	}
	return done
}

// InFlight reports whether a full migration has run and cutover has not: the
// migration the target is being kept in step with. Only one can be in flight;
// a partial run or another migration waits until it is cut over.
func (h *ScopeHistory) InFlight() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.fullDoneLocked() && h.CutoverAt == nil
}

// Unfinished reports whether any run has happened without a cutover after it,
// partial runs included. The history holds one migration, so configuring a
// different pair of instances while this is true would overwrite it.
func (h *ScopeHistory) Unfinished() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.Runs) > 0 && h.CutoverAt == nil
}

// Waiting reports whether the migration is between a full run and cutover
// by choice.
func (h *ScopeHistory) Waiting() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.WaitingSince != nil && h.CutoverAt == nil && h.fullDoneLocked()
}

// TopUps counts the top-ups recorded.
func (h *ScopeHistory) TopUps() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, run := range h.Runs {
		if run.Kind == RunKindTopUp {
			n++
		}
	}
	return n
}

// LastRun is the most recent run, or nil.
func (h *ScopeHistory) LastRun() *ScopeRun {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.Runs) == 0 {
		return nil
	}
	run := h.Runs[len(h.Runs)-1]
	return &run
}

func (h *ScopeHistory) saveLocked() error {
	if h.path == "" {
		return nil
	}
	raw, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal scope history: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(h.path), 0o700); err != nil {
		return fmt.Errorf("create scope history dir: %w", err)
	}
	// 0600, like the checkpoint: project names describe the shape of a workplace.
	if err := os.WriteFile(h.path, raw, 0o600); err != nil {
		return fmt.Errorf("write scope history: %w", err)
	}
	return nil
}

// Migrated maps each project any finished run covered to when it first did.
func (h *ScopeHistory) Migrated() map[string]time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()

	out := map[string]time.Time{}
	for _, run := range h.Runs {
		for _, slug := range run.Projects {
			if _, seen := out[slug]; !seen {
				out[slug] = run.At
			}
		}
	}
	return out
}

// LastMigrated maps each project any finished run covered to when a run
// last did — which, for a project deleted from the target and migrated
// again, is the rebuild rather than the first time.
func (h *ScopeHistory) LastMigrated() map[string]time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()

	out := map[string]time.Time{}
	for _, run := range h.Runs {
		for _, slug := range run.Projects {
			if run.At.After(out[slug]) {
				out[slug] = run.At
			}
		}
	}
	return out
}

// MigratedAmong returns, sorted, the projects in candidates a finished run has
// already covered.
func (h *ScopeHistory) MigratedAmong(candidates []string) []string {
	done := h.Migrated()
	var out []string
	for _, slug := range candidates {
		if _, ok := done[slug]; ok {
			out = append(out, slug)
		}
	}
	sort.Strings(out)
	return out
}

// LastPartial is the most recent partial run, or nil.
func (h *ScopeHistory) LastPartial() *ScopeRun {
	h.mu.Lock()
	defer h.mu.Unlock()

	for i := len(h.Runs) - 1; i >= 0; i-- {
		if h.Runs[i].Mode == ScopePartial {
			run := h.Runs[i]
			return &run
		}
	}
	return nil
}

// CutoverBlocked reports whether the target holds only part of the source.
//
// Cutover mirrors trusted IPs and applies deletions. Against a target a partial
// run left most of the source off, that is locking down and pruning a
// workplace that is not there yet. A full run completing after the last
// partial clears it; a history with no partial at all never blocks, which is
// also what keeps a migration that predates partial runs from being stopped.
func (h *ScopeHistory) CutoverBlocked() bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	partialSeen := false
	for _, run := range h.Runs {
		switch run.Mode {
		case ScopePartial:
			partialSeen = true
		case ScopeFull:
			partialSeen = false
		}
	}
	return partialSeen
}
