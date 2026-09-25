package migration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// The checkpoint is the only thing this program writes to disk, and the shipped
// compose file mounts it onto the host. What lands in it is bounded here rather
// than trusted to be small, because reasons come from error strings.
func TestCheckpointReasonIsBounded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checkpoint.json")
	cm := NewCheckpointManager(path)

	huge := "upstream returned: " + strings.Repeat("A", 8000)
	cm.SetItemStatus("secrets_and_locks", "secrets:web/prd", StatusFailed, huge)
	if err := cm.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var cp Checkpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		t.Fatalf("checkpoint is not valid JSON: %v", err)
	}

	reason := cp.Phases["secrets_and_locks"].Items["secrets:web/prd"].Reason
	if len(reason) > maxCheckpointReason+len("…") {
		t.Errorf("reason kept %d bytes on disk, want it bounded", len(reason))
	}
	if !strings.HasPrefix(reason, "upstream returned:") {
		t.Errorf("bounding lost the start of the reason: %q", reason)
	}
}

func TestCheckpointReasonKeepsOnlyTheFirstLine(t *testing.T) {
	// A multi-line body — an HTML error page, a stack trace — should contribute
	// one line, not paint the file with someone else's output.
	cm := NewCheckpointManager(filepath.Join(t.TempDir(), "checkpoint.json"))
	cm.SetItemStatus("configs", "config:web/prd", StatusFailed, "409 conflict\n<html>\n<body>oh dear</body>")

	got := cm.Data().Phases["configs"].Items["config:web/prd"].Reason
	if got != "409 conflict" {
		t.Errorf("Reason = %q, want just the first line", got)
	}
}

func TestCheckpointReasonStaysValidUTF8(t *testing.T) {
	// Cutting mid-rune would make the file invalid UTF-8 and the JSON unreadable
	// on the next resume — which is the one moment it has to work.
	cm := NewCheckpointManager(filepath.Join(t.TempDir(), "checkpoint.json"))
	cm.SetItemStatus("configs", "c", StatusFailed, strings.Repeat("é", 400))

	got := cm.Data().Phases["configs"].Items["c"].Reason
	if !utf8.ValidString(got) {
		t.Errorf("reason is not valid UTF-8: %q", got)
	}
}

func TestCheckpointErrorIsBounded(t *testing.T) {
	cm := NewCheckpointManager(filepath.Join(t.TempDir(), "checkpoint.json"))
	cm.SetError(strings.Repeat("B", 5000))

	if got := cm.Data().Error; len(got) > maxCheckpointReason+len("…") {
		t.Errorf("Error kept %d bytes, want it bounded", len(got))
	}
}
