package doppler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
)

// ListTrustedIPs returns all trusted IPs for a config.
func (c *Client) ListTrustedIPs(ctx context.Context, project, config string) ([]TrustedIP, error) {
	params := url.Values{
		"project": {project},
		"config":  {config},
	}
	items, err := c.ListAll(ctx, "/configs/config/trusted_ips", params, "ips", RateLimitRead)
	if err != nil {
		return nil, err
	}

	ips := make([]TrustedIP, 0, len(items))
	for _, item := range items {
		// The API returns ips as plain strings (e.g. "1.2.3.4/32"), not objects
		var ipStr string
		if err := json.Unmarshal(item, &ipStr); err != nil {
			return nil, fmt.Errorf("parsing trusted IP: %w", err)
		}
		ips = append(ips, TrustedIP{IP: ipStr})
	}

	return ips, nil
}

// AddTrustedIP adds a trusted IP to a config.
func (c *Client) AddTrustedIP(ctx context.Context, project, config, ip string) error {
	body := map[string]string{
		"project": project,
		"config":  config,
		"ip":      ip,
	}

	_, _, err := c.Post(ctx, "/configs/config/trusted_ips", body)
	return err
}

// RemoveTrustedIP removes a trusted IP or CIDR range from a config.
func (c *Client) RemoveTrustedIP(ctx context.Context, project, config, ip string) error {
	body := map[string]string{
		"project": project,
		"config":  config,
		"ip":      ip,
	}

	params := url.Values{
		"project": {project},
		"config":  {config},
	}

	_, _, err := c.doRequest(ctx, http.MethodDelete, "/configs/config/trusted_ips?"+params.Encode(), body, nil, RateLimitWrite)
	return err
}

// allowAllCIDR is the entry every config is created with. Nothing about it is
// special except that it is the default — a config whose only entry is this one
// has never been restricted, and a config that has been restricted had it
// removed, because leaving it in makes every other entry pointless.
const allowAllCIDR = "0.0.0.0/0"

// TrustedIPSet is a config's trusted ranges as a set.
type TrustedIPSet map[string]struct{}

// NewTrustedIPSet builds a set from a list.
func NewTrustedIPSet(ips []TrustedIP) TrustedIPSet {
	set := make(TrustedIPSet, len(ips))
	for _, ip := range ips {
		set[ip.IP] = struct{}{}
	}
	return set
}

// IsDefault reports whether this is an untouched config: allow-all and nothing
// else. Such a config has no restriction to migrate and cannot lock anyone out.
func (s TrustedIPSet) IsDefault() bool {
	if len(s) != 1 {
		return false
	}
	_, ok := s[allowAllCIDR]
	return ok
}

// AllowsEverything reports whether the set still contains the allow-all entry.
// While it does, no addition to the set can restrict access to anything.
func (s TrustedIPSet) AllowsEverything() bool {
	_, ok := s[allowAllCIDR]
	return ok
}

// Sorted returns the members in a stable order, for display and comparison.
func (s TrustedIPSet) Sorted() []string {
	out := make([]string, 0, len(s))
	for ip := range s {
		out = append(out, ip)
	}
	sort.Strings(out)
	return out
}

// Equal reports whether two sets hold the same ranges.
func (s TrustedIPSet) Equal(other TrustedIPSet) bool {
	if len(s) != len(other) {
		return false
	}
	for ip := range s {
		if _, ok := other[ip]; !ok {
			return false
		}
	}
	return true
}

// TrustedIPPlan is the work needed to make a target set match a source set.
type TrustedIPPlan struct {
	Add    []string
	Remove []string
}

// Empty reports whether the two sets already match.
func (p TrustedIPPlan) Empty() bool { return len(p.Add) == 0 && len(p.Remove) == 0 }

// RemovesAllowAll reports whether carrying out this plan closes the config to
// everything not explicitly listed — which is the moment a migrator running
// from outside those ranges loses access to it.
func (p TrustedIPPlan) RemovesAllowAll() bool {
	for _, ip := range p.Remove {
		if ip == allowAllCIDR {
			return true
		}
	}
	return false
}

// PlanTrustedIPs works out how to mirror source onto target.
//
// A mirror, not a merge. Adding the source's ranges and removing only the
// allow-all default leaves anything else the target happens to carry — a range
// from an earlier run against different source data, or one somebody added by
// hand — quietly permitting access to a config that reports as migrated.
//
// keep is exempted from removal. It exists for the migrator's own address
// during a cutover: that address is by definition not in the source, so a plain
// mirror would strip it on the next pass and lock the run out of the config it
// was about to finish.
func PlanTrustedIPs(source, target TrustedIPSet, keep ...string) TrustedIPPlan {
	exempt := make(map[string]struct{}, len(keep))
	for _, ip := range keep {
		if ip != "" {
			exempt[ip] = struct{}{}
		}
	}

	var plan TrustedIPPlan
	for ip := range source {
		if _, ok := target[ip]; !ok {
			plan.Add = append(plan.Add, ip)
		}
	}
	for ip := range target {
		if _, ok := source[ip]; ok {
			continue
		}
		if _, ok := exempt[ip]; ok {
			continue
		}
		plan.Remove = append(plan.Remove, ip)
	}

	sort.Strings(plan.Add)
	sort.Strings(plan.Remove)
	return plan
}
