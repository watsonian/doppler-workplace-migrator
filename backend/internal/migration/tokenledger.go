package migration

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// The ledger is what stops a token quietly going missing.
//
// A token's value cannot be read from the source and is shown once on the
// target, so the migrator can neither copy one across nor recover one it has
// already issued. That leaves exactly two honest outcomes per token: it was
// issued here and you copied it, or you are dealing with it yourself. The
// ledger records which, and cutover refuses to run while any token is neither —
// because cutover removes the write access issuing needs, and there is no way
// back once it has.
//
// Nothing in here is a secret. The values never reach it.

// TokenKind distinguishes the two sorts, which live behind different endpoints.
type TokenKind string

const (
	KindServiceToken        TokenKind = "service_token"
	KindServiceAccountToken TokenKind = "service_account_token"
)

// TokenDisposition is where a token has ended up.
type TokenDisposition string

const (
	// DispPending is undecided. It and DispDeferred are the dispositions that block cutover.
	DispPending TokenDisposition = "pending"
	// DispIssued means it exists on the target and its value was shown once.
	DispIssued TokenDisposition = "issued"
	// DispToRecreate means it is yours to rebuild by hand; it goes on the worklist.
	DispToRecreate TokenDisposition = "to_recreate"
	// DispNotNeeded means it was deliberately dropped, and is kept off the
	// worklist so that list stays a list of work.
	DispNotNeeded TokenDisposition = "not_needed"
	// DispDeferred is "later": set aside for now, not outstanding on the page,
	// and back to pending at the next run, to be offered again. It still
	// blocks cutover — cutover removes the write access issuing one needs, so
	// deferring past it would be skipping it.
	DispDeferred TokenDisposition = "deferred"
)

// Handled reports whether this disposition lets cutover proceed.
func (d TokenDisposition) Handled() bool { return d != DispPending && d != DispDeferred }

// maxTokenLifetime is the furthest ahead the target will accept an expiry.
const maxTokenLifetime = 365 * 24 * time.Hour

// TokenRecord is one row of the worklist.
type TokenRecord struct {
	Key  string    `json:"key"`
	Kind TokenKind `json:"kind"`

	// Placement. Project/Config/Environment for a service token; ServiceAccount
	// for the other. Access is empty for service account tokens, which have none.
	Project        string `json:"project,omitempty"`
	Config         string `json:"config,omitempty"`
	Environment    string `json:"environment,omitempty"`
	ServiceAccount string `json:"service_account,omitempty"`
	// ServiceAccountName is how the account reads. Its slug is a UUID, and a
	// different one on each instance, so it identifies nothing to a person.
	ServiceAccountName string `json:"service_account_name,omitempty"`
	Name               string `json:"name"`
	Access             string `json:"access,omitempty"`

	SourceExpiresAt *time.Time `json:"source_expires_at,omitempty"`

	Disposition TokenDisposition `json:"disposition"`
	IssuedAt    *time.Time       `json:"issued_at,omitempty"`

	// TargetSlug identifies what was created, so a reroll can delete it. It is
	// an identifier, not a credential.
	TargetSlug string `json:"target_slug,omitempty"`

	// IssuedExpiresAt is what the token was actually given, which differs from
	// the source when the cap applied.
	IssuedExpiresAt *time.Time `json:"issued_expires_at,omitempty"`
	ExpiryShortened bool       `json:"expiry_shortened,omitempty"`

	// Note carries the reason a row looks the way it does — a failure message, or
	// why something closed itself. Never a value.
	Note string `json:"note,omitempty"`

	// FailedAt is when the attempt behind a failure note was made, so a
	// failure from before the token's project was migrated can be recognized
	// as describing a target that no longer exists.
	FailedAt *time.Time `json:"failed_at,omitempty"`

	// PreexistingOnTarget marks a token this migrator did not issue in this
	// session but found on the target under the same name. Issuing again would
	// add a second token rather than replace the first, so these offer a reroll.
	PreexistingOnTarget bool `json:"preexisting_on_target,omitempty"`
}

