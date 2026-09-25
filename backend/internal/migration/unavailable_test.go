package migration

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/doppler/workplace-migrator/internal/doppler"
)

func forbidden(msg string) error {
	return &doppler.APIError{StatusCode: 403, Message: msg}
}

func TestAReadOnlySourceTokenGathersWhatItCan(t *testing.T) {
	// A read-only token cannot list webhooks, members, service tokens or
	// trusted IPs: Doppler has no read-only permission for them. Refusals used
	// to stop the whole gather.
	mc := fullMockClient()
	for _, p := range []string{"backend", "frontend"} {
		mc.webhooksErr[p] = forbidden("Your workplace does not have access to Webhooks.")
		mc.membersErr[p] = forbidden("You do not have access to this project's members.")
	}
	for _, c := range []string{"backend/dev", "backend/prd", "frontend/dev"} {
		mc.serviceTokensErr[c] = forbidden("You do not have access to service tokens.")
		mc.trustedIPsErr[c] = forbidden("You do not have access to Trusted IPs.")
	}
	// And a plan without change request policies.
	mc.changeRequestPoliciesErr = forbidden("You do not have access to change request policies.")

	data, err := NewGatherer(mc, nil).GatherAll(context.Background())
	if err != nil {
		t.Fatalf("the gather should carry on: %v", err)
	}
	if len(data.Projects) != 2 || len(data.Projects[0].Configs) == 0 || len(data.Projects[0].Configs[0].Secrets) == 0 {
		t.Fatal("secrets should still be gathered")
	}

	got := map[string]UnavailableResource{}
	for _, u := range data.Unavailable {
		got[u.Resource] = u
	}
	for _, r := range []string{ResourceWebhooks, ResourceProjectMembers, ResourceServiceTokens, ResourceTrustedIPs, ResourceChangeRequestPolicies} {
		if _, ok := got[r]; !ok {
			t.Errorf("%s should be recorded as unavailable", r)
		}
	}
	if u := got[ResourceTrustedIPs]; u.Where != 3 || !strings.Contains(u.Message, "Trusted IPs") || !strings.Contains(u.Permission, "Manage Trusted IPs") {
		t.Errorf("trusted IPs %+v", u)
	}

	// It is the first thing in the manual steps.
	plan := GeneratePlan(data)
	if plan.ManualSteps[0].Category != "unavailable" || len(plan.ManualSteps[0].Items) != 5 {
		t.Errorf("manual steps %+v", plan.ManualSteps[0])
	}
}

func TestOnlyARefusalIsTolerated(t *testing.T) {
	// A 500, a timeout, a bad response: those still stop the gather, since
	// carrying on would quietly migrate less than the token can read.
	mc := fullMockClient()
	mc.webhooksErr["backend"] = &doppler.APIError{StatusCode: 500, Message: "boom"}
	if _, err := NewGatherer(mc, nil).GatherAll(context.Background()); err == nil {
		t.Fatal("a server error should stop the gather")
	}

	mc = fullMockClient()
	mc.trustedIPsErr["backend/prd"] = errors.New("connection reset")
	if _, err := NewGatherer(mc, nil).GatherAll(context.Background()); err == nil {
		t.Fatal("a network error should stop the gather")
	}
}

func TestSecretsAreStillRequired(t *testing.T) {
	mc := fullMockClient()
	mc.secretsErr["backend/dev"] = forbidden("You do not have access to this config's secrets.")
	if _, err := NewGatherer(mc, nil).GatherAll(context.Background()); err == nil {
		t.Fatal("secrets are the migration; a refusal to read them must stop it")
	}
}
