package migration

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// ResourceStatus tracks the migration status of a single item.
type ResourceStatus string

const (
	StatusPending    ResourceStatus = "pending"
	StatusInProgress ResourceStatus = "in_progress"
	StatusCompleted  ResourceStatus = "completed"
	StatusSkipped    ResourceStatus = "skipped"
	StatusFailed     ResourceStatus = "failed"
)

// Checkpoint is the top-level checkpoint structure persisted to disk.
// It contains NO secrets — only metadata about migration progress.
type Checkpoint struct {
	// SourceHost and TargetHost identify which migration this checkpoint belongs to.
	// Without them a checkpoint left over from a different pair of instances is
	// replayed as-is, and a completed one skips every phase while reporting success.
	SourceHost string                      `json:"source_host,omitempty"`
	TargetHost string                      `json:"target_host,omitempty"`
	StartedAt  time.Time                   `json:"started_at"`
	UpdatedAt  time.Time                   `json:"updated_at"`
	Phases     map[string]*PhaseCheckpoint `json:"phases"`
	Completed  bool                        `json:"completed"`
	Error      string                      `json:"error,omitempty"`

	// ScopeKey names what the run covered. A phase is marked complete once every
	// item in it has run — for that run's scope — so resuming a checkpoint under
	// a different scope would skip phases wholesale for the projects it never
	// covered. Empty is a full run, which is also what every checkpoint written
	// before scopes existed means.
	ScopeKey string `json:"scope_key,omitempty"`
}

// PhaseCheckpoint tracks progress within a single migration phase.
type PhaseCheckpoint struct {
	Name      string                     `json:"name"`
	Status    ResourceStatus             `json:"status"`
	Items     map[string]*ItemCheckpoint `json:"items"`
	StartedAt *time.Time                 `json:"started_at,omitempty"`
	DoneAt    *time.Time                 `json:"done_at,omitempty"`
}

// ItemCheckpoint tracks the status of a single resource item within a phase.
type ItemCheckpoint struct {
	Status ResourceStatus `json:"status"`
	Reason string         `json:"reason,omitempty"`
}

// CheckpointManager handles loading, saving, and querying checkpoint data.
// It uses debounced persistence to avoid excessive disk writes.
type CheckpointManager struct {
	mu         sync.RWMutex
	checkpoint *Checkpoint
	filePath   string
	dirty      bool
	stopCh     chan struct{}
	doneCh     chan struct{}
}

// NewCheckpointManager creates a new manager. Call Start() to begin background persistence.
func NewCheckpointManager(filePath string) *CheckpointManager {
	return &CheckpointManager{
		filePath: filePath,
		checkpoint: &Checkpoint{
			StartedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
			Phases:    make(map[string]*PhaseCheckpoint),
		},
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
}

// LoadFor reads the checkpoint for a specific source/target pair. A checkpoint
// belonging to a different pair, or one that is already complete, is discarded and
// the manager starts fresh — resuming either would silently do nothing while
// reporting success. Returns true when an existing checkpoint was adopted.
func (cm *CheckpointManager) LoadFor(sourceHost, targetHost string) (bool, error) {
	return cm.LoadForScope(sourceHost, targetHost, "")
}

// LoadForScope is LoadFor for a run over a particular scope. A checkpoint from a
// run with a different scope is set aside, for the reason on ScopeKey.
func (cm *CheckpointManager) LoadForScope(sourceHost, targetHost, scopeKey string) (bool, error) {
	if err := cm.Load(); err != nil {
		return false, err
	}

	cm.mu.Lock()
	defer cm.mu.Unlock()

	cp := cm.checkpoint
	adopted := len(cp.Phases) > 0

	switch {
	case adopted && cp.Completed:
		log.Printf("[checkpoint] previous run completed; starting fresh")
		adopted = false
	case adopted && (cp.SourceHost != sourceHost || cp.TargetHost != targetHost):
		log.Printf("[checkpoint] belongs to %s -> %s, not %s -> %s; starting fresh",
			cp.SourceHost, cp.TargetHost, sourceHost, targetHost)
		adopted = false
	case adopted && cp.ScopeKey != scopeKey:
		log.Printf("[checkpoint] covered a different scope (%q, not %q); starting fresh", cp.ScopeKey, scopeKey)
		adopted = false
	}

	if !adopted {
		cm.checkpoint = &Checkpoint{
			StartedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
			Phases:    make(map[string]*PhaseCheckpoint),
		}
	}

	cm.checkpoint.SourceHost = sourceHost
	cm.checkpoint.TargetHost = targetHost
	cm.checkpoint.ScopeKey = scopeKey
	cm.dirty = true
	return adopted, nil
}

// Load reads a checkpoint file from disk. If the file does not exist,
// the manager starts fresh.
func (cm *CheckpointManager) Load() error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	data, err := os.ReadFile(cm.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // fresh start
		}
		return fmt.Errorf("read checkpoint: %w", err)
	}

	var cp Checkpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		return fmt.Errorf("parse checkpoint: %w", err)
	}

	cm.checkpoint = &cp
	return nil
}

