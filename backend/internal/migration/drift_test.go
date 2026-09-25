package migration

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/doppler/onprem-migrator/internal/doppler"
)

type fakeLogReader struct {
	entries map[string][]doppler.ConfigLogEntry
	err     error
	failFor map[string]error
}

func (f *fakeLogReader) ListConfigLogsSince(_ context.Context, project, config string, since time.Time) ([]doppler.ConfigLogEntry, error) {
	key := project + "/" + config
	if f.err != nil {
		return nil, f.err
	}
	if err, ok := f.failFor[key]; ok {
		return nil, err
	}
	var out []doppler.ConfigLogEntry
	for _, e := range f.entries[key] {
		if e.CreatedAt.After(since) {
			out = append(out, e)
		}
	}
	return out, nil
}

func entry(when time.Time, who, text string) doppler.ConfigLogEntry {
	return doppler.ConfigLogEntry{
		CreatedAt: when,
		User:      doppler.LogActor{Email: who, Name: who},
		Text:      text,
	}
}

var wroteAt = time.Date(2026, 9, 17, 10, 12, 0, 0, time.UTC)

func writes(scopes ...string) []ConfigWrite {
	out := make([]ConfigWrite, 0, len(scopes))
	for _, s := range scopes {
		project, config, _ := strings.Cut(s, "/")
		out = append(out, ConfigWrite{Project: project, Config: config, WrittenAt: wroteAt})
	}
	return out
}

func TestDriftIgnoresOurOwnWrites(t *testing.T) {
	// The migrator's own writes are in the log too. Counting them would report
	// every config it touched as drifted.
	reader := &fakeLogReader{entries: map[string][]doppler.ConfigLogEntry{
		"payments/prd": {entry(wroteAt.Add(time.Minute), "migrator@acme.com", "Updated 12 secrets")},
	}}

	d := NewDriftDetector(reader, "migrator@acme.com")
	records, unavailable := d.Detect(context.Background(), writes("payments/prd"))

	if len(records) != 0 {
		t.Errorf("our own write was reported as drift: %+v", records)
	}
	if unavailable != "" {
		t.Errorf("unexpected unavailable: %q", unavailable)
	}
}

func TestDriftReportsSomebodyElse(t *testing.T) {
	changed := wroteAt.Add(23 * time.Hour)
	reader := &fakeLogReader{entries: map[string][]doppler.ConfigLogEntry{
		"payments/prd": {entry(changed, "rosa.imani@acme.com", "Updated secret STRIPE_WEBHOOK_SECRET")},
	}}

	d := NewDriftDetector(reader, "migrator@acme.com")
	records, _ := d.Detect(context.Background(), writes("payments/prd"))

	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}
	// Who is the point. A stored digest could only ever have said that
	// something changed, which is the less useful half of the answer.
	if records[0].ChangedBy != "rosa.imani@acme.com" {
		t.Errorf("ChangedBy = %q", records[0].ChangedBy)
	}
	if records[0].Scope != "payments/prd" {
		t.Errorf("Scope = %q", records[0].Scope)
	}
	if !strings.Contains(records[0].Summary, "STRIPE_WEBHOOK_SECRET") {
		t.Errorf("Summary lost the detail: %q", records[0].Summary)
	}
}

func TestDriftMatchesIdentityAcrossEmailNameAndSlug(t *testing.T) {
	reader := &fakeLogReader{entries: map[string][]doppler.ConfigLogEntry{
		"payments/prd": {{
			CreatedAt: wroteAt.Add(time.Minute),
			User:      doppler.LogActor{Slug: "terraform-provisioner", Type: "service_account"},
		}},
	}}

	// A service account has no email; the identity has to match on whichever
	// name it does have, or every service-account write reads as drift.
	d := NewDriftDetector(reader, "terraform-provisioner")
	records, _ := d.Detect(context.Background(), writes("payments/prd"))
	if len(records) != 0 {
		t.Errorf("matched on slug failed: %+v", records)
	}
}

func TestDriftAcceptsATokenSwappedMidRun(t *testing.T) {
	// Fixing a restricted-read failure replaces the source token, and the
	// replacement writes under a different name. Both are us.
	reader := &fakeLogReader{entries: map[string][]doppler.ConfigLogEntry{
		"payments/prd": {entry(wroteAt.Add(time.Minute), "backup-runner", "Updated secrets")},
	}}

	d := NewDriftDetector(reader, "migrator@acme.com")
	d.AddIdentity("backup-runner")

	records, _ := d.Detect(context.Background(), writes("payments/prd"))
	if len(records) != 0 {
		t.Errorf("a replacement token's writes were reported as drift: %+v", records)
	}
}

