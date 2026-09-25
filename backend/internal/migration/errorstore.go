package migration

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/doppler/onprem-migrator/internal/doppler"
)

// Evidence is a Doppler API exchange, recorded so a failure can be diagnosed.
// Defined in the doppler package, where the exchange happens; aliased here
// because it reads as part of an error record everywhere it is used.
type Evidence = doppler.Evidence

// EvidenceFrom builds an Evidence from an error.
var EvidenceFrom = doppler.EvidenceFrom

// ErrorRecord is one failure: what was being done, to what, and what came back.
type ErrorRecord struct {
	ID        string        `json:"id"`
	Class     ErrorClassKey `json:"class"`
	Phase     int           `json:"phase"`
	PhaseName string        `json:"phase_name"`

	// Resource is the kind of thing that failed ("config", "group", "webhook"),
	// and ResourceLabel is how it reads in a table heading ("Secrets", "Groups").
	Resource      string `json:"resource"`
	ResourceLabel string `json:"resource_label"`

	// Item is the identifier shown to the user, e.g. "payments/prd". Project is
	// pulled out separately because filtering by project is the only way a list of
	// several hundred becomes selectable.
	Item    string `json:"item"`
	Project string `json:"project"`
	Config  string `json:"config,omitempty"`

	// Size is a short phrase describing how much is at stake for this item, e.g.
	// "128 secrets · 7 restricted". Shown beside the item in the triage list.
	Size string `json:"size,omitempty"`
	// Weight is the same thing as a number, so a selection can total it up.
	Weight int `json:"weight,omitempty"`

	Message    string    `json:"message"`
	Evidence   Evidence  `json:"evidence"`
	OccurredAt time.Time `json:"occurred_at"`

	Resolved   bool      `json:"resolved"`
	ResolvedAt time.Time `json:"resolved_at,omitempty"`
	Resolution string    `json:"resolution,omitempty"`

	// CheckpointPhase and CheckpointKey locate this item in the checkpoint, so a
	// re-run can reset exactly the items selected. Not published: they are an
	// internal coordinate system and mean nothing to whoever reads the export.
	CheckpointPhase string `json:"-"`
	CheckpointKey   string `json:"-"`
}

