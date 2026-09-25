package migration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/doppler/onprem-migrator/internal/doppler"
)

// TargetSnapshot is what the target holds right now.
//
// Deliberately not a MigrationData. That type means "the source", and a second
// one of it would make every function that takes one ambiguous about which
// instance it is being handed. This is also a much thinner thing: a diff needs
// to know which secrets exist and whether each one matches, not what any of
// them are.
type TargetSnapshot struct {
	ReadAt time.Time

	WorkplaceRoles  map[string]doppler.Role // by identifier
	ProjectRoles    map[string]doppler.Role // by identifier
	Groups          map[string]GroupSnapshot
	ServiceAccounts map[string]doppler.ServiceAccount
	Users           map[string]doppler.WorkplaceUser // by lowercased email

	Projects map[string]ProjectSnapshot

	// Listed says Projects, and each project's Configs, were read fresh from
	// the target just now — so a project or config missing from them is not on
	// the target, rather than merely not read. RefreshTrustedIPs sets it.
	Listed bool
}

// GroupSnapshot is a group and who is in it.
type GroupSnapshot struct {
	Group   doppler.Group
	Members []doppler.GroupMember
}

// ProjectSnapshot is one project on the target.
type ProjectSnapshot struct {
	Project      doppler.Project
	Environments map[string]doppler.Environment // by slug
	Configs      map[string]ConfigSnapshot      // by config name
	Webhooks     map[string]doppler.Webhook     // by URL
	Members      map[string]doppler.ProjectMember
}

// ConfigSnapshot is one config on the target.
type ConfigSnapshot struct {
	Config     doppler.Config
	Secrets    map[string]SecretFingerprint
	TrustedIPs doppler.TrustedIPSet
	// TrustedIPsUnread is set when the ranges could not be read, so an empty
	// TrustedIPs is not taken to mean "none".
	TrustedIPsUnread bool
}

// SecretFingerprint is everything the diff needs about one secret on the target,
// and nothing else.
//
// The value is reduced to a digest the moment it is read and the original is
// dropped. That is not a stored record — it never leaves memory and never
// survives the run — it just means a second copy of the workplace's secrets is
// not sitting in the process for the length of a comparison. Equality is all a
// diff needs, and a digest answers that.
type SecretFingerprint struct {
	Digest     string
	Visibility string
	ValueType  string
	Note       string
}

// fingerprint reduces a secret to what a comparison needs.
func fingerprint(s doppler.Secret) SecretFingerprint {
	sum := sha256.Sum256([]byte(s.Raw))
	fp := SecretFingerprint{
		Digest:     hex.EncodeToString(sum[:]),
		Visibility: s.RawVisibility,
		Note:       s.Note,
	}
	if s.RawValueType != nil {
		fp.ValueType = s.RawValueType.Type
	}
	return fp
}

// FingerprintOf reduces a source secret the same way, so the two sides are
// compared on identical terms.
func FingerprintOf(s doppler.Secret) SecretFingerprint { return fingerprint(s) }

// Matches reports whether two fingerprints describe the same secret.
func (f SecretFingerprint) Matches(other SecretFingerprint) bool {
	return f.Digest == other.Digest &&
		f.Visibility == other.Visibility &&
		f.ValueType == other.ValueType &&
		f.Note == other.Note
}

// FieldChanges says what differs, one field per change, reading f as the
// source and other as the target: Was is the target's setting, Now what the
// run will make it.
//
// Visibility, type and note are metadata the API returns beside the value,
// and are shown. The value never is: its change carries no Was or Now, so
// nothing on the plan screen, in a screenshot or in the export can hold one.
func (f SecretFingerprint) FieldChanges(other SecretFingerprint) []FieldChange {
	var out []FieldChange
	if f.Digest != other.Digest {
		out = append(out, FieldChange{Field: "value"})
	}
	if f.Visibility != other.Visibility {
		out = append(out, FieldChange{Field: "visibility", Was: orUnset(other.Visibility), Now: orUnset(f.Visibility)})
	}
	if f.ValueType != other.ValueType {
		out = append(out, FieldChange{Field: "type", Was: orDefaultType(other.ValueType), Now: orDefaultType(f.ValueType)})
	}
	if f.Note != other.Note {
		out = append(out, FieldChange{Field: "note", Was: orNone(other.Note), Now: orNone(f.Note)})
	}
	return out
}

func orUnset(v string) string {
	if v == "" {
		return "unset"
	}
	return v
}

// orDefaultType names the type a secret has when none is recorded.
func orDefaultType(v string) string {
	if v == "" {
		return "string"
	}
	return v
}

func orNone(v string) string {
	if v == "" {
		return "none"
	}
	return v
}

// SnapshotReader reads the target.
type SnapshotReader struct {
	target     DopplerClient
	onProgress func(GatherProgress)
}

