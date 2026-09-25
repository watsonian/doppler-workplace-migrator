package migration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/doppleruniversity/workplace-migrator/internal/doppler"
)

// trustedIPTarget records the order of trusted-IP operations, which is the part
// of a cutover that has to be right.
type trustedIPTarget struct {
	*mockReadWriteClient
	ops     []string
	failOn  map[string]error
	current map[string]map[string]struct{}
}

func newTrustedIPTarget() *trustedIPTarget {
	return &trustedIPTarget{
		mockReadWriteClient: &mockReadWriteClient{},
		failOn:              map[string]error{},
		current:             map[string]map[string]struct{}{},
	}
}

func (t *trustedIPTarget) AddTrustedIP(_ context.Context, project, config, ip string) error {
	key := "add " + project + "/" + config + " " + ip
	t.ops = append(t.ops, key)
	if err, ok := t.failOn[key]; ok {
		return err
	}
	scope := project + "/" + config
	if t.current[scope] == nil {
		t.current[scope] = map[string]struct{}{}
	}
	t.current[scope][ip] = struct{}{}
	return nil
}

func (t *trustedIPTarget) RemoveTrustedIP(_ context.Context, project, config, ip string) error {
	key := "remove " + project + "/" + config + " " + ip
	t.ops = append(t.ops, key)
	if err, ok := t.failOn[key]; ok {
		return err
	}
	delete(t.current[project+"/"+config], ip)
	return nil
}

func planFor(ips ...string) *CutoverPlan {
	return &CutoverPlan{
		TrustedIPs: []TrustedIPWork{{
			Project: "payments", Config: "prd", Scope: "payments/prd",
			Add: []string{"10.0.0.0/8"}, Remove: ips, ClosesConfig: true,
		}},
	}
}

func TestCutoverAddsBeforeRemoving(t *testing.T) {
	// A config left with neither its old ranges nor its new ones is closed to
	// everyone, including whoever has to fix it.
	target := newTrustedIPTarget()
	plan := planFor("0.0.0.0/0")

	if _, err := NewCutover(target, plan, nil, nil).Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	addIdx, removeIdx := -1, -1
	for i, op := range target.ops {
		if strings.HasPrefix(op, "add payments/prd 10.") && addIdx < 0 {
			addIdx = i
		}
		if strings.HasPrefix(op, "remove payments/prd 0.0.0.0/0") {
			removeIdx = i
		}
	}
	if addIdx < 0 || removeIdx < 0 {
		t.Fatalf("expected both operations, got %v", target.ops)
	}
	if addIdx > removeIdx {
		t.Errorf("removed the default before adding the replacement: %v", target.ops)
	}
}

func TestCutoverSelfTrustGoesOnBeforeTheDefaultComesOff(t *testing.T) {
	// There must be no instant where the migrator has no way back into a config
	// it is in the middle of restricting.
	target := newTrustedIPTarget()
	plan := planFor("0.0.0.0/0")
	plan.MigratorCIDR = "203.0.113.47/32"

	if _, err := NewCutover(target, plan, nil, nil).Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	selfIdx, removeIdx := -1, -1
	for i, op := range target.ops {
		if op == "add payments/prd 203.0.113.47/32" {
			selfIdx = i
		}
		if op == "remove payments/prd 0.0.0.0/0" {
			removeIdx = i
		}
	}
	if selfIdx < 0 {
		t.Fatalf("the migrator's address was never added: %v", target.ops)
	}
	if selfIdx > removeIdx {
		t.Errorf("the default was removed before self-trust was in place: %v", target.ops)
	}
}

func TestCutoverRemovesItsOwnAddressLast(t *testing.T) {
	target := newTrustedIPTarget()
	plan := planFor("0.0.0.0/0")
	plan.MigratorCIDR = "203.0.113.47/32"

	result, err := NewCutover(target, plan, nil, nil).Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	last := target.ops[len(target.ops)-1]
	if last != "remove payments/prd 203.0.113.47/32" {
		t.Errorf("last operation = %q, want the self-trust removal", last)
	}
	if _, still := target.current["payments/prd"]["203.0.113.47/32"]; still {
		t.Error("the migrator's address is still trusted")
	}
	if !result.Complete() {
		t.Errorf("a cutover that closed its own hole should report complete: %+v", result)
	}
	if len(result.SelfTrustOutstanding) != 0 {
		t.Errorf("SelfTrustOutstanding = %v", result.SelfTrustOutstanding)
	}
}

func TestCutoverIsNotCompleteWhileItsAddressRemains(t *testing.T) {
	// Trusted IPs are bare CIDR strings with nowhere to put a label, so one left
	// behind is indistinguishable from one somebody added deliberately. A
	// cutover that cannot remove it has not finished, however well the rest went.
	target := newTrustedIPTarget()
	target.failOn["remove payments/prd 203.0.113.47/32"] = &doppler.APIError{StatusCode: 500}

	plan := planFor("0.0.0.0/0")
	plan.MigratorCIDR = "203.0.113.47/32"

	result, err := NewCutover(target, plan, nil, nil).Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Complete() {
		t.Error("a cutover leaving its own address behind must not report complete")
	}
	if len(result.SelfTrustOutstanding) != 1 || result.SelfTrustOutstanding[0] != "payments/prd" {
		t.Errorf("SelfTrustOutstanding = %v", result.SelfTrustOutstanding)
	}
	if result.MigratorCIDR != "203.0.113.47/32" {
		t.Error("the address has to be reported so it can be removed by hand")
	}
}

