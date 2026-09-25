package doppler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The rotated secrets endpoint is not paginated: it ignores page parameters and
// returns everything. Going through ListAll would re-request the same full page
// forever once a config has 100 or more, so this must issue exactly one request.
func TestListRotatedSecretsIssuesOneRequest(t *testing.T) {
	var requests int
	var gotQuery string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		gotQuery = r.URL.RawQuery

		secrets := make([]map[string]interface{}, 0, 100)
		for i := 0; i < 100; i++ {
			secrets = append(secrets, map[string]interface{}{
				"slug": "rs", "name": "db-password", "enabled": true,
				"status":              map[string]interface{}{"type": "retrying", "error": "creds rejected"},
				"rotation_period_sec": 86400,
				"integration":         map[string]interface{}{"slug": "int-1", "name": "AWS Prod", "type": "aws_secrets_manager"},
			})
		}
		w.Header().Set("Content-Type", "application/json")
		// Note the camelCase response key.
		json.NewEncoder(w).Encode(map[string]interface{}{"rotatedSecrets": secrets})
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "dp.ct.test")
	got, err := client.ListRotatedSecrets(context.Background(), "api", "prd")
	if err != nil {
		t.Fatalf("ListRotatedSecrets: %v", err)
	}

	if requests != 1 {
		t.Errorf("made %d requests, want exactly 1 — the endpoint is not paginated", requests)
	}
	if len(got) != 100 {
		t.Fatalf("expected 100 rotated secrets, got %d", len(got))
	}
	if gotQuery != "config=prd&project=api" {
		t.Errorf("query = %q, want project and config", gotQuery)
	}

	// project/config aren't in this payload, so the client fills them in.
	if got[0].Project != "api" || got[0].Config != "prd" {
		t.Errorf("project/config not backfilled: %+v", got[0])
	}
	if got[0].Status.Type != "retrying" || got[0].Status.Error != "creds rejected" {
		t.Errorf("status is a discriminated union, not a string: %+v", got[0].Status)
	}
	if got[0].Integration.Name != "AWS Prod" {
		t.Errorf("integration not parsed: %+v", got[0].Integration)
	}
}

func TestListServiceAccountTokensAndIdentities(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v3/workplace/service_accounts/service_account/sa-1/tokens":
			json.NewEncoder(w).Encode(map[string]interface{}{"api_tokens": []interface{}{
				map[string]interface{}{"name": "ci", "slug": "tok-1", "expires_at": nil},
			}})
		case r.URL.Path == "/v3/workplace/service_accounts/service_account/sa-1/identities":
			json.NewEncoder(w).Encode(map[string]interface{}{"identities": []interface{}{
				map[string]interface{}{
					"slug": "id-1", "name": "github-actions", "method": "oidc", "ttl_seconds": 3600,
					"config": map[string]interface{}{"discovery_url": "https://example.com", "claims_type": "exact"},
				},
			}})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "dp.ct.test")

	tokens, err := client.ListServiceAccountTokens(context.Background(), "sa-1")
	if err != nil {
		t.Fatalf("ListServiceAccountTokens: %v", err)
	}
	if len(tokens) != 1 || tokens[0].Name != "ci" {
		t.Fatalf("unexpected tokens: %+v", tokens)
	}
	// The API doesn't name the account in the payload, so the client attaches it.
	if tokens[0].ServiceAccount != "sa-1" {
		t.Errorf("token.ServiceAccount = %q, want sa-1", tokens[0].ServiceAccount)
	}

	identities, err := client.ListServiceAccountIdentities(context.Background(), "sa-1")
	if err != nil {
		t.Fatalf("ListServiceAccountIdentities: %v", err)
	}
	if len(identities) != 1 || identities[0].Method != "oidc" || identities[0].TTLSeconds != 3600 {
		t.Fatalf("unexpected identities: %+v", identities)
	}
	if identities[0].ServiceAccount != "sa-1" {
		t.Errorf("identity.ServiceAccount = %q, want sa-1", identities[0].ServiceAccount)
	}
	if len(identities[0].Config) == 0 {
		t.Error("config should be captured verbatim")
	}
}