// ClassSummary is a whole class of failures as the interface shows it: one row in
// triage, one nested entry under a phase in the timeline.
type ClassSummary struct {
	ErrorClass

	Phase         int    `json:"phase"`
	PhaseName     string `json:"phase_name"`
	Resource      string `json:"resource"`
	ResourceLabel string `json:"resource_label"`

	Count         int `json:"count"`
	ResolvedCount int `json:"resolved_count"`
	OpenCount     int `json:"open_count"`
	// Weight totals the per-item weights, e.g. how many secrets are at stake.
	Weight int `json:"weight"`

	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`

	// Sample is the evidence from the first occurrence. Every member of a class
	// failed the same way, so one exchange explains all of them.
	Sample Evidence `json:"sample"`

	// Projects lists the distinct projects this class touched, for the filter.
	Projects []string `json:"projects,omitempty"`

	Resolution string    `json:"resolution,omitempty"`
	ResolvedAt time.Time `json:"resolved_at,omitempty"`
}

// ErrorStore collects failures and groups them by class.
//
// It is written to from the executor goroutine and read from HTTP handlers, so
// every method takes the lock. Records are kept in arrival order; grouping is
// derived on read rather than maintained, which keeps the write path cheap.
type ErrorStore struct {
	mu      sync.RWMutex
	records []*ErrorRecord
	nextID  int

	// resolutions records what was done about a class, kept even after every
	// member is resolved so the summary and the export can say how it was cleared.
	resolutions map[ErrorClassKey]classResolution
}

type classResolution struct {
	text string
	at   time.Time
}

// NewErrorStore creates an empty store.
func NewErrorStore() *ErrorStore {
	return &ErrorStore{resolutions: map[ErrorClassKey]classResolution{}}
}

// RecordInput is what the executor knows at the moment something fails.
type RecordInput struct {
	Err           error
	Side          string
	Phase         int
	PhaseName     string
	Resource      string
	ResourceLabel string
	Item          string
	Project       string
	Config        string
	Size          string
	Weight        int
	// Class overrides classification. Used for the cases the executor recognizes
	// itself — an unresolved cross-config reference is a warning, not an HTTP
	// failure, and has no APIError to classify.
	Class ErrorClassKey
	// Message overrides the error text, for the same reason.
	Message string
	// CheckpointPhase and CheckpointKey locate the failed item for a re-run.
	CheckpointPhase string
	CheckpointKey   string
}

// Record adds a failure and returns the record it created.
func (s *ErrorStore) Record(in RecordInput) *ErrorRecord {
	s.mu.Lock()
	defer s.mu.Unlock()

	class := in.Class
	if class == "" {
		class = ClassifyError(in.Err, in.Side)
	}
	if class == "" {
		class = ClassUnknown
	}

	message := in.Message
	if message == "" && in.Err != nil {
		message = in.Err.Error()
	}

	s.nextID++
	rec := &ErrorRecord{
		ID:              fmt.Sprintf("e%d", s.nextID),
		Class:           class,
		Phase:           in.Phase,
		PhaseName:       in.PhaseName,
		Resource:        in.Resource,
		ResourceLabel:   in.ResourceLabel,
		Item:            in.Item,
		Project:         in.Project,
		Config:          in.Config,
		Size:            in.Size,
		Weight:          in.Weight,
		Message:         message,
		Evidence:        EvidenceFrom(in.Err, in.Side),
		OccurredAt:      time.Now(),
		CheckpointPhase: in.CheckpointPhase,
		CheckpointKey:   in.CheckpointKey,
	}
	s.records = append(s.records, rec)
	return rec
}

// Classes returns one summary per class, ordered by blast radius: open classes
// before resolved ones, then by how many items each broke.
func (s *ErrorStore) Classes() []ClassSummary {
	s.mu.RLock()
	defer s.mu.RUnlock()

	type acc struct {
		sum      *ClassSummary
		projects map[string]struct{}
	}
	byClass := map[ErrorClassKey]*acc{}
	order := []ErrorClassKey{}

	for _, rec := range s.records {
		a, ok := byClass[rec.Class]
		if !ok {
			cs := ClassSummary{
				ErrorClass:    LookupErrorClass(rec.Class),
				Phase:         rec.Phase,
				PhaseName:     rec.PhaseName,
				Resource:      rec.Resource,
				ResourceLabel: rec.ResourceLabel,
				FirstSeen:     rec.OccurredAt,
				Sample:        rec.Evidence,
			}
			a = &acc{sum: &cs, projects: map[string]struct{}{}}
			byClass[rec.Class] = a
			order = append(order, rec.Class)
		}
		a.sum.Count++
		a.sum.Weight += rec.Weight
		if rec.Resolved {
			a.sum.ResolvedCount++
		} else {
			a.sum.OpenCount++
		}
		if rec.OccurredAt.After(a.sum.LastSeen) {
			a.sum.LastSeen = rec.OccurredAt
		}
		if rec.Project != "" {
			a.projects[rec.Project] = struct{}{}
		}
		// A class can span phases — a 429 can hit configs and secrets alike. The
		// phase shown is the earliest, which is where the timeline nests it.
		if rec.Phase < a.sum.Phase {
			a.sum.Phase = rec.Phase
			a.sum.PhaseName = rec.PhaseName
		}
	}

	out := make([]ClassSummary, 0, len(order))
	for _, key := range order {
		a := byClass[key]
		a.sum.Projects = sortedKeys(a.projects)
		if res, ok := s.resolutions[key]; ok {
			a.sum.Resolution = res.text
			a.sum.ResolvedAt = res.at
		}
		out = append(out, *a.sum)
	}

	sort.SliceStable(out, func(i, j int) bool {
		// Open before resolved, then biggest first. A resolved class still shows —
		// the run's history should not rewrite itself as things get fixed.
		iOpen, jOpen := out[i].OpenCount > 0, out[j].OpenCount > 0
		if iOpen != jOpen {
			return iOpen
		}
		if out[i].OpenCount != out[j].OpenCount {
			return out[i].OpenCount > out[j].OpenCount
		}
		return out[i].Count > out[j].Count
	})

	return out
}

// ClassesForPhase returns the classes produced by one phase, for nesting under it
// in the timeline.
func (s *ErrorStore) ClassesForPhase(phase int) []ClassSummary {
	all := s.Classes()
	out := []ClassSummary{}
	for _, c := range all {
		if c.Phase == phase {
			out = append(out, c)
		}
	}
	return out
}

// RecordFilter narrows a class's instances.
type RecordFilter struct {
	Class ErrorClassKey
	// Projects, when non-empty, keeps only records in those projects.
	Projects []string
	// OpenOnly drops records already resolved.
	OpenOnly bool
	// IDs, when non-empty, keeps only those records.
	IDs []string
}

// Records returns the instances matching a filter, in the order they occurred.
func (s *ErrorStore) Records(f RecordFilter) []ErrorRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.recordsLocked(f)
}

func (s *ErrorStore) recordsLocked(f RecordFilter) []ErrorRecord {
	projects := toSet(f.Projects)
	ids := toSet(f.IDs)

	out := []ErrorRecord{}
	for _, rec := range s.records {
		if f.Class != "" && rec.Class != f.Class {
			continue
		}
		if f.OpenOnly && rec.Resolved {
			continue
		}
		if len(projects) > 0 {
			if _, ok := projects[rec.Project]; !ok {
				continue
			}
		}
		if len(ids) > 0 {
			if _, ok := ids[rec.ID]; !ok {
				continue
			}
		}
		out = append(out, *rec)
	}
	return out
}

// Resolve marks records as resolved and records what cleared them. The resolution
// text is kept against the class, because that is what the summary reports.
func (s *ErrorStore) Resolve(f RecordFilter, resolution string) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	projects := toSet(f.Projects)
	ids := toSet(f.IDs)

	n := 0
	for _, rec := range s.records {
		if rec.Resolved {
			continue
		}
		if f.Class != "" && rec.Class != f.Class {
			continue
		}
		if len(projects) > 0 {
			if _, ok := projects[rec.Project]; !ok {
				continue
			}
		}
		if len(ids) > 0 {
			if _, ok := ids[rec.ID]; !ok {
				continue
			}
		}
		rec.Resolved = true
		rec.ResolvedAt = now
		rec.Resolution = resolution
		n++
	}

	if n > 0 && f.Class != "" {
		s.resolutions[f.Class] = classResolution{text: resolution, at: now}
	}
	return n
}

// Counts reports the totals the header band and the summary tiles need.
func (s *ErrorStore) Counts() (total, open, classes int) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	seen := map[ErrorClassKey]struct{}{}
	for _, rec := range s.records {
		total++
		if !rec.Resolved {
			open++
		}
		seen[rec.Class] = struct{}{}
	}
	return total, open, len(seen)
}

// HasOpenErrors reports whether anything is still unresolved.
func (s *ErrorStore) HasOpenErrors() bool {
	_, open, _ := s.Counts()
	return open > 0
}

// IsNewClass reports whether a class has been seen before. The executor uses it to
// decide whether to pause: a repeat of a known problem should not stop the run,
// but the first of a new one should, before it burns through the whole workplace.
func (s *ErrorStore) IsNewClass(key ErrorClassKey) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, rec := range s.records {
		if rec.Class == key {
			return false
		}
	}
	return true
}

// CSV renders every record as a spreadsheet, evidence included. This is what gets
// handed to whoever was not in the room.
func (s *ErrorStore) CSV() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var b strings.Builder
	b.WriteString("id,class,class_title,phase,phase_name,resource,item,project,size,occurred_at,resolved,resolved_at,resolution,side,token_hint,token_kind,actor,method,url,status,attempts,message,body\n")
	for _, rec := range s.records {
		class := LookupErrorClass(rec.Class)
		row := []string{
			rec.ID,
			string(rec.Class),
			class.Title,
			fmt.Sprintf("%d", rec.Phase),
			rec.PhaseName,
			rec.Resource,
			rec.Item,
			rec.Project,
			rec.Size,
			rec.OccurredAt.Format(time.RFC3339),
			fmt.Sprintf("%t", rec.Resolved),
			formatTime(rec.ResolvedAt),
			rec.Resolution,
			rec.Evidence.Side,
			rec.Evidence.TokenHint,
			rec.Evidence.TokenKind,
			rec.Evidence.Actor,
			rec.Evidence.Method,
			rec.Evidence.URL,
			fmt.Sprintf("%d", rec.Evidence.Status),
			fmt.Sprintf("%d", rec.Evidence.Attempts),
			rec.Message,
			rec.Evidence.Body,
		}
		for i, field := range row {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(csvField(field))
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

// csvField quotes a value if it contains anything that would break a row, and
// doubles any embedded quotes. Response bodies contain both commas and newlines,
// so this is not optional.
func csvField(s string) string {
	if !strings.ContainsAny(s, ",\"\n\r") {
		return s
	}
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

func toSet(values []string) map[string]struct{} {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(values))
	for _, v := range values {
		if v != "" {
			out[v] = struct{}{}
		}
	}
	return out
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