// Scope is the human-readable placement, used in messages and the UI.
func (r *TokenRecord) Scope() string {
	if r.Kind == KindServiceAccountToken {
		if r.ServiceAccountName != "" {
			return r.ServiceAccountName
		}
		return r.ServiceAccount
	}
	return r.Project + "/" + r.Config
}

// ServiceTokenKey and ServiceAccountTokenKey build the stable identity of a row.
// Name within a config (or within an account) is all the API gives us to match
// on — there is no link from a target token back to the source one it copies.
func ServiceTokenKey(project, config, name string) string {
	return "st:" + project + "/" + config + "/" + name
}

func ServiceAccountTokenKey(account, name string) string {
	return "sa:" + account + "/" + name
}

// TokenCounts is the summary the progress strip and the cutover gate both read.
type TokenCounts struct {
	Total      int `json:"total"`
	Issued     int `json:"issued"`
	ToRecreate int `json:"to_recreate"`
	NotNeeded  int `json:"not_needed"`
	Pending    int `json:"pending"`
	// Deferred are set aside until the next run. Not outstanding now, but not
	// decided either: cutover waits on them.
	Deferred int `json:"deferred"`
}

// Handled is everything decided: not undecided, and not deferred.
func (c TokenCounts) Handled() int { return c.Total - c.Pending - c.Deferred }

// TokenLedger holds one record per token, and persists them.
type TokenLedger struct {
	mu       sync.RWMutex
	records  map[string]*TokenRecord
	order    []string
	filePath string

	// inScope, when set, limits every reader to the tokens whose project or
	// service account has been migrated. Seeding adds every gathered token —
	// often before a scope is chosen — and a partial run's token page must not
	// offer to issue tokens for projects the target does not have. Records out
	// of scope are kept, decisions and all, and reappear once migrated.
	inScope func(TokenRecord) bool
}

// SetScope limits the ledger to the records the filter accepts; nil lifts it.
func (l *TokenLedger) SetScope(inScope func(TokenRecord) bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.inScope = inScope
}

// visibleLocked reports whether a record is in scope.
func (l *TokenLedger) visibleLocked(rec *TokenRecord) bool {
	return l.inScope == nil || l.inScope(*rec)
}

// NotYetInScope counts the records held back because their project or
// service account has not been migrated.
func (l *TokenLedger) NotYetInScope() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	n := 0
	for _, rec := range l.records {
		if !l.visibleLocked(rec) {
			n++
		}
	}
	return n
}

// InScope reports whether a record exists and is in scope.
func (l *TokenLedger) InScope(key string) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	rec, ok := l.records[key]
	return ok && l.visibleLocked(rec)
}

// NewTokenLedger creates an empty ledger backed by filePath.
func NewTokenLedger(filePath string) *TokenLedger {
	return &TokenLedger{records: map[string]*TokenRecord{}, filePath: filePath}
}

// ClampExpiry works out the expiry a newly created token can actually carry.
//
// The target refuses a time in the past and caps the rest a year out, so a
// source expiry is reproducible, shortenable, or evidence the token is already
// dead. Shortening errs toward a token that lives less long than its source,
// never longer, which is the safe direction to be wrong in.
func ClampExpiry(source *time.Time, now time.Time) (expiry *time.Time, shortened, expired bool) {
	if source == nil {
		return nil, false, false
	}
	if !source.After(now) {
		return nil, false, true
	}
	if max := now.Add(maxTokenLifetime); source.After(max) {
		capped := max
		return &capped, true, false
	}
	exp := *source
	return &exp, false, false
}

