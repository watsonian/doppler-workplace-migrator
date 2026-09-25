package migration

import (
	"path/filepath"
	"testing"
	"time"
)

func tempHistory(t *testing.T) (*ScopeHistory, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "scope-history.json")
	h, err := LoadScopeHistory(path, "https://src", "https://tgt")
	if err != nil {
		t.Fatal(err)
	}
	return h, path
}

func TestAFullRunAfterAFullMigrationIsATopUp(t *testing.T) {
	h, _ := tempHistory(t)
	at := time.Now()

	_ = h.Record(ScopeRun{Mode: ScopePartial, Projects: []string{"api"}, At: at})
	_ = h.Record(ScopeRun{Mode: ScopeFull, Projects: []string{"api", "web"}, At: at})
	_ = h.Record(ScopeRun{Mode: ScopeFull, Projects: []string{"api", "web"}, At: at})

	kinds := []string{h.Runs[0].Kind, h.Runs[1].Kind, h.Runs[2].Kind}
	if kinds[0] != "" || kinds[1] != "" || kinds[2] != RunKindTopUp {
		t.Fatalf("kinds %q: the full run completing a partial is not a top-up; the one after it is", kinds)
	}
	if h.TopUps() != 1 {
		t.Fatalf("top-ups %d", h.TopUps())
	}
}

func TestWaitingSurvivesARestartAndEndsAtCutover(t *testing.T) {
	h, path := tempHistory(t)
	at := time.Now().UTC()
	_ = h.Record(ScopeRun{Mode: ScopeFull, Projects: []string{"api"}, At: at})
	if err := h.SetWaiting(true, at); err != nil {
		t.Fatal(err)
	}
	if err := h.AddToLast(1, 14, 0, nil); err != nil {
		t.Fatal(err)
	}

	again, _ := LoadScopeHistory(path, "https://src", "https://tgt")
	if !again.Waiting() {
		t.Fatal("the choice to wait should be read back after a restart")
	}
	if last := again.LastRun(); last.Created != 1 || last.Updated != 14 {
		t.Fatalf("counts %+v", last)
	}

	// Another pair of instances is another migration.
	other, _ := LoadScopeHistory(path, "https://src", "https://elsewhere")
	if other.Waiting() || len(other.Runs) != 0 {
		t.Fatal("a history for other instances should read as empty")
	}
	// Welcome peeks without knowing the instances yet.
	peek, _ := PeekScopeHistory(path)
	if peek.WaitingSince == nil || peek.TargetHost != "https://tgt" {
		t.Fatalf("peek %+v", peek)
	}

	if err := again.RecordCutover(at); err != nil {
		t.Fatal(err)
	}
	if again.Waiting() || again.CutoverAt == nil || again.WaitingSince != nil {
		t.Fatal("cutover ends the wait")
	}
}

func TestWaitingNeedsAFullMigration(t *testing.T) {
	h, _ := tempHistory(t)
	_ = h.Record(ScopeRun{Mode: ScopePartial, Projects: []string{"api"}, At: time.Now()})
	_ = h.SetWaiting(true, time.Now())
	if h.Waiting() {
		t.Fatal("after only a partial there is nothing to wait with")
	}
}

func TestTheHistoryRemembersEveryConfigWritten(t *testing.T) {
	// Drift used to be looked for only in the configs the latest run wrote;
	// after a quiet top-up that was none, and every other config went unchecked.
	h, path := tempHistory(t)
	early := time.Date(2026, 9, 22, 9, 0, 0, 0, time.UTC)
	late := early.Add(time.Hour)

	_ = h.RecordWrites([]ConfigWrite{{Project: "api", Config: "prd", WrittenAt: early}, {Project: "web", Config: "dev", WrittenAt: early}})
	_ = h.RecordWrites([]ConfigWrite{{Project: "api", Config: "prd", WrittenAt: late}})
	_ = h.RecordWrites([]ConfigWrite{{Project: "web", Config: "dev", WrittenAt: early.Add(-time.Hour)}}) // older: kept as is

	again, _ := LoadScopeHistory(path, "https://src", "https://tgt")
	writes := again.AllWrites()
	if len(writes) != 2 || !writes[0].WrittenAt.Equal(late) || !writes[1].WrittenAt.Equal(early) {
		t.Fatalf("got %+v", writes)
	}
}