func TestDriftSaysWhenItCannotTell(t *testing.T) {
	// "No drift" and "could not check" must not look the same, or a permissions
	// problem reads as a clean bill of health.
	reader := &fakeLogReader{err: &doppler.APIError{StatusCode: 403, Message: "forbidden"}}

	d := NewDriftDetector(reader, "migrator@acme.com")
	records, unavailable := d.Detect(context.Background(), writes("payments/prd", "api/prd"))

	if len(records) != 0 {
		t.Errorf("records = %+v", records)
	}
	if unavailable == "" {
		t.Fatal("a total failure to read logs must be reported")
	}
	if !strings.Contains(unavailable, "log read access") {
		t.Errorf("the message should say what to do about it: %q", unavailable)
	}
}

func TestDriftReportsAPartialFailureWithoutLosingWhatItFound(t *testing.T) {
	changed := wroteAt.Add(time.Hour)
	reader := &fakeLogReader{
		entries: map[string][]doppler.ConfigLogEntry{
			"payments/prd": {entry(changed, "rosa.imani@acme.com", "Updated secrets")},
		},
		failFor: map[string]error{"api/prd": &doppler.APIError{StatusCode: 500}},
	}

	d := NewDriftDetector(reader, "migrator@acme.com")
	records, unavailable := d.Detect(context.Background(), writes("payments/prd", "api/prd"))

	if len(records) != 1 {
		t.Errorf("the readable config's drift was lost: %+v", records)
	}
	if !strings.Contains(unavailable, "1 of 2") {
		t.Errorf("the partial failure should be quantified: %q", unavailable)
	}
}

func TestWritesFromCheckpointReconstructsWhatWeWrote(t *testing.T) {
	// Drift needs one thing remembered from the earlier run, and the checkpoint
	// was already remembering it.
	started := wroteAt
	cp := Checkpoint{
		StartedAt: started,
		Phases: map[string]*PhaseCheckpoint{
			"secrets_and_locks": {
				StartedAt: &started,
				Items: map[string]*ItemCheckpoint{
					"secrets:payments/prd": {Status: StatusCompleted},
					"secrets:api/prd":      {Status: StatusCompleted},
					"secrets:web/prd":      {Status: StatusFailed},
					"lock:payments/prd":    {Status: StatusCompleted},
				},
			},
		},
	}

	got := WritesFromCheckpoint(cp)
	if len(got) != 2 {
		t.Fatalf("got %d writes, want 2 completed secret writes: %+v", len(got), got)
	}
	for _, w := range got {
		if w.Config != "prd" || (w.Project != "payments" && w.Project != "api") {
			t.Errorf("unexpected write %+v", w)
		}
		if !w.WrittenAt.Equal(started) {
			t.Errorf("WrittenAt = %v, want the phase start", w.WrittenAt)
		}
	}
}

func TestWritesFromCheckpointIsEmptyWithoutASecretsPhase(t *testing.T) {
	if got := WritesFromCheckpoint(Checkpoint{Phases: map[string]*PhaseCheckpoint{}}); len(got) != 0 {
		t.Errorf("got %+v, want none", got)
	}
}

func TestDriftSkipsConfigsNoLongerOnTheTarget(t *testing.T) {
	// The report: the only configs the last run wrote belonged to a project
	// since deleted from the target. Every log read answered 404, and the page
	// said the instance had no config logs at all.
	at := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	writes := []ConfigWrite{
		{Project: "api", Config: "prd", WrittenAt: at},
		{Project: "something-new", Config: "dev", WrittenAt: at},
		{Project: "api", Config: "gone", WrittenAt: at},
	}
	snap := &TargetSnapshot{Projects: map[string]ProjectSnapshot{
		"api": {Configs: map[string]ConfigSnapshot{"prd": {}}},
	}}
	got := WritesOnTarget(writes, snap)
	if len(got) != 1 || got[0].Project != "api" || got[0].Config != "prd" {
		t.Fatalf("got %+v", got)
	}
}

func TestMergeWritesKeepsTheLatestForEachConfig(t *testing.T) {
	early := time.Date(2026, 9, 22, 9, 0, 0, 0, time.UTC)
	late := early.Add(24 * time.Hour)
	got := MergeWrites(
		[]ConfigWrite{{Project: "api", Config: "prd", WrittenAt: early}, {Project: "web", Config: "dev", WrittenAt: early}},
		[]ConfigWrite{{Project: "api", Config: "prd", WrittenAt: late}},
	)
	if len(got) != 2 || !got[0].WrittenAt.Equal(late) || got[1].Project != "web" {
		t.Fatalf("got %+v", got)
	}
}