// Seed builds the ledger from gathered source data, leaving existing decisions
// alone so a resumed session does not offer to issue everything again.
func (l *TokenLedger) Seed(data *MigrationData, now time.Time) {
	if data == nil {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	roles := map[string]string{}
	names := map[string]string{}
	for _, sa := range data.ServiceAccounts {
		names[sa.Slug] = sa.Name
		if sa.WorkplaceRole != nil {
			roles[sa.Slug] = sa.WorkplaceRole.Identifier
		}
	}

	// A ledger from before names were recorded gets them now.
	for _, rec := range l.records {
		if rec.Kind == KindServiceAccountToken && rec.ServiceAccountName == "" {
			rec.ServiceAccountName = names[rec.ServiceAccount]
		}
	}

	// The source token's own account is not migrated, so its tokens are not
	// the target's to have. A ledger seeded before it was left out may still
	// offer them; anything not yet issued goes.
	if ex := data.ExcludedServiceAccount; ex != nil {
		kept := l.order[:0]
		for _, key := range l.order {
			rec := l.records[key]
			if rec != nil && rec.Kind == KindServiceAccountToken && rec.ServiceAccount == ex.Slug && rec.Disposition != DispIssued {
				delete(l.records, key)
				continue
			}
			kept = append(kept, key)
		}
		l.order = kept
	}

	for _, st := range data.ServiceTokens {
		rec := &TokenRecord{
			Key:             ServiceTokenKey(st.Project, st.Config, st.Name),
			Kind:            KindServiceToken,
			Project:         st.Project,
			Config:          st.Config,
			Environment:     st.Environment,
			Name:            st.Name,
			Access:          st.Access,
			SourceExpiresAt: parseTokenTime(st.ExpiresAt),
		}
		l.addLocked(rec, now)
	}

	for _, sat := range data.ServiceAccountTokens {
		name := sat.Name
		if name == "" {
			// The API allows an unnamed token; the slug is then the only handle.
			name = sat.Slug
		}
		rec := &TokenRecord{
			Key:                ServiceAccountTokenKey(sat.ServiceAccount, name),
			Kind:               KindServiceAccountToken,
			ServiceAccount:     sat.ServiceAccount,
			ServiceAccountName: names[sat.ServiceAccount],
			Name:               name,
			Access:             roles[sat.ServiceAccount],
			SourceExpiresAt:    parseTokenTime(sat.ExpiresAt),
		}
		l.addLocked(rec, now)
	}
}

// addLocked inserts a record unless a decision about it already exists.
func (l *TokenLedger) addLocked(rec *TokenRecord, now time.Time) {
	if _, exists := l.records[rec.Key]; exists {
		return
	}

	// A token that has already expired on the source closes itself. Reissuing it
	// would hand out access the source no longer grants, and leaving it pending
	// would deadlock the cutover gate against a row with no usable action.
	if _, _, expired := ClampExpiry(rec.SourceExpiresAt, now); expired {
		rec.Disposition = DispNotNeeded
		rec.Note = "already expired on the source"
	} else {
		rec.Disposition = DispPending
	}

	l.records[rec.Key] = rec
	l.order = append(l.order, rec.Key)
}

// Get returns a copy of one record.
func (l *TokenLedger) Get(key string) (TokenRecord, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	rec, ok := l.records[key]
	if !ok {
		return TokenRecord{}, false
	}
	return *rec, true
}

// Records returns every row, in the order they were seeded.
func (l *TokenLedger) Records() []TokenRecord {
	l.mu.RLock()
	defer l.mu.RUnlock()

	out := make([]TokenRecord, 0, len(l.order))
	for _, key := range l.order {
		if rec, ok := l.records[key]; ok && l.visibleLocked(rec) {
			out = append(out, *rec)
		}
	}
	return out
}

// Counts summarises the ledger.
func (l *TokenLedger) Counts() TokenCounts {
	l.mu.RLock()
	defer l.mu.RUnlock()

	var c TokenCounts
	for _, rec := range l.records {
		if !l.visibleLocked(rec) {
			continue
		}
		c.Total++
		switch rec.Disposition {
		case DispIssued:
			c.Issued++
		case DispToRecreate:
			c.ToRecreate++
		case DispNotNeeded:
			c.NotNeeded++
		case DispDeferred:
			c.Deferred++
		default:
			c.Pending++
		}
	}
	return c
}

// Pending lists the scopes still undecided, for the cutover gate's message.
func (l *TokenLedger) Pending() []TokenRecord {
	l.mu.RLock()
	defer l.mu.RUnlock()

	var out []TokenRecord
	for _, key := range l.order {
		if rec, ok := l.records[key]; ok && rec.Disposition == DispPending && l.visibleLocked(rec) {
			out = append(out, *rec)
		}
	}
	return out
}

// MarkIssued records a successful creation. The value is not a parameter here,
// and that is deliberate: there is no path by which it could reach the ledger.
func (l *TokenLedger) MarkIssued(key, targetSlug string, issuedExpiry *time.Time, shortened bool, at time.Time) error {
	return l.update(key, func(rec *TokenRecord) {
		rec.Disposition = DispIssued
		rec.FailedAt = nil
		rec.TargetSlug = targetSlug
		rec.IssuedAt = &at
		rec.IssuedExpiresAt = issuedExpiry
		rec.ExpiryShortened = shortened
		rec.PreexistingOnTarget = false
		if shortened {
			rec.Note = "expiry shortened to the one-year maximum"
		} else {
			rec.Note = ""
		}
	})
}

// MarkSkipped records a decision to handle the token outside the migrator.
func (l *TokenLedger) MarkSkipped(key string, notNeeded bool) error {
	return l.update(key, func(rec *TokenRecord) {
		if notNeeded {
			rec.Disposition = DispNotNeeded
		} else {
			rec.Disposition = DispToRecreate
		}
	})
}

// MarkFailed leaves the row pending — a failure is not a decision — but records
// why, so the reason survives a reload.
func (l *TokenLedger) MarkFailed(key, reason string) error {
	now := time.Now().UTC()
	return l.update(key, func(rec *TokenRecord) {
		rec.Disposition = DispPending
		rec.Note = reason
		rec.FailedAt = &now
	})
}

// MarkDeferred sets a token aside until the next run.
func (l *TokenLedger) MarkDeferred(key string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	rec, ok := l.records[key]
	if !ok {
		return fmt.Errorf("no such token: %s", key)
	}
	if rec.Disposition == DispIssued {
		return fmt.Errorf("this token has already been issued on the target")
	}
	rec.Disposition = DispDeferred
	return l.saveLocked()
}

// ResurfaceDeferred returns every deferred token to pending, for a new run to
// offer again. Returns how many.
func (l *TokenLedger) ResurfaceDeferred() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, rec := range l.records {
		if rec.Disposition == DispDeferred {
			rec.Disposition = DispPending
			n++
		}
	}
	if n > 0 {
		_ = l.saveLocked()
	}
	return n
}