// NewSnapshotReader creates a reader over a target client.
func NewSnapshotReader(target DopplerClient, onProgress func(GatherProgress)) *SnapshotReader {
	if onProgress == nil {
		onProgress = func(GatherProgress) {}
	}
	return &SnapshotReader{target: target, onProgress: onProgress}
}

// Read builds a snapshot of the target.
//
// A target that is empty — the first run — reads as an empty snapshot rather
// than an error, so the diff against it is "everything is new" and the same
// code path handles a first migration and a top-up.
func (r *SnapshotReader) Read(ctx context.Context) (*TargetSnapshot, error) {
	snap := &TargetSnapshot{
		ReadAt:          time.Now().UTC(),
		WorkplaceRoles:  map[string]doppler.Role{},
		ProjectRoles:    map[string]doppler.Role{},
		Groups:          map[string]GroupSnapshot{},
		ServiceAccounts: map[string]doppler.ServiceAccount{},
		Users:           map[string]doppler.WorkplaceUser{},
		Projects:        map[string]ProjectSnapshot{},
	}

	r.progress("workplace_roles", 0, 0, "Reading target roles")
	roles, err := r.target.ListWorkplaceRoles(ctx)
	if err != nil {
		return nil, fmt.Errorf("read target workplace roles: %w", err)
	}
	for _, role := range roles {
		snap.WorkplaceRoles[role.Identifier] = role
	}

	projectRoles, err := r.target.ListProjectRoles(ctx)
	if err != nil {
		return nil, fmt.Errorf("read target project roles: %w", err)
	}
	for _, role := range projectRoles {
		snap.ProjectRoles[role.Identifier] = role
	}

	r.progress("groups", 0, 0, "Reading target groups")
	groups, err := r.target.ListGroups(ctx)
	if err != nil {
		return nil, fmt.Errorf("read target groups: %w", err)
	}
	for _, group := range groups {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		members, err := r.target.GetGroupMembers(ctx, group.Slug)
		if err != nil {
			// A group whose membership will not read is still a group that
			// exists; recording it without members is better than failing the
			// whole comparison over one of them.
			log.Printf("[warn] read target members of group %s: %v", group.Slug, err)
		}
		snap.Groups[group.Slug] = GroupSnapshot{Group: group, Members: members}
	}

	r.progress("service_accounts", 0, 0, "Reading target service accounts")
	accounts, err := r.target.ListServiceAccounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("read target service accounts: %w", err)
	}
	for _, account := range accounts {
		snap.ServiceAccounts[account.Slug] = account
	}

	r.progress("users", 0, 0, "Reading target users")
	users, err := r.target.ListWorkplaceUsers(ctx)
	if err != nil {
		return nil, fmt.Errorf("read target users: %w", err)
	}
	for _, user := range users {
		if email := strings.ToLower(strings.TrimSpace(user.User.Email)); email != "" {
			snap.Users[email] = user
		}
	}

	r.progress("projects", 0, 0, "Reading target projects")
	projects, err := r.target.ListProjects(ctx)
	if err != nil {
		return nil, fmt.Errorf("read target projects: %w", err)
	}

	for i, project := range projects {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		r.progress("projects", i+1, len(projects), "Reading "+project.Slug)

		ps, err := r.readProject(ctx, project)
		if err != nil {
			return nil, err
		}
		snap.Projects[project.Slug] = *ps
	}

	return snap, nil
}

func (r *SnapshotReader) readProject(ctx context.Context, project doppler.Project) (*ProjectSnapshot, error) {
	ps := &ProjectSnapshot{
		Project:      project,
		Environments: map[string]doppler.Environment{},
		Configs:      map[string]ConfigSnapshot{},
		Webhooks:     map[string]doppler.Webhook{},
		Members:      map[string]doppler.ProjectMember{},
	}

	environments, err := r.target.ListEnvironments(ctx, project.Slug)
	if err != nil {
		return nil, fmt.Errorf("read target environments for %s: %w", project.Slug, err)
	}
	for _, env := range environments {
		ps.Environments[env.Slug] = env
	}

	configs, err := r.target.ListConfigs(ctx, project.Slug)
	if err != nil {
		return nil, fmt.Errorf("read target configs for %s: %w", project.Slug, err)
	}

	for _, cfg := range configs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// Personal configs belong to individual users and are never migrated, so
		// comparing against them would invent differences nobody can act on.
		if doppler.IsPersonalConfig(cfg.Name) {
			continue
		}

		cs := ConfigSnapshot{Config: cfg, Secrets: map[string]SecretFingerprint{}}

		secrets, err := r.target.ListSecrets(ctx, project.Slug, cfg.Name)
		if err != nil {
			// A config that will not read is reported as a difference rather
			// than silently treated as empty, which would read as "every secret
			// is missing" and propose rewriting all of them.
			log.Printf("[warn] read target secrets for %s/%s: %v", project.Slug, cfg.Name, err)
			cs.Secrets = nil
		} else {
			for name, secret := range secrets {
				if strings.HasPrefix(name, "DOPPLER_") {
					continue
				}
				cs.Secrets[name] = fingerprint(secret)
			}
		}

		ips, err := r.target.ListTrustedIPs(ctx, project.Slug, cfg.Name)
		if err != nil {
			log.Printf("[warn] read target trusted IPs for %s/%s: %v", project.Slug, cfg.Name, err)
			cs.TrustedIPsUnread = true
		} else {
			cs.TrustedIPs = doppler.NewTrustedIPSet(ips)
		}

		ps.Configs[cfg.Name] = cs
	}

	webhooks, err := r.target.ListWebhooks(ctx, project.Slug)
	if err != nil {
		log.Printf("[warn] read target webhooks for %s: %v", project.Slug, err)
	}
	for _, wh := range webhooks {
		ps.Webhooks[wh.URL] = wh
	}

	members, err := r.target.ListProjectMembers(ctx, project.Slug)
	if err != nil {
		log.Printf("[warn] read target members for %s: %v", project.Slug, err)
	}
	for _, m := range members {
		ps.Members[m.Type+":"+m.Slug] = m
	}

	return ps, nil
}