func TestCreateServiceAccountIdentityDropsSourceSlug(t *testing.T) {
	var body map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"identity": map[string]interface{}{"slug": "new-slug", "name": body["name"]}})
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "dp.ct.test")
	identity := ServiceAccountIdentity{
		Slug: "source-slug", Name: "github-actions", Method: "oidc", TTLSeconds: 3600,
		Config: json.RawMessage(`{"discovery_url":"https://example.com"}`),
	}
	created, err := client.CreateServiceAccountIdentity(context.Background(), "tgt-sa", &identity)
	if err != nil {
		t.Fatalf("CreateServiceAccountIdentity: %v", err)
	}

	if _, ok := body["slug"]; ok {
		t.Error("request must not carry the source slug")
	}
	if body["method"] != "oidc" || body["ttl_seconds"].(float64) != 3600 {
		t.Errorf("unexpected body: %+v", body)
	}
	cfg, ok := body["config"].(map[string]interface{})
	if !ok || cfg["discovery_url"] != "https://example.com" {
		t.Errorf("config should be sent as an object, got %+v", body["config"])
	}
	if created.Slug != "new-slug" {
		t.Errorf("expected the target-assigned slug, got %q", created.Slug)
	}
}

func TestSetEnvironmentOrder(t *testing.T) {
	var body map[string]interface{}
	var query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"environments":[]}`))
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "dp.ct.test")
	if err := client.SetEnvironmentOrder(context.Background(), "api", []string{"dev", "stg", "prd"}); err != nil {
		t.Fatalf("SetEnvironmentOrder: %v", err)
	}

	if query != "project=api" {
		t.Errorf("query = %q, want project=api", query)
	}
	order, _ := body["order"].([]interface{})
	if len(order) != 3 || order[0] != "dev" || order[2] != "prd" {
		t.Errorf("unexpected order body: %+v", body["order"])
	}
}

func TestSetEnvironmentOrderSkipsEmpty(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "dp.ct.test")
	if err := client.SetEnvironmentOrder(context.Background(), "api", nil); err != nil {
		t.Fatalf("expected no error: %v", err)
	}
	if called {
		t.Error("an empty order should not issue a request")
	}
}

func TestSetConfigInherits(t *testing.T) {
	var body map[string]interface{}
	var path, query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, query = r.URL.Path, r.URL.RawQuery
		json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"config":{}}`))
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "dp.ct.test")
	err := client.SetConfigInherits(context.Background(), "api", "prd", []ConfigDescriptor{
		{Project: "shared", Config: "prd"},
	})
	if err != nil {
		t.Fatalf("SetConfigInherits: %v", err)
	}

	if path != "/v3/configs/config/inherits" {
		t.Errorf("path = %q", path)
	}
	if query != "config=prd&project=api" {
		t.Errorf("query = %q, want project and config", query)
	}

	inherits, _ := body["inherits"].([]interface{})
	if len(inherits) != 1 {
		t.Fatalf("unexpected inherits: %+v", body["inherits"])
	}
	ref := inherits[0].(map[string]interface{})
	if ref["project"] != "shared" || ref["config"] != "prd" {
		t.Errorf("unexpected reference: %+v", ref)
	}
}

// Clearing inheritance must send an empty array, not null — the API expects a list.
func TestSetConfigInheritsSendsEmptyArrayNotNull(t *testing.T) {
	var raw string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		raw = string(b)
		w.Write([]byte(`{"config":{}}`))
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "dp.ct.test")
	if err := client.SetConfigInherits(context.Background(), "api", "prd", nil); err != nil {
		t.Fatalf("SetConfigInherits: %v", err)
	}
	if !strings.Contains(raw, `"inherits":[]`) {
		t.Errorf("expected an empty array, got %s", raw)
	}
}

func TestUpdateWorkplaceUserAccess(t *testing.T) {
	var method, path string
	var body map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		json.NewDecoder(r.Body).Decode(&body)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "dp.ct.test")
	if err := client.UpdateWorkplaceUserAccess(context.Background(), "user-slug-1", "admin"); err != nil {
		t.Fatalf("UpdateWorkplaceUserAccess: %v", err)
	}

	if method != http.MethodPatch {
		t.Errorf("method = %s, want PATCH", method)
	}
	// The user slug is a path segment, not a query parameter.
	if path != "/v3/workplace/users/user-slug-1" {
		t.Errorf("path = %q", path)
	}
	if body["access"] != "admin" {
		t.Errorf("body = %+v", body)
	}
}

func TestGetTokenInfoParsesPrincipal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"name":"my token","slug":"tok-1","type":"personal",
			"principal":{"type":"workplace_user","slug":"user-9"},
			"workplace":{"name":"Acme","slug":"acme"}}`))
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "dp.pt.test")
	info, err := client.GetTokenInfo(context.Background())
	if err != nil {
		t.Fatalf("GetTokenInfo: %v", err)
	}
	if info.Principal.Type != "workplace_user" || info.Principal.Slug != "user-9" {
		t.Errorf("principal = %+v, want the workplace user identity", info.Principal)
	}
}