// ClearStaleFailures drops failure notes from before a record's project, or
// service account, was last migrated. The failure described a target that
// has since changed — typically "this token does not have access to the
// project", from an attempt made before the project existed there — and left
// on the row it reads as a failure of the token as it stands now. A failure
// with no recorded time predates this and is treated as stale too.
//
// migratedAt returns when the record's project or account was last migrated,
// or the zero time if it has not been.
func (l *TokenLedger) ClearStaleFailures(migratedAt func(TokenRecord) time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	changed := false
	for _, rec := range l.records {
		if rec.Disposition != DispPending || rec.Note == "" {
			continue
		}
		at := migratedAt(*rec)
		if at.IsZero() {
			continue
		}
		if rec.FailedAt == nil || rec.FailedAt.Before(at) {
			rec.Note = ""
			rec.FailedAt = nil
			changed = true
		}
	}
	if changed {
		_ = l.saveLocked()
	}
}

// Undo returns a row to undecided. Only a skip can be undone: an issued token
// exists on the target, and pretending otherwise would invite a second one.
func (l *TokenLedger) Undo(key string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	rec, ok := l.records[key]
	if !ok {
		return fmt.Errorf("no such token: %s", key)
	}
	if rec.Disposition == DispIssued {
		return fmt.Errorf("this token has already been issued on the target — reroll it instead of undoing it")
	}
	rec.Disposition = DispPending
	rec.Note = ""
	rec.FailedAt = nil
	return l.saveLocked()
}

// SkipAllPending closes every undecided row onto the worklist, and returns how
// many it moved.
func (l *TokenLedger) SkipAllPending() int {
	l.mu.Lock()
	defer l.mu.Unlock()

	moved := 0
	for _, rec := range l.records {
		if rec.Disposition == DispPending && l.visibleLocked(rec) {
			rec.Disposition = DispToRecreate
			moved++
		}
	}
	if moved > 0 {
		_ = l.saveLocked()
	}
	return moved
}

