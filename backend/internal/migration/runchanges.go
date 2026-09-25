package migration

import (
	"sort"
	"strings"
)

// RunChanges is what one run changed, grouped by resource type, for the
// waiting page's expandable Runs table and the final report.
//
// Names and counts only. A secret appears by name within its config, never
// with a value. Items are capped per group so a first migration of thousands
// of configs stays a small file; the counts stay exact.
type RunChanges struct {
	Groups []ChangeGroup `json:"groups"`
}

// ChangeGroup is one resource type's share of a run.
type ChangeGroup struct {
	Label    string       `json:"label"`
	Created  int          `json:"created"`
	Updated  int          `json:"updated"`
	Deleted  int          `json:"deleted"`
	Migrated int          `json:"migrated"`
	Failed   int          `json:"failed"`
	Items    []ChangeItem `json:"items"`
	// More counts the items past the cap.
	More int `json:"more,omitempty"`
}

// ChangeItem is one thing a run wrote, or failed to.
type ChangeItem struct {
	Action string `json:"action"` // created, updated, deleted, migrated, failed
	Item   string `json:"item"`
}

// changeItemCap is how many items a group keeps.
const changeItemCap = 100

// Add records one change.
func (rc *RunChanges) Add(label, action, item string) {
	g := rc.group(label)
	switch action {
	case "created":
		g.Created++
	case "updated":
		g.Updated++
	case "deleted":
		g.Deleted++
	case "migrated":
		g.Migrated++
	case "failed":
		g.Failed++
	}
	if len(g.Items) < changeItemCap {
		g.Items = append(g.Items, ChangeItem{Action: action, Item: item})
	} else {
		g.More++
	}
}

// Merge folds another run stage's changes in.
func (rc *RunChanges) Merge(other *RunChanges) {
	if other == nil {
		return
	}
	for _, og := range other.Groups {
		g := rc.group(og.Label)
		g.Created += og.Created
		g.Updated += og.Updated
		g.Deleted += og.Deleted
		g.Migrated += og.Migrated
		g.Failed += og.Failed
		for _, it := range og.Items {
			if len(g.Items) < changeItemCap {
				g.Items = append(g.Items, it)
			} else {
				g.More++
			}
		}
		g.More += og.More
	}
}

// Empty reports whether nothing was recorded.
func (rc *RunChanges) Empty() bool { return rc == nil || len(rc.Groups) == 0 }

func (rc *RunChanges) group(label string) *ChangeGroup {
	for i := range rc.Groups {
		if rc.Groups[i].Label == label {
			return &rc.Groups[i]
		}
	}
	rc.Groups = append(rc.Groups, ChangeGroup{Label: label, Items: []ChangeItem{}})
	return &rc.Groups[len(rc.Groups)-1]
}

// ReappliedEveryRun are the checkpoint items the migration engine writes on
// every run whether or not anything changed: the workplace's settings, each
// user's access level, and group member adds, which succeed on a member
// already present. A top-up leaves them out of what it reports.
var ReappliedEveryRun = []string{"workplace_settings", "user_access", "group_member"}

// Totals adds up what a run wrote and what failed. Migrated, created and
// updated all count as written.
func (rc *RunChanges) Totals() (written, failed int) {
	if rc == nil {
		return 0, 0
	}
	for _, g := range rc.Groups {
		written += g.Migrated + g.Created + g.Updated
		failed += g.Failed
	}
	return written, failed
}

// convergeResourceLabels names the converger's resources in the Runs table.
var convergeResourceLabels = map[string]string{
	"roles":            "Roles",
	"groups":           "Groups",
	"service_accounts": "Service accounts",
	"environments":     "Environments",
	"configs":          "Configs",
	"secrets":          "Secrets",
	"webhooks":         "Webhooks",
	"members":          "Project members",
}

