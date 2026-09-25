package migration

import (
	"strings"
	"testing"
)

// rerunFixture builds an executor whose store holds failures of two classes in
// two projects, with checkpoint coordinates so a re-run can target them.
func rerunFixture(t *testing.T) *Executor {
	t.Helper()

	e := &Executor{
		checkpoint: NewCheckpointManager(t.TempDir() + "/checkpoint.json"),
		errors:     NewErrorStore(),
		onProgress: func(MigrateProgress) {},
	}

	file := func(class ErrorClassKey, project, config string, weight int) {
		key := "secrets:" + project + "/" + config
		e.checkpoint.SetItemStatus("secrets_and_locks", key, StatusFailed, "boom")
		e.errors.Record(RecordInput{
			Err:             apiErr(403, "restricted", ""),
			Side:            SideSource,
			Phase:           5,
			PhaseName:       "secrets_and_locks",
			Resource:        "secrets",
			ResourceLabel:   "Secrets",
			Item:            project + "/" + config,
			Project:         project,
			Config:          config,
			Weight:          weight,
			Class:           class,
			CheckpointPhase: "secrets_and_locks",
			CheckpointKey:   key,
		})
	}

	file(ClassRestrictedRead, "payments", "prd", 128)
	file(ClassRestrictedRead, "payments", "stg", 126)
	file(ClassRestrictedRead, "identity", "prd", 84)
	file(ClassRateLimited, "web", "prd", 40)

	return e
}

func TestPrepareRerunScopesToTheSelection(t *testing.T) {
	e := rerunFixture(t)

	plan, err := e.PrepareRerun(RerunRequest{
		Class:    ClassRestrictedRead,
		Remedy:   RemedyToken,
		Token:    "dp.sa.0000000000000000000000007f2c",
		Projects: []string{"payments"},
	})
	if err != nil {
		t.Fatalf("PrepareRerun: %v", err)
	}

	if plan.ItemCount != 2 {
		t.Errorf("ItemCount = %d, want 2 — the identity project was not selected", plan.ItemCount)
	}
	if plan.Weight != 254 {
		t.Errorf("Weight = %d, want 254", plan.Weight)
	}
	if !plan.NeedsRun {
		t.Error("a token remedy has to actually re-run")
	}

	// The failing read means these configs hold nothing in memory to rewrite.
	if len(plan.RegatherConfigs) != 2 {
		t.Fatalf("RegatherConfigs = %v, want both payments configs", plan.RegatherConfigs)
	}
	if plan.RegatherConfigs[0] != (ConfigRef{Project: "payments", Config: "prd"}) {
		t.Errorf("RegatherConfigs not sorted: %v", plan.RegatherConfigs)
	}

	// Only the chosen items are walked back; the others must still be skipped on
	// the next run, or fixing one class silently retries another's failures.
	if got := e.checkpoint.GetItemStatus("secrets_and_locks", "secrets:payments/prd"); got != StatusPending {
		t.Errorf("selected item status = %q, want pending", got)
	}
	if got := e.checkpoint.GetItemStatus("secrets_and_locks", "secrets:identity/prd"); got != StatusFailed {
		t.Errorf("unselected item status = %q, want it left alone", got)
	}
	if got := e.checkpoint.GetItemStatus("secrets_and_locks", "secrets:web/prd"); got != StatusFailed {
		t.Errorf("other class status = %q, want it left alone", got)
	}
}

func TestPrepareRerunReopensACompletedPhase(t *testing.T) {
	e := rerunFixture(t)
	e.checkpoint.CompletePhase("secrets_and_locks")
	if !e.checkpoint.IsPhaseComplete("secrets_and_locks") {
		t.Fatal("fixture did not complete the phase")
	}

	if _, err := e.PrepareRerun(RerunRequest{
		Class: ClassRateLimited, Remedy: RemedyPace, PaceMS: 250,
	}); err != nil {
		t.Fatalf("PrepareRerun: %v", err)
	}

	// A completed phase is skipped wholesale, so without reopening it the reset
	// items are never revisited and the re-run silently does nothing.
	if e.checkpoint.IsPhaseComplete("secrets_and_locks") {
		t.Error("phase still marked complete; the re-run would skip it")
	}
}

