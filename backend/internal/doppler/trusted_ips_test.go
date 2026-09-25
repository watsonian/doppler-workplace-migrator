package doppler

import (
	"reflect"
	"testing"
)

func set(ips ...string) TrustedIPSet {
	s := make(TrustedIPSet, len(ips))
	for _, ip := range ips {
		s[ip] = struct{}{}
	}
	return s
}

func TestPlanTrustedIPsMirrors(t *testing.T) {
	tests := []struct {
		name       string
		source     TrustedIPSet
		target     TrustedIPSet
		keep       []string
		wantAdd    []string
		wantRemove []string
	}{
		{
			name:       "the ordinary first run: restrict a default config",
			source:     set("10.0.0.0/8"),
			target:     set("0.0.0.0/0"),
			wantAdd:    []string{"10.0.0.0/8"},
			wantRemove: []string{"0.0.0.0/0"},
		},
		{
			// The case the old code got wrong: it removed only the allow-all
			// default, so a range the source never authorised survived on a
			// config reporting as migrated.
			name:       "a stray range on the target is removed too",
			source:     set("10.0.0.0/8"),
			target:     set("0.0.0.0/0", "192.168.0.0/16"),
			wantAdd:    []string{"10.0.0.0/8"},
			wantRemove: []string{"0.0.0.0/0", "192.168.0.0/16"},
		},
		{
			name:       "a range dropped from the source is dropped from the target",
			source:     set("10.0.0.0/8"),
			target:     set("10.0.0.0/8", "172.16.0.0/12"),
			wantRemove: []string{"172.16.0.0/12"},
		},
		{
			// Both sides untouched. A workplace where this holds everywhere has
			// no trusted-IP work at all and cannot lock the migrator out.
			name:   "both at the default is no work",
			source: set("0.0.0.0/0"),
			target: set("0.0.0.0/0"),
		},
		{
			name:   "already mirrored is no work",
			source: set("10.0.0.0/8", "172.16.0.0/12"),
			target: set("172.16.0.0/12", "10.0.0.0/8"),
		},
		{
			// The migrator's own address is not in the source, so a plain mirror
			// would strip it and lock the run out mid-cutover.
			name:       "the kept address survives the mirror",
			source:     set("10.0.0.0/8"),
			target:     set("0.0.0.0/0", "10.0.0.0/8", "203.0.113.47/32"),
			keep:       []string{"203.0.113.47/32"},
			wantRemove: []string{"0.0.0.0/0"},
		},
		{
			name:       "without the exemption that address is removed",
			source:     set("10.0.0.0/8"),
			target:     set("10.0.0.0/8", "203.0.113.47/32"),
			wantRemove: []string{"203.0.113.47/32"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plan := PlanTrustedIPs(tc.source, tc.target, tc.keep...)
			if !reflect.DeepEqual(plan.Add, tc.wantAdd) {
				t.Errorf("Add = %v, want %v", plan.Add, tc.wantAdd)
			}
			if !reflect.DeepEqual(plan.Remove, tc.wantRemove) {
				t.Errorf("Remove = %v, want %v", plan.Remove, tc.wantRemove)
			}
			if plan.Empty() != (tc.wantAdd == nil && tc.wantRemove == nil) {
				t.Errorf("Empty() = %t", plan.Empty())
			}
		})
	}
}

func TestRemovesAllowAllMarksTheLockout(t *testing.T) {
	// Adding ranges beside the default locks nobody out; taking the default away
	// is the moment access closes. That distinction decides whether a config
	// needs the migrator's address kept during cutover.
	restricting := PlanTrustedIPs(set("10.0.0.0/8"), set("0.0.0.0/0"))
	if !restricting.RemovesAllowAll() {
		t.Error("removing the default should be reported as closing the config")
	}

	widening := PlanTrustedIPs(set("0.0.0.0/0", "10.0.0.0/8"), set("0.0.0.0/0"))
	if widening.RemovesAllowAll() {
		t.Error("a config keeping the default is not closed by adding to it")
	}
}

func TestTrustedIPSetDefaults(t *testing.T) {
	if !set("0.0.0.0/0").IsDefault() {
		t.Error("allow-all alone is the default state")
	}
	if set("0.0.0.0/0", "10.0.0.0/8").IsDefault() {
		t.Error("allow-all plus a range is not the default state")
	}
	if set("10.0.0.0/8").IsDefault() {
		t.Error("a restricted config is not at its default")
	}
	if !set("0.0.0.0/0", "10.0.0.0/8").AllowsEverything() {
		t.Error("a set containing allow-all still allows everything")
	}
	if set("10.0.0.0/8").AllowsEverything() {
		t.Error("a set without allow-all does not")
	}
}