// checkpointGroups are the executor's item-key prefixes as the Runs table
// names them, in the order the migration runs them. Keys not listed here are
// bookkeeping (environment ordering) and are not shown.
var checkpointGroups = []struct{ prefix, label string }{
	{"workplace_settings", "Workplace settings"},
	{"workplace_role", "Roles"},
	{"project_role", "Roles"},
	{"group", "Groups"},
	{"service_account", "Service accounts"},
	{"service_account_identity", "Service account identities"},
	{"project", "Projects"},
	{"env", "Environments"},
	{"config", "Configs"},
	{"secrets", "Secrets"},
	{"lock", "Config locks"},
	{"inheritable", "Config inheritance"},
	{"inherits", "Config inheritance"},
	{"webhook", "Webhooks"},
	{"group_member", "Group members"},
	{"project_member", "Project members"},
	{"user_access", "User access"},
	{"change_request_policy", "Change request policies"},
	{"trusted_ips", "Trusted IPs"},
}

// ChangesFromCheckpoint reads what a migration run did from its checkpoint,
// naming each item as a person knows it. Group and service account slugs are
// UUIDs, and member slugs are user IDs, so data turns them back into names
// and emails; a key it cannot resolve is shown as it is.
//
// Items whose key starts with one of the excluded prefixes are left out.
func ChangesFromCheckpoint(cp Checkpoint, data *MigrationData, exclude ...string) *RunChanges {
	skip := map[string]bool{}
	for _, prefix := range exclude {
		skip[prefix] = true
	}
	names := checkpointNames(data)
	label := map[string]string{}
	order := map[string]int{}
	for i, g := range checkpointGroups {
		label[g.prefix] = g.label
		if _, seen := order[g.label]; !seen {
			order[g.label] = i
		}
	}

	type entry struct{ group, action, item string }
	var entries []entry
	for _, phase := range cp.Phases {
		for key, item := range phase.Items {
			prefix, rest, _ := strings.Cut(key, ":")
			group, ok := label[prefix]
			if !ok || skip[prefix] {
				continue
			}
			var action string
			switch item.Status {
			case StatusCompleted:
				action = "migrated"
			case StatusFailed:
				action = "failed"
			default:
				continue // skipped items did nothing, and in-progress ones did not finish
			}
			entries = append(entries, entry{group, action, names.describe(prefix, rest)})
		}
	}

	// Map order is random; the Runs table should read the same every time.
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].group != entries[j].group {
			return order[entries[i].group] < order[entries[j].group]
		}
		if entries[i].action != entries[j].action {
			return entries[i].action == "failed" // failures first
		}
		return entries[i].item < entries[j].item
	})

	rc := &RunChanges{Groups: []ChangeGroup{}}
	for _, e := range entries {
		rc.Add(e.group, e.action, e.item)
	}
	return rc
}

type itemNames struct {
	groups, accounts, users map[string]string
}

func checkpointNames(data *MigrationData) itemNames {
	n := itemNames{groups: map[string]string{}, accounts: map[string]string{}, users: map[string]string{}}
	if data == nil {
		return n
	}
	for _, g := range data.Groups {
		n.groups[g.Group.Slug] = g.Group.Name
	}
	for _, sa := range data.ServiceAccounts {
		n.accounts[sa.Slug] = sa.Name
	}
	for _, u := range data.WorkplaceUsers {
		if u.User.Email != "" {
			n.users[u.ID] = u.User.Email
		}
	}
	return n
}

// member names a member slug, whichever kind it is.
func (n itemNames) member(slug string) string {
	for _, m := range []map[string]string{n.users, n.groups, n.accounts} {
		if name, ok := m[slug]; ok {
			return name
		}
	}
	return slug
}

func (n itemNames) describe(prefix, rest string) string {
	switch prefix {
	case "workplace_settings":
		return "Workplace settings"
	case "group":
		if name, ok := n.groups[rest]; ok {
			return name
		}
	case "service_account":
		if name, ok := n.accounts[rest]; ok {
			return name
		}
	case "service_account_identity":
		account, identity, _ := strings.Cut(rest, "/")
		if name, ok := n.accounts[account]; ok {
			return name + " · " + identity
		}
	case "group_member":
		group, member, _ := strings.Cut(rest, "/")
		if name, ok := n.groups[group]; ok {
			group = name
		}
		return group + " · " + n.member(member)
	case "project_member":
		project, member, _ := strings.Cut(rest, "/")
		return project + " · " + n.member(member)
	}
	return rest
}