// Save writes the checkpoint to disk immediately.
func (cm *CheckpointManager) Save() error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	return cm.saveLocked()
}

// saveLocked writes checkpoint to disk. Caller must hold cm.mu.
func (cm *CheckpointManager) saveLocked() error {
	cm.checkpoint.UpdatedAt = time.Now().UTC()
	cm.dirty = false

	data, err := json.MarshalIndent(cm.checkpoint, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal checkpoint: %w", err)
	}

	dir := filepath.Dir(cm.filePath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create checkpoint dir: %w", err)
	}

	// 0600: reasons are bounded API messages, which can name secrets and configs
	// even though they never carry a value.
	if err := os.WriteFile(cm.filePath, data, 0o600); err != nil {
		return fmt.Errorf("write checkpoint: %w", err)
	}

	return nil
}

// Start begins the background debounced persistence goroutine (saves at most once per second).
func (cm *CheckpointManager) Start() {
	go func() {
		defer close(cm.doneCh)
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-cm.stopCh:
				// Final save on stop
				cm.mu.Lock()
				if cm.dirty {
					cm.saveLocked()
				}
				cm.mu.Unlock()
				return
			case <-ticker.C:
				cm.mu.Lock()
				if cm.dirty {
					cm.saveLocked()
				}
				cm.mu.Unlock()
			}
		}
	}()
}

// Stop signals the background goroutine to stop and waits for it to finish.
func (cm *CheckpointManager) Stop() {
	close(cm.stopCh)
	<-cm.doneCh
}

// InitPhase initializes a phase in the checkpoint if it doesn't exist.
func (cm *CheckpointManager) InitPhase(phase string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if _, exists := cm.checkpoint.Phases[phase]; !exists {
		now := time.Now().UTC()
		cm.checkpoint.Phases[phase] = &PhaseCheckpoint{
			Name:      phase,
			Status:    StatusInProgress,
			Items:     make(map[string]*ItemCheckpoint),
			StartedAt: &now,
		}
		cm.dirty = true
	}
}

// CompletePhase marks a phase as completed.
func (cm *CheckpointManager) CompletePhase(phase string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if p, ok := cm.checkpoint.Phases[phase]; ok {
		p.Status = StatusCompleted
		now := time.Now().UTC()
		p.DoneAt = &now
		cm.dirty = true
	}
}

// SetItemStatus sets the status of an item within a phase.
func (cm *CheckpointManager) SetItemStatus(phase, item string, status ResourceStatus, reason string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	p, ok := cm.checkpoint.Phases[phase]
	if !ok {
		now := time.Now().UTC()
		p = &PhaseCheckpoint{
			Name:      phase,
			Status:    StatusInProgress,
			Items:     make(map[string]*ItemCheckpoint),
			StartedAt: &now,
		}
		cm.checkpoint.Phases[phase] = p
	}

	p.Items[item] = &ItemCheckpoint{
		Status: status,
		Reason: boundReason(reason),
	}
	cm.dirty = true
}

// maxCheckpointReason bounds a stored reason. Long enough for an API message,
// short enough that nothing large can be smuggled onto disk a line at a time.
const maxCheckpointReason = 200