// MarkPreexisting flags rows whose name already exists on the target.
func (l *TokenLedger) MarkPreexisting(keys []string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	for _, key := range keys {
		if rec, ok := l.records[key]; ok && rec.Disposition == DispPending {
			rec.PreexistingOnTarget = true
		}
	}
	_ = l.saveLocked()
}

func (l *TokenLedger) update(key string, fn func(*TokenRecord)) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	rec, ok := l.records[key]
	if !ok {
		return fmt.Errorf("no such token: %s", key)
	}
	fn(rec)
	return l.saveLocked()
}

// CSV renders the worklist: what someone needs to rebuild these by hand.
//
// Rows marked not-needed are excluded — the file is a list of work, and a token
// deliberately dropped is not work. Issued rows stay in, so the same file also
// serves as the record of what the migrator did.
func (l *TokenLedger) CSV() string {
	l.mu.RLock()
	defer l.mu.RUnlock()

	var sb strings.Builder
	w := csv.NewWriter(&sb)
	_ = w.Write([]string{
		"kind", "project", "config", "environment", "service_account", "service_account_name",
		"token_name", "access", "source_expires_at", "disposition", "issued_at", "note",
	})

	keys := append([]string(nil), l.order...)
	sort.SliceStable(keys, func(i, j int) bool {
		a, b := l.records[keys[i]], l.records[keys[j]]
		if a == nil || b == nil {
			return false
		}
		// Work first, then the record of what was done.
		if (a.Disposition == DispIssued) != (b.Disposition == DispIssued) {
			return b.Disposition == DispIssued
		}
		return a.Scope()+a.Name < b.Scope()+b.Name
	})

	for _, key := range keys {
		rec, ok := l.records[key]
		if !ok || rec.Disposition == DispNotNeeded || !l.visibleLocked(rec) {
			continue
		}
		_ = w.Write([]string{
			string(rec.Kind),
			rec.Project,
			rec.Config,
			rec.Environment,
			rec.ServiceAccount,
			rec.ServiceAccountName,
			rec.Name,
			rec.Access,
			formatTokenTime(rec.SourceExpiresAt),
			string(rec.Disposition),
			formatTokenTime(rec.IssuedAt),
			rec.Note,
		})
	}

	w.Flush()
	return sb.String()
}

// Load reads a ledger previously written to disk. A missing file is not an
// error: it just means nothing has been decided yet.
func (l *TokenLedger) Load() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	data, err := os.ReadFile(l.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read token ledger: %w", err)
	}

	var stored struct {
		Records []TokenRecord `json:"records"`
	}
	if err := json.Unmarshal(data, &stored); err != nil {
		return fmt.Errorf("parse token ledger: %w", err)
	}

	l.records = make(map[string]*TokenRecord, len(stored.Records))
	l.order = l.order[:0]
	for i := range stored.Records {
		rec := stored.Records[i]
		l.records[rec.Key] = &rec
		l.order = append(l.order, rec.Key)
	}
	return nil
}

// Save writes the ledger to disk.
func (l *TokenLedger) Save() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.saveLocked()
}

func (l *TokenLedger) saveLocked() error {
	if l.filePath == "" {
		return nil
	}

	out := struct {
		Records []TokenRecord `json:"records"`
	}{Records: make([]TokenRecord, 0, len(l.order))}
	for _, key := range l.order {
		if rec, ok := l.records[key]; ok {
			out.Records = append(out.Records, *rec)
		}
	}

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal token ledger: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(l.filePath), 0o700); err != nil {
		return fmt.Errorf("create token ledger dir: %w", err)
	}
	// 0600 to match the checkpoint: no values, but token names and config paths
	// describe the shape of a workplace.
	if err := os.WriteFile(l.filePath, data, 0o600); err != nil {
		return fmt.Errorf("write token ledger: %w", err)
	}
	return nil
}

func parseTokenTime(s *string) *time.Time {
	if s == nil || *s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, *s)
	if err != nil {
		return nil
	}
	u := t.UTC()
	return &u
}

func formatTokenTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// tokenAccessForCreate maps a listed access string onto what the create endpoint
// accepts. The two vocabularies match, so this only guards against an unexpected
// value reaching the API as-is.
func tokenAccessForCreate(access string) string {
	if strings.Contains(access, "write") {
		return "read/write"
	}
	return "read"
}
