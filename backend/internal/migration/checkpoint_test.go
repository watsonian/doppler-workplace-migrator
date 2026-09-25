package migration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestCheckpointSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoint.json")

	cm := NewCheckpointManager(path)
	cm.InitPhase("phase1")
	cm.SetItemStatus("phase1", "item-a", StatusCompleted, "")
	cm.SetItemStatus("phase1", "item-b", StatusSkipped, "already exists")
	cm.CompletePhase("phase1")

	if err := cm.Save(); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	// Load into a fresh manager
	cm2 := NewCheckpointManager(path)
	if err := cm2.Load(); err != nil {
		t.Fatalf("load failed: %v", err)
	}

	if !cm2.IsPhaseComplete("phase1") {
		t.Error("expected phase1 to be complete after load")
	}
	if cm2.GetItemStatus("phase1", "item-a") != StatusCompleted {
		t.Errorf("expected item-a completed, got %s", cm2.GetItemStatus("phase1", "item-a"))
	}
	if cm2.GetItemStatus("phase1", "item-b") != StatusSkipped {
		t.Errorf("expected item-b skipped, got %s", cm2.GetItemStatus("phase1", "item-b"))
	}
}

func TestCheckpointGetSetItemStatus(t *testing.T) {
	cm := NewCheckpointManager("")

	// Before init, status is pending
	if s := cm.GetItemStatus("phase1", "item-a"); s != StatusPending {
		t.Errorf("expected pending, got %s", s)
	}

	cm.InitPhase("phase1")
	cm.SetItemStatus("phase1", "item-a", StatusInProgress, "")

	if s := cm.GetItemStatus("phase1", "item-a"); s != StatusInProgress {
		t.Errorf("expected in_progress, got %s", s)
	}

	cm.SetItemStatus("phase1", "item-a", StatusCompleted, "")
	if s := cm.GetItemStatus("phase1", "item-a"); s != StatusCompleted {
		t.Errorf("expected completed, got %s", s)
	}
}

func TestCheckpointIsComplete(t *testing.T) {
	cm := NewCheckpointManager("")

	if cm.IsPhaseComplete("phase1") {
		t.Error("expected phase1 not complete before init")
	}

	cm.InitPhase("phase1")
	if cm.IsPhaseComplete("phase1") {
		t.Error("expected phase1 not complete after init")
	}

	cm.CompletePhase("phase1")
	if !cm.IsPhaseComplete("phase1") {
		t.Error("expected phase1 complete after CompletePhase")
	}
}

func TestCheckpointConcurrentAccess(t *testing.T) {
	cm := NewCheckpointManager("")
	cm.InitPhase("phase1")

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			item := "item-" + string(rune('a'+idx%26))
			cm.SetItemStatus("phase1", item, StatusCompleted, "")
			cm.GetItemStatus("phase1", item)
		}(i)
	}
	wg.Wait()

	// Verify no panic and data is accessible
	data := cm.Data()
	if data.Phases["phase1"] == nil {
		t.Error("expected phase1 to exist after concurrent access")
	}
}

func TestCheckpointDebounce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoint.json")

	cm := NewCheckpointManager(path)
	cm.Start()

	// Make multiple rapid changes
	cm.InitPhase("phase1")
	cm.SetItemStatus("phase1", "item-a", StatusCompleted, "")
	cm.SetItemStatus("phase1", "item-b", StatusCompleted, "")

	// Wait for debounce tick
	time.Sleep(1500 * time.Millisecond)

	cm.Stop()

	// Verify file was written
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected checkpoint file to exist: %v", err)
	}

	var cp Checkpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if cp.Phases["phase1"] == nil {
		t.Error("expected phase1 in saved checkpoint")
	}
	if len(cp.Phases["phase1"].Items) != 2 {
		t.Errorf("expected 2 items, got %d", len(cp.Phases["phase1"].Items))
	}
}

func TestCheckpointLoadNonexistentFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.json")
	cm := NewCheckpointManager(path)

	if err := cm.Load(); err != nil {
		t.Fatalf("expected no error for nonexistent file, got %v", err)
	}

	// Should have fresh checkpoint
	data := cm.Data()
	if len(data.Phases) != 0 {
		t.Errorf("expected 0 phases for fresh checkpoint, got %d", len(data.Phases))
	}
}

func TestCheckpointPhaseInitialization(t *testing.T) {
	cm := NewCheckpointManager("")

	cm.InitPhase("phase1")
	cm.InitPhase("phase2")

	data := cm.Data()
	if len(data.Phases) != 2 {
		t.Fatalf("expected 2 phases, got %d", len(data.Phases))
	}

	p1 := data.Phases["phase1"]
	if p1.Status != StatusInProgress {
		t.Errorf("expected in_progress, got %s", p1.Status)
	}
	if p1.StartedAt == nil {
		t.Error("expected StartedAt to be set")
	}
	if p1.DoneAt != nil {
		t.Error("expected DoneAt to be nil")
	}

	// Double init should not reset
	cm.SetItemStatus("phase1", "item-a", StatusCompleted, "")
	cm.InitPhase("phase1")
	if cm.GetItemStatus("phase1", "item-a") != StatusCompleted {
		t.Error("InitPhase should not reset existing phase")
	}
}

func TestCheckpointValidJSONFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoint.json")

	cm := NewCheckpointManager(path)
	cm.InitPhase("phase1")
	cm.SetItemStatus("phase1", "role:custom-admin", StatusCompleted, "")
	cm.SetItemStatus("phase1", "role:deployer", StatusSkipped, "already exists on target")
	cm.CompletePhase("phase1")
	cm.SetCompleted()

	if err := cm.Save(); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}

	// Verify valid JSON
	if !json.Valid(data) {
		t.Error("checkpoint file is not valid JSON")
	}

	// Verify structure
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if _, ok := raw["started_at"]; !ok {
		t.Error("expected started_at field")
	}
	if _, ok := raw["updated_at"]; !ok {
		t.Error("expected updated_at field")
	}
	if _, ok := raw["phases"]; !ok {
		t.Error("expected phases field")
	}
	if raw["completed"] != true {
		t.Error("expected completed=true")
	}
}

func TestCheckpointSetError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoint.json")

	cm := NewCheckpointManager(path)
	cm.SetError("phase 5 failed: connection refused")

	if err := cm.Save(); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	cm2 := NewCheckpointManager(path)
	if err := cm2.Load(); err != nil {
		t.Fatalf("load failed: %v", err)
	}

	data := cm2.Data()
	if data.Error != "phase 5 failed: connection refused" {
		t.Errorf("expected error message, got %q", data.Error)
	}
}

func TestCheckpointStopFinalSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoint.json")

	cm := NewCheckpointManager(path)
	cm.Start()

	// Make a change but don't wait for tick
	cm.InitPhase("phase1")
	cm.SetItemStatus("phase1", "item-a", StatusCompleted, "")

	// Stop should force a final save
	cm.Stop()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected checkpoint file after stop: %v", err)
	}

	var cp Checkpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if cp.Phases["phase1"] == nil {
		t.Error("expected phase1 to be saved on stop")
	}
}
