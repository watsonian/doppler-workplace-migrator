package migration

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/doppleruniversity/workplace-migrator/internal/doppler"
)

func apiErr(status int, message, body string, opts ...func(*doppler.APIError)) *doppler.APIError {
	e := &doppler.APIError{
		StatusCode: status,
		Message:    message,
		Body:       body,
		Method:     "GET",
		URL:        "https://api.doppler.com/v3/configs/config/secrets?project=payments&config=prd",
		TokenHint:  "dp.pt.…a91c",
		TokenKind:  doppler.TokenKindPersonal,
		Actor:      "joel.watson@doppler.com",
		Attempts:   3,
		LatencyMS:  61,
	}
	for _, o := range opts {
		o(e)
	}
	return e
}

func TestClassifyError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		side string
		want ErrorClassKey
	}{
		{
			name: "403 naming restricted secrets",
			err:  apiErr(403, "You do not have access to one or more restricted secrets", ""),
			side: SideSource,
			want: ClassRestrictedRead,
		},
		{
			// The API does not always say "restricted". A user-backed token reading
			// secrets and getting a 403 is the same problem regardless of wording.
			name: "403 reading secrets with a user-backed token",
			err:  apiErr(403, "You do not have access to one or more secrets in this config", ""),
			side: SideSource,
			want: ClassRestrictedRead,
		},
		{
			name: "403 from a service account token is about access, not restriction",
			err: apiErr(403, "forbidden", "", func(e *doppler.APIError) {
				e.TokenKind = doppler.TokenKindServiceAccount
			}),
			side: SideSource,
			want: ClassPermissionDenied,
		},
		{
			name: "403 writing the target is about access",
			err: apiErr(403, "forbidden", "", func(e *doppler.APIError) {
				e.URL = "https://doppler.internal.acme.com/v3/configs"
			}),
			side: SideTarget,
			want: ClassPermissionDenied,
		},
		{name: "429", err: apiErr(429, "still rate limited after 10 retries", ""), side: SideTarget, want: ClassRateLimited},
		{name: "409", err: apiErr(409, "already exists", ""), side: SideTarget, want: ClassAlreadyExists},
		{name: "404", err: apiErr(404, "not found", ""), side: SideTarget, want: ClassSubjectNotFound},
		{name: "500", err: apiErr(500, "", "internal"), side: SideTarget, want: ClassServerError},
		{name: "422", err: apiErr(422, "invalid name", ""), side: SideTarget, want: ClassValidationRejected},
		{
			name: "entitlement wording beats the generic 4xx bucket",
			err:  apiErr(400, "user_groups entitlement not enabled", ""),
			side: SideTarget,
			want: ClassEntitlementMissing,
		},
		{name: "non-API error is a transport failure", err: errors.New("dial tcp: timeout"), side: SideTarget, want: ClassNetwork},
		{name: "no error", err: nil, side: SideTarget, want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyError(tc.err, tc.side); got != tc.want {
				t.Errorf("ClassifyError() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestClassifyWrappedError(t *testing.T) {
	// annotateRestrictedReadError wraps the API error in an explanation. The class
	// has to survive that, or every restricted read lands in the unknown bucket.
	wrapped := fmt.Errorf("reading secrets: %w", apiErr(403, "restricted", ""))
	if got := ClassifyError(wrapped, SideSource); got != ClassRestrictedRead {
		t.Errorf("wrapped error classified as %q, want %q", got, ClassRestrictedRead)
	}
}

func TestEvidenceFromCarriesTokenAndRequest(t *testing.T) {
	ev := EvidenceFrom(apiErr(403, "restricted", `{"messages":["nope"]}`), SideSource)

	if ev.TokenHint != "dp.pt.…a91c" {
		t.Errorf("TokenHint = %q", ev.TokenHint)
	}
	if ev.TokenKindLabel != "Personal token" {
		t.Errorf("TokenKindLabel = %q", ev.TokenKindLabel)
	}
	if ev.Actor != "joel.watson@doppler.com" {
		t.Errorf("Actor = %q", ev.Actor)
	}
	if ev.Method != "GET" || !strings.Contains(ev.URL, "project=payments") {
		t.Errorf("request not carried: %s %s", ev.Method, ev.URL)
	}
	if ev.StatusText != "403 Forbidden" {
		t.Errorf("StatusText = %q", ev.StatusText)
	}
	if ev.Body != `{"messages":["nope"]}` {
		t.Errorf("Body = %q", ev.Body)
	}
	if ev.Attempts != 3 || ev.LatencyMS != 61 {
		t.Errorf("attempts/latency = %d/%d", ev.Attempts, ev.LatencyMS)
	}
}

func TestEvidenceNeverCarriesAWholeToken(t *testing.T) {
	// The hint is the only form a token is ever published in. If a full token can
	// reach an Evidence, it can reach the interface, the CSV and the clipboard.
	full := "dp.pt.0123456789abcdef0123456789abcdef"
	e := apiErr(403, "restricted", "")
	e.TokenHint = doppler.TokenHint(full)

	ev := EvidenceFrom(e, SideSource)
	if strings.Contains(ev.TokenHint, "0123456789") {
		t.Fatalf("token hint leaked the token body: %q", ev.TokenHint)
	}
	if ev.TokenHint != "dp.pt.…cdef" {
		t.Errorf("TokenHint = %q, want dp.pt.…cdef", ev.TokenHint)
	}
}

func TestEvidenceBodyIsTruncated(t *testing.T) {
	const bound = 2048
	huge := strings.Repeat("x", bound*3)
	ev := EvidenceFrom(apiErr(500, "", huge), SideTarget)
	if len(ev.Body) > bound+len("\n… truncated") {
		t.Errorf("body kept %d bytes, want it bounded", len(ev.Body))
	}
	if !strings.HasSuffix(ev.Body, "truncated") {
		t.Error("truncation is not signposted")
	}
}

// storeWith files n failures of one class against distinct configs.
func storeWith(t *testing.T, s *ErrorStore, class ErrorClassKey, err error, project string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		s.Record(RecordInput{
			Err:           err,
			Side:          SideSource,
			Phase:         5,
			PhaseName:     "secrets_and_locks",
			Resource:      "secrets",
			ResourceLabel: "Secrets",
			Item:          fmt.Sprintf("%s/cfg%d", project, i),
			Project:       project,
			Config:        fmt.Sprintf("cfg%d", i),
			Weight:        10,
			Class:         class,
		})
	}
}

func TestClassesGroupAndRankByBlastRadius(t *testing.T) {
	s := NewErrorStore()
	storeWith(t, s, ClassRateLimited, apiErr(429, "rate limited", ""), "web", 3)
	storeWith(t, s, ClassRestrictedRead, apiErr(403, "restricted", ""), "payments", 12)

	classes := s.Classes()
	if len(classes) != 2 {
		t.Fatalf("got %d classes, want 2", len(classes))
	}
	// Biggest first: the class that broke the most is the one worth fixing first.
	if classes[0].Key != ClassRestrictedRead || classes[0].Count != 12 {
		t.Errorf("first class = %s (%d), want restricted_read_denied (12)", classes[0].Key, classes[0].Count)
	}
	if classes[0].Weight != 120 {
		t.Errorf("Weight = %d, want 120", classes[0].Weight)
	}
	if classes[0].Title == "" || classes[0].Cause == "" {
		t.Error("class summary carries no explanation")
	}
	if classes[0].Remedy.Kind != RemedyToken {
		t.Errorf("remedy kind = %q, want token", classes[0].Remedy.Kind)
	}
	// A remedy label with a count in it goes stale as the run continues.
	if strings.ContainsAny(classes[0].Remedy.Label, "0123456789") {
		t.Errorf("remedy label carries a count: %q", classes[0].Remedy.Label)
	}
	if classes[0].Sample.TokenHint == "" {
		t.Error("class summary carries no sample evidence")
	}
}

func TestClassesKeepResolvedOnesVisibleButLast(t *testing.T) {
	s := NewErrorStore()
	storeWith(t, s, ClassRestrictedRead, apiErr(403, "restricted", ""), "payments", 5)
	storeWith(t, s, ClassRateLimited, apiErr(429, "rate limited", ""), "web", 2)

	if n := s.Resolve(RecordFilter{Class: ClassRestrictedRead}, "Supplied dp.sa.…7f2c"); n != 5 {
		t.Fatalf("resolved %d, want 5", n)
	}

	classes := s.Classes()
	if len(classes) != 2 {
		t.Fatalf("a resolved class vanished: got %d classes", len(classes))
	}
	if classes[0].Key != ClassRateLimited {
		t.Errorf("open class is not first: %s", classes[0].Key)
	}
	resolved := classes[1]
	if resolved.OpenCount != 0 || resolved.ResolvedCount != 5 {
		t.Errorf("counts = open %d, resolved %d", resolved.OpenCount, resolved.ResolvedCount)
	}
	if resolved.Resolution != "Supplied dp.sa.…7f2c" {
		t.Errorf("resolution = %q", resolved.Resolution)
	}
	if resolved.ResolvedAt.IsZero() {
		t.Error("resolution has no timestamp")
	}
}

func TestResolveOnlySelectedItems(t *testing.T) {
	s := NewErrorStore()
	storeWith(t, s, ClassRestrictedRead, apiErr(403, "restricted", ""), "payments", 4)
	storeWith(t, s, ClassRestrictedRead, apiErr(403, "restricted", ""), "identity", 3)

	// Partial fixes are the point of the selection in triage: fix the projects
	// that matter now, leave the rest open.
	n := s.Resolve(RecordFilter{Class: ClassRestrictedRead, Projects: []string{"payments"}}, "token")
	if n != 4 {
		t.Fatalf("resolved %d, want 4", n)
	}

	open := s.Records(RecordFilter{Class: ClassRestrictedRead, OpenOnly: true})
	if len(open) != 3 {
		t.Fatalf("%d still open, want 3", len(open))
	}
	for _, r := range open {
		if r.Project != "identity" {
			t.Errorf("wrong project left open: %s", r.Project)
		}
	}
}

func TestResolveIsNotRepeatable(t *testing.T) {
	s := NewErrorStore()
	storeWith(t, s, ClassRateLimited, apiErr(429, "rate limited", ""), "web", 3)

	if n := s.Resolve(RecordFilter{Class: ClassRateLimited}, "slower"); n != 3 {
		t.Fatalf("first resolve returned %d, want 3", n)
	}
	// A second click must not re-count the same records as freshly fixed.
	if n := s.Resolve(RecordFilter{Class: ClassRateLimited}, "slower"); n != 0 {
		t.Errorf("second resolve returned %d, want 0", n)
	}
}

func TestRecordsFilterByProject(t *testing.T) {
	s := NewErrorStore()
	storeWith(t, s, ClassRestrictedRead, apiErr(403, "restricted", ""), "payments", 4)
	storeWith(t, s, ClassRestrictedRead, apiErr(403, "restricted", ""), "identity", 2)

	got := s.Records(RecordFilter{Class: ClassRestrictedRead, Projects: []string{"identity"}})
	if len(got) != 2 {
		t.Fatalf("got %d records, want 2", len(got))
	}

	summary := s.Classes()[0]
	if len(summary.Projects) != 2 || summary.Projects[0] != "identity" {
		t.Errorf("project filter options = %v, want sorted [identity payments]", summary.Projects)
	}
}

func TestIsNewClassDrivesTheStopDecision(t *testing.T) {
	s := NewErrorStore()
	if !s.IsNewClass(ClassRateLimited) {
		t.Error("an unseen class should be new")
	}
	storeWith(t, s, ClassRateLimited, apiErr(429, "rate limited", ""), "web", 1)
	if s.IsNewClass(ClassRateLimited) {
		t.Error("a seen class should not stop the run again")
	}
	if !s.IsNewClass(ClassRestrictedRead) {
		t.Error("a different class is still new")
	}
}

func TestCounts(t *testing.T) {
	s := NewErrorStore()
	storeWith(t, s, ClassRestrictedRead, apiErr(403, "restricted", ""), "payments", 5)
	storeWith(t, s, ClassRateLimited, apiErr(429, "rate limited", ""), "web", 2)
	s.Resolve(RecordFilter{Class: ClassRestrictedRead}, "token")

	total, open, classes := s.Counts()
	if total != 7 || open != 2 || classes != 2 {
		t.Errorf("counts = %d total, %d open, %d classes; want 7/2/2", total, open, classes)
	}
	if !s.HasOpenErrors() {
		t.Error("HasOpenErrors should be true with 2 open")
	}
	s.Resolve(RecordFilter{Class: ClassRateLimited}, "slower")
	if s.HasOpenErrors() {
		t.Error("HasOpenErrors should be false once everything is resolved")
	}
}

func TestUnresolvedReferenceIsAWarningNotAFailure(t *testing.T) {
	s := NewErrorStore()
	s.Record(RecordInput{
		Class:     ClassUnresolvedReference,
		Message:   "references payments.prd, not migrated yet",
		Phase:     5,
		PhaseName: "secrets_and_locks",
		Resource:  "secrets",
		Item:      "web/prd",
		Project:   "web",
	})

	c := s.Classes()[0]
	if c.Severity != SeverityWarning {
		t.Errorf("severity = %q, want warning", c.Severity)
	}
	if c.Remedy.Kind != RemedyNone {
		t.Errorf("remedy = %q, want none — nothing failed", c.Remedy.Kind)
	}
}

func TestCSVQuotesBodiesContainingCommasAndNewlines(t *testing.T) {
	s := NewErrorStore()
	s.Record(RecordInput{
		Err:       apiErr(403, "restricted", "{\"messages\":[\"a, b\",\n\"c\"]}"),
		Side:      SideSource,
		Phase:     5,
		PhaseName: "secrets_and_locks",
		Resource:  "secrets",
		Item:      "payments/prd",
		Project:   "payments",
	})

	csv := s.CSV()
	lines := strings.Split(strings.TrimRight(csv, "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected a header and at least one row, got %d lines", len(lines))
	}
	if !strings.Contains(csv, "token_hint") || !strings.Contains(csv, "dp.pt.…a91c") {
		t.Error("CSV drops the evidence that makes it worth exporting")
	}
	// The body holds a comma and a newline; unquoted, it would shear the row.
	if !strings.Contains(csv, `"{""messages"":[""a, b"",`) {
		t.Errorf("body not CSV-quoted:\n%s", csv)
	}
}

func TestClassesForPhaseNestsUnderTheRightPhase(t *testing.T) {
	s := NewErrorStore()
	storeWith(t, s, ClassRestrictedRead, apiErr(403, "restricted", ""), "payments", 2)
	s.Record(RecordInput{
		Err: apiErr(409, "already exists", ""), Side: SideTarget,
		Phase: 4, PhaseName: "configs", Resource: "configs",
		Item: "web/prd", Project: "web", Class: ClassAlreadyExists,
	})

	if got := s.ClassesForPhase(5); len(got) != 1 || got[0].Key != ClassRestrictedRead {
		t.Errorf("phase 5 classes = %v", got)
	}
	if got := s.ClassesForPhase(4); len(got) != 1 || got[0].Key != ClassAlreadyExists {
		t.Errorf("phase 4 classes = %v", got)
	}
	if got := s.ClassesForPhase(9); len(got) != 0 {
		t.Errorf("phase 9 should have no classes, got %d", len(got))
	}
}
