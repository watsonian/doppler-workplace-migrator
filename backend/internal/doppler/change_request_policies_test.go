package doppler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListChangeRequestPolicies(t *testing.T) {
	var gotPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.URL.Path+"?"+r.URL.RawQuery)
		page := r.URL.Query().Get("page")
		w.Header().Set("Content-Type", "application/json")
		if page == "1" {
			// A full page forces the client to ask for a second one.
			policies := make([]map[string]interface{}, 0, 100)
			for i := 0; i < 100; i++ {
				policies = append(policies, map[string]interface{}{
					"id": fmt.Sprintf("id-%d", i), "name": fmt.Sprintf("Policy %d", i),
					"description": "", "rules": []interface{}{},
					"targets": map[string]interface{}{"allProjects": false, "projects": map[string]interface{}{}},
				})
			}
			json.NewEncoder(w).Encode(map[string]interface{}{"policies": policies, "page": 1})
			return
		}
		if page == "3" {
			json.NewEncoder(w).Encode(map[string]interface{}{"policies": []interface{}{}, "page": 3})
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"page": 2, "policies": []interface{}{
			map[string]interface{}{
				"id": "id-last", "name": "Production approvals", "description": "Two reviewers",
				"rules": []interface{}{
					map[string]interface{}{"type": "DisallowSelfReview"},
					map[string]interface{}{"type": "AutoAssignReviewers", "strategy": "matchCount"},
					map[string]interface{}{"type": "RequiredReviewer", "count": 2, "subjects": []interface{}{
						map[string]interface{}{"type": "Group", "slug": "grp-1", "name": "Platform", "isActive": true},
					}},
				},
				"targets": map[string]interface{}{
					"allProjects": false,
					"projects": map[string]interface{}{
						"api": map[string]interface{}{"all": false, "envSlugs": []string{"prd"}, "configNames": []string{"prd_eu"}},
						"web": map[string]interface{}{"all": true},
					},
				},
			},
		}})
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "dp.ct.test")
	policies, err := client.ListChangeRequestPolicies(context.Background())
	if err != nil {
		t.Fatalf("ListChangeRequestPolicies: %v", err)
	}

	if len(policies) != 101 {
		t.Fatalf("expected 101 policies across two pages, got %d", len(policies))
	}
	if len(gotPaths) != 3 {
		t.Errorf("expected 3 requests (last one empty ends it), got %d: %v", len(gotPaths), gotPaths)
	}

	last := policies[100]
	if last.Name != "Production approvals" || len(last.Rules) != 3 {
		t.Fatalf("unexpected policy: %+v", last)
	}
	rr := last.Rules[2]
	if rr.Type != RuleRequiredReviewer || rr.Count != 2 || len(rr.Subjects) != 1 {
		t.Errorf("unexpected RequiredReviewer rule: %+v", rr)
	}
	if rr.Subjects[0].Slug != "grp-1" || rr.Subjects[0].Name != "Platform" {
		t.Errorf("unexpected subject: %+v", rr.Subjects[0])
	}
	if last.Targets.Projects["web"] == nil || !last.Targets.Projects["web"].All {
		t.Error("expected web to be targeted with all=true")
	}
	api := last.Targets.Projects["api"]
	if api == nil || api.All || len(api.EnvSlugs) != 1 || api.ConfigNames[0] != "prd_eu" {
		t.Errorf("unexpected api target: %+v", api)
	}
}

func TestCreateChangeRequestPolicy(t *testing.T) {
	var body map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/v3/workplace/change_request_policies" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"policy": map[string]interface{}{"id": "new-id", "name": body["name"]}})
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "dp.ct.test")
	policy := samplePolicy()
	created, err := client.CreateChangeRequestPolicy(context.Background(), &policy)
	if err != nil {
		t.Fatalf("CreateChangeRequestPolicy: %v", err)
	}
	if created.ID != "new-id" {
		t.Errorf("expected the target-assigned id, got %q", created.ID)
	}

	// The source id must not be echoed back — the target assigns its own.
	if _, ok := body["id"]; ok {
		t.Error("request body should not carry an id")
	}

	rules := body["rules"].([]interface{})
	if len(rules) != 3 {
		t.Fatalf("expected 3 rules, got %d", len(rules))
	}

	// RequiredReviewer subjects must reduce to {type, slug}.
	rr := rules[0].(map[string]interface{})
	subs := rr["subjects"].([]interface{})
	sub := subs[0].(map[string]interface{})
	if _, ok := sub["name"]; ok {
		t.Error("subject should not include name on write")
	}
	if sub["type"] != "Group" || sub["slug"] != "grp-target" {
		t.Errorf("unexpected subject %+v", sub)
	}

	// An empty subject list must be omitted, not sent as [] (the API rejects []).
	rrAny := rules[1].(map[string]interface{})
	if _, ok := rrAny["subjects"]; ok {
		t.Error("an empty subject list should be omitted so the API reads it as 'any reviewer'")
	}

	// Fields belonging to other rule types are dropped.
	auto := rules[2].(map[string]interface{})
	if auto["strategy"] != "all" {
		t.Errorf("expected strategy to survive, got %+v", auto)
	}
	if _, ok := auto["count"]; ok {
		t.Error("count does not belong on AutoAssignReviewers")
	}
}

// samplePolicy builds a policy fixture for the create test.
func samplePolicy() ChangeRequestPolicy {
	return ChangeRequestPolicy{
		ID:          "source-id",
		Name:        "Production approvals",
		Description: "Two reviewers",
		Rules: []ChangeRequestPolicyRule{
			{Type: RuleRequiredReviewer, Count: 2, Subjects: []ChangeRequestPolicySubject{
				{Type: SubjectGroup, Slug: "grp-target", Name: "Platform"},
			}},
			{Type: RuleRequiredReviewer, Count: 1},
			{Type: RuleAutoAssignReviewers, Strategy: "all", Count: 9},
		},
		Targets: ChangeRequestPolicyTargets{
			AllProjects: true,
		},
	}
}