func TestCutoverWithoutSelfTrustJustMirrors(t *testing.T) {
	target := newTrustedIPTarget()
	plan := planFor("0.0.0.0/0", "192.168.0.0/16")

	result, err := NewCutover(target, plan, nil, nil).Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, op := range target.ops {
		if strings.Contains(op, "203.0.113") {
			t.Errorf("no address was supplied, so none should be added: %v", target.ops)
		}
	}
	// Both the default and the stray range go: a mirror, not a merge.
	if result.RangesRemoved != 2 {
		t.Errorf("RangesRemoved = %d, want 2", result.RangesRemoved)
	}
	if !result.Complete() {
		t.Error("expected a clean mirror to report complete")
	}
}

func TestPlanCutoverSkipsConfigsThatAlreadyMatch(t *testing.T) {
	// Most workplaces reach cutover with nothing to do here, and the count of
	// configs needing nothing is what says so.
	data := &MigrationData{Projects: []ProjectData{{
		Project: doppler.Project{Slug: "payments"},
		Configs: []ConfigData{
			{Config: doppler.Config{Name: "prd"}, TrustedIPs: []doppler.TrustedIP{{IP: "10.0.0.0/8"}}},
			{Config: doppler.Config{Name: "stg"}, TrustedIPs: []doppler.TrustedIP{{IP: "0.0.0.0/0"}}},
		},
	}}}
	snap := &TargetSnapshot{Projects: map[string]ProjectSnapshot{"payments": {
		Configs: map[string]ConfigSnapshot{
			"prd": {TrustedIPs: doppler.TrustedIPSet{"0.0.0.0/0": {}}},
			"stg": {TrustedIPs: doppler.TrustedIPSet{"0.0.0.0/0": {}}},
		},
	}}}

	plan := PlanCutover(data, snap, "")
	if len(plan.TrustedIPs) != 1 || plan.TrustedIPs[0].Scope != "payments/prd" {
		t.Fatalf("TrustedIPs = %+v, want only the restricted config", plan.TrustedIPs)
	}
	// stg is at its default on both sides — nothing to mirror.
	if plan.AlreadyMatching != 1 {
		t.Errorf("AlreadyMatching = %d, want 1", plan.AlreadyMatching)
	}
	if plan.ClosingCount != 1 || !plan.NeedsSelfTrust() {
		t.Error("restricting a config should be reported as closing it")
	}
}

func TestPlanCutoverNeedsNoSelfTrustWhenNothingCloses(t *testing.T) {
	// Adding ranges beside the default locks nobody out, so no address is needed.
	data := &MigrationData{Projects: []ProjectData{{
		Project: doppler.Project{Slug: "payments"},
		Configs: []ConfigData{{
			Config:     doppler.Config{Name: "prd"},
			TrustedIPs: []doppler.TrustedIP{{IP: "0.0.0.0/0"}, {IP: "10.0.0.0/8"}},
		}},
	}}}
	snap := &TargetSnapshot{Projects: map[string]ProjectSnapshot{"payments": {
		Configs: map[string]ConfigSnapshot{"prd": {TrustedIPs: doppler.TrustedIPSet{"0.0.0.0/0": {}}}},
	}}}

	plan := PlanCutover(data, snap, "")
	if plan.NeedsSelfTrust() {
		t.Error("a config keeping the default cannot lock anyone out")
	}
}

func TestAnEmptyCutoverPlanEncodesItsListsAsArrays(t *testing.T) {
	// The page reads .length on each list. As null, it crashed to a blank
	// screen for any workplace with nothing to mirror.
	plan := PlanCutover(&MigrationData{}, &TargetSnapshot{Projects: map[string]ProjectSnapshot{}}, "")
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"trusted_ips":[]`, `"matching":[]`, `"unreadable":[]`} {
		if !strings.Contains(string(raw), field) {
			t.Errorf("want %s in %s", field, raw)
		}
	}
}

func TestCutoverLeavesAloneConfigsItCouldNotRead(t *testing.T) {
	// An unread list is not an empty one. Planned as empty, it would offer to
	// add every source range, including ones the target already has.
	data := &MigrationData{Projects: []ProjectData{{
		Project: doppler.Project{Slug: "api"},
		Configs: []ConfigData{{Config: doppler.Config{Name: "prd"}, TrustedIPs: []doppler.TrustedIP{{IP: "10.0.0.0/8"}}}},
	}}}
	snap := &TargetSnapshot{Projects: map[string]ProjectSnapshot{
		"api": {Configs: map[string]ConfigSnapshot{"prd": {TrustedIPsUnread: true}}},
	}}

	plan := PlanCutover(data, snap, "")
	if len(plan.TrustedIPs) != 0 || len(plan.Unreadable) != 1 || plan.Unreadable[0] != "api/prd" {
		t.Fatalf("got %+v", plan)
	}
}