// boundReason trims a reason to its first line and a fixed length.
//
// The checkpoint is the only thing this program writes to disk, and it is
// mounted onto the host in the shipped compose file. Its purpose is to say which
// items to retry, which needs a short explanation and nothing more. Reasons come
// from error strings, and an error string is not a value this code chooses — so
// what lands in the file is bounded here rather than trusted to be small.
func boundReason(reason string) string {
	if i := strings.IndexAny(reason, "\r\n"); i >= 0 {
		reason = reason[:i]
	}
	if len(reason) > maxCheckpointReason {
		// Cut on a rune boundary: a reason ending mid-character would make the
		// file invalid UTF-8 and the JSON unreadable.
		cut := maxCheckpointReason
		for cut > 0 && !utf8.RuneStart(reason[cut]) {
			cut--
		}
		reason = reason[:cut] + "…"
	}
	return reason
}

// ResetItems drops the recorded status of specific items so the next run attempts
// them again, and reopens their phase if it had been marked complete.
//
// This is how a targeted re-run works: rather than re-deriving which operations
// to repeat, the checkpoint is walked back for exactly the chosen items and the
// executor's ordinary resume path does the rest. Items not named are left alone,
// so fixing one class never re-runs another class's failures.
func (cm *CheckpointManager) ResetItems(phase string, items []string) int {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	p, ok := cm.checkpoint.Phases[phase]
	if !ok {
		return 0
	}

	n := 0
	for _, item := range items {
		if _, ok := p.Items[item]; ok {
			delete(p.Items, item)
			n++
		}
	}
	if n > 0 {
		// A completed phase is skipped wholesale, so reopen it or the reset items
		// are never revisited.
		p.Status = StatusInProgress
		p.DoneAt = nil
		cm.checkpoint.Completed = false
		cm.checkpoint.Error = ""
		cm.dirty = true
	}
	return n
}

// GetItemStatus returns the status of an item, or StatusPending if not tracked.
func (cm *CheckpointManager) GetItemStatus(phase, item string) ResourceStatus {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	p, ok := cm.checkpoint.Phases[phase]
	if !ok {
		return StatusPending
	}
	ic, ok := p.Items[item]
	if !ok {
		return StatusPending
	}
	return ic.Status
}

// IsPhaseComplete returns true if the phase is marked completed.
func (cm *CheckpointManager) IsPhaseComplete(phase string) bool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	p, ok := cm.checkpoint.Phases[phase]
	if !ok {
		return false
	}
	return p.Status == StatusCompleted
}

// SetCompleted marks the entire migration as completed.
func (cm *CheckpointManager) SetCompleted() {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cm.checkpoint.Completed = true
	cm.dirty = true
}

// SetError records a fatal error in the checkpoint.
func (cm *CheckpointManager) SetError(msg string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cm.checkpoint.Error = boundReason(msg)
	cm.dirty = true
}

// PhaseHasFailures returns true if any item in the phase has StatusFailed.
func (cm *CheckpointManager) PhaseHasFailures(phase string) bool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	p, ok := cm.checkpoint.Phases[phase]
	if !ok {
		return false
	}
	for _, item := range p.Items {
		if item.Status == StatusFailed {
			return true
		}
	}
	return false
}

// Data returns a deep copy of the current checkpoint data, safe to serialize while
// a migration is still running. A shallow copy shares the Phases and Items maps, so
// encoding it concurrently with SetItemStatus is a concurrent map read and write —
// which Go turns into an unrecoverable fatal error, not a recoverable panic.
func (cm *CheckpointManager) Data() Checkpoint {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	out := *cm.checkpoint
	out.Phases = make(map[string]*PhaseCheckpoint, len(cm.checkpoint.Phases))
	for name, phase := range cm.checkpoint.Phases {
		p := *phase
		p.Items = make(map[string]*ItemCheckpoint, len(phase.Items))
		for key, item := range phase.Items {
			i := *item
			p.Items[key] = &i
		}
		out.Phases[name] = &p
	}
	return out
}