func TestPrepareRerunRejectsTheWrongRemedy(t *testing.T) {
	e := rerunFixture(t)

	_, err := e.PrepareRerun(RerunRequest{
		Class: ClassRestrictedRead, Remedy: RemedyPace, PaceMS: 100,
	})
	if err == nil {
		t.Fatal("pacing does not fix a permissions problem; expected a refusal")
	}
	if !strings.Contains(err.Error(), "token") {
		t.Errorf("error should name the right remedy, got: %v", err)
	}

	// And nothing should have been disturbed on the way to refusing.
	if got := e.checkpoint.GetItemStatus("secrets_and_locks", "secrets:payments/prd"); got != StatusFailed {
		t.Errorf("checkpoint touched despite refusal: %q", got)
	}
}

func TestPrepareRerunRequiresItsInput(t *testing.T) {
	tests := []struct {
		name string
		req  RerunRequest
		want string
	}{
		{"token remedy with no token", RerunRequest{Class: ClassRestrictedRead, Remedy: RemedyToken}, "needs a replacement token"},
		{"pace remedy with no gap", RerunRequest{Class: ClassRateLimited, Remedy: RemedyPace}, "needs a gap"},
		{"absurd pace", RerunRequest{Class: ClassRateLimited, Remedy: RemedyPace, PaceMS: 120000}, "longer than waiting"},
		{"no class", RerunRequest{Remedy: RemedyToken, Token: "dp.sa.x"}, "no error class"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := rerunFixture(t)
			_, err := e.PrepareRerun(tc.req)
			if err == nil {
				t.Fatalf("expected a refusal")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestPrepareRerunRefusesAWarningClass(t *testing.T) {
	e := rerunFixture(t)
	e.errors.Record(RecordInput{
		Class: ClassUnresolvedReference, Message: "references payments.prd",
		Phase: 5, PhaseName: "secrets_and_locks", Resource: "secrets", Item: "web/stg",
	})

	_, err := e.PrepareRerun(RerunRequest{Class: ClassUnresolvedReference})
	if err == nil {
		t.Fatal("nothing failed, so there is nothing to re-run")
	}
	if !strings.Contains(err.Error(), "needs no action") {
		t.Errorf("error = %q", err)
	}
}

func TestFinishRerunOnlyResolvesWhatActuallySucceeded(t *testing.T) {
	e := rerunFixture(t)

	plan, err := e.PrepareRerun(RerunRequest{
		Class:  ClassRestrictedRead,
		Remedy: RemedyToken,
		Token:  "dp.sa.0000000000000000000000007f2c",
	})
	if err != nil {
		t.Fatalf("PrepareRerun: %v", err)
	}

	// Two of the three land; the third fails the same way again.
	e.checkpoint.SetItemStatus("secrets_and_locks", "secrets:payments/prd", StatusCompleted, "")
	e.checkpoint.SetItemStatus("secrets_and_locks", "secrets:payments/stg", StatusCompleted, "")
	e.checkpoint.SetItemStatus("secrets_and_locks", "secrets:identity/prd", StatusFailed, "still 403")

	if n := e.FinishRerun(plan); n != 2 {
		t.Fatalf("resolved %d, want 2 — a re-run that failed again is not a fix", n)
	}

	open := e.errors.Records(RecordFilter{Class: ClassRestrictedRead, OpenOnly: true})
	if len(open) != 1 || open[0].Project != "identity" {
		t.Errorf("wrong record left open: %+v", open)
	}
}

func TestFinishRerunRecordsWhatWasDoneWithoutTheToken(t *testing.T) {
	e := rerunFixture(t)
	const token = "dp.sa.0123456789abcdef0123456789ab7f2c"

	plan, err := e.PrepareRerun(RerunRequest{
		Class: ClassRestrictedRead, Remedy: RemedyToken, Token: token,
	})
	if err != nil {
		t.Fatalf("PrepareRerun: %v", err)
	}
	for _, rec := range plan.Records {
		e.checkpoint.SetItemStatus(rec.CheckpointPhase, rec.CheckpointKey, StatusCompleted, "")
	}
	e.FinishRerun(plan)

	summary := findClass(t, e.errors.Classes(), ClassRestrictedRead)
	if !strings.Contains(summary.Resolution, "dp.sa.…7f2c") {
		t.Errorf("resolution should name the token that fixed it: %q", summary.Resolution)
	}
	// Resolutions are exported. A whole token must never reach one.
	if strings.Contains(summary.Resolution, "0123456789") {
		t.Fatalf("resolution leaked the token: %q", summary.Resolution)
	}
	if !strings.Contains(summary.Resolution, "3 items") {
		t.Errorf("resolution should say how much it covered: %q", summary.Resolution)
	}
}

func TestKeepTargetResolvesWithoutRunningAnything(t *testing.T) {
	e := &Executor{
		checkpoint: NewCheckpointManager(t.TempDir() + "/checkpoint.json"),
		errors:     NewErrorStore(),
		onProgress: func(MigrateProgress) {},
	}
	e.checkpoint.SetItemStatus("configs", "config:web/prd", StatusFailed, "409")
	e.errors.Record(RecordInput{
		Err: apiErr(409, "already exists", ""), Side: SideTarget,
		Phase: 4, PhaseName: "configs", Resource: "configs", ResourceLabel: "Configs",
		Item: "web/prd", Project: "web", Config: "prd", Class: ClassAlreadyExists,
		CheckpointPhase: "configs", CheckpointKey: "config:web/prd",
	})

	plan, err := e.PrepareRerun(RerunRequest{
		Class: ClassAlreadyExists, Remedy: RemedyDecision, Choice: "keep_target",
	})
	if err != nil {
		t.Fatalf("PrepareRerun: %v", err)
	}
	if plan.NeedsRun {
		t.Error("keeping the target's version writes nothing; it should not need a run")
	}
	// Nothing is going to re-run, so the checkpoint must be left as it was.
	if got := e.checkpoint.GetItemStatus("configs", "config:web/prd"); got != StatusFailed {
		t.Errorf("checkpoint reset for a remedy that writes nothing: %q", got)
	}

	if n := e.FinishRerun(plan); n != 1 {
		t.Fatalf("resolved %d, want 1", n)
	}
	summary := findClass(t, e.errors.Classes(), ClassAlreadyExists)
	if !strings.Contains(summary.Resolution, "Keep The Target's Version") {
		t.Errorf("resolution = %q", summary.Resolution)
	}
}

func TestRerunRejectsAnUnknownChoice(t *testing.T) {
	e := rerunFixture(t)
	e.errors.Record(RecordInput{
		Err: apiErr(409, "already exists", ""), Side: SideTarget,
		Phase: 4, PhaseName: "configs", Resource: "configs",
		Item: "web/prd", Project: "web", Config: "prd", Class: ClassAlreadyExists,
	})

	_, err := e.PrepareRerun(RerunRequest{
		Class: ClassAlreadyExists, Remedy: RemedyDecision, Choice: "delete_everything",
	})
	if err == nil {
		t.Fatal("expected an unrecognized choice to be refused")
	}
}

func findClass(t *testing.T, classes []ClassSummary, key ErrorClassKey) ClassSummary {
	t.Helper()
	for _, c := range classes {
		if c.Key == key {
			return c
		}
	}
	t.Fatalf("class %q not found", key)
	return ClassSummary{}
}