func (r *SnapshotReader) progress(resource string, current, total int, message string) {
	r.onProgress(GatherProgress{
		Resource: resource,
		Current:  current,
		Total:    total,
		Message:  message,
	})
}

// IsEmpty reports whether the target has nothing on it, which is what a first
// migration compares against.
func (s *TargetSnapshot) IsEmpty() bool {
	return s == nil || (len(s.Projects) == 0 && len(s.Groups) == 0 && len(s.WorkplaceRoles) == 0)
}

// RefreshTrustedIPs returns a copy of snap with every source config's trusted
// ranges read again from the target.
//
// Cutover runs after the migration, but the snapshot it would otherwise plan
// from was taken at Compare, before the migration created anything. A project
// created since reads as having no ranges at all — not even the allow-all
// default the target gives every new config — and the plan offers to add
// ranges that are already there.
func RefreshTrustedIPs(ctx context.Context, target DopplerClient, snap *TargetSnapshot, data *MigrationData) *TargetSnapshot {
	out := &TargetSnapshot{}
	if snap != nil {
		*out = *snap
	}
	out.Projects = map[string]ProjectSnapshot{}
	if data == nil {
		return out
	}

	// Only what is on the target now. A project a partial run has not
	// migrated is not there, and asking for its ranges gets a refusal —
	// "this token does not have access to requested project", when the token
	// can reach one project only — that reads as a permission problem.
	present, err := target.ListProjects(ctx)
	if err != nil {
		log.Printf("[warn] list target projects: %v", err)
		return out
	}
	onTarget := make(map[string]bool, len(present))
	for _, p := range present {
		onTarget[p.Slug] = true
	}

	type job struct{ project, config string }
	var jobs []job
	for _, pd := range data.Projects {
		if !onTarget[pd.Project.Slug] {
			continue
		}
		configs, err := target.ListConfigs(ctx, pd.Project.Slug)
		if err != nil {
			log.Printf("[warn] list target configs for %s: %v", pd.Project.Slug, err)
			continue
		}
		ps := ProjectSnapshot{Project: doppler.Project{Slug: pd.Project.Slug}, Configs: map[string]ConfigSnapshot{}}
		have := make(map[string]bool, len(configs))
		for _, c := range configs {
			have[c.Name] = true
		}
		for _, cd := range pd.Configs {
			if have[cd.Config.Name] {
				ps.Configs[cd.Config.Name] = ConfigSnapshot{Config: doppler.Config{Name: cd.Config.Name}}
				jobs = append(jobs, job{pd.Project.Slug, cd.Config.Name})
			}
		}
		out.Projects[pd.Project.Slug] = ps
	}

	var mu sync.Mutex
	_ = runPool(ctx, len(jobs), gatherConcurrency, func(ctx context.Context, i int) error {
		j := jobs[i]
		ips, err := target.ListTrustedIPs(ctx, j.project, j.config)

		mu.Lock()
		defer mu.Unlock()
		ps := out.Projects[j.project]
		cs := ps.Configs[j.config]
		if err != nil {
			log.Printf("[warn] read target trusted IPs for %s/%s: %v", j.project, j.config, err)
			cs.TrustedIPs = nil
			cs.TrustedIPsUnread = true
		} else {
			cs.TrustedIPs = doppler.NewTrustedIPSet(ips)
		}
		ps.Configs[j.config] = cs
		return nil // one unreadable config does not stop the others
	})
	out.Listed = true
	return out
}
