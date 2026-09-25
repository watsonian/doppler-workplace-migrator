package doppler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// testResourceServer creates a test server that routes to different handlers by path.
func testResourceServer(t *testing.T, routes map[string]http.HandlerFunc) (*httptest.Server, *Client) {
	t.Helper()
	mux := http.NewServeMux()
	for path, handler := range routes {
		mux.HandleFunc(path, handler)
	}
	server := httptest.NewServer(mux)
	client := NewClient(server.URL, "dp.ct.testtoken", WithHTTPClient(server.Client()))
	return server, client
}

// --- Auth ---

func TestGetTokenInfo(t *testing.T) {
	server, client := testResourceServer(t, map[string]http.HandlerFunc{
		"/v3/me": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"name":      "My Token",
				"slug":      "my-token",
				"type":      "cli",
				"workplace": map[string]string{"slug": "wp1", "name": "Test Workplace"},
			})
		},
	})
	defer server.Close()

	info, err := client.GetTokenInfo(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Name != "My Token" {
		t.Errorf("expected name 'My Token', got %q", info.Name)
	}
	if info.Type != "cli" {
		t.Errorf("expected type 'cli', got %q", info.Type)
	}
}

func TestGetTokenInfoError(t *testing.T) {
	server, client := testResourceServer(t, map[string]http.HandlerFunc{
		"/v3/me": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"message": "Invalid token"})
		},
	})
	defer server.Close()

	_, err := client.GetTokenInfo(context.Background())
	if err == nil {
		t.Fatal("expected error for unauthorized token")
	}
}

// --- Workplace ---

func TestGetWorkplace(t *testing.T) {
	server, client := testResourceServer(t, map[string]http.HandlerFunc{
		"/v3/workplace": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"workplace": map[string]string{
					"id":             "wp-123",
					"name":           "Test Corp",
					"billing_email":  "billing@test.com",
					"security_email": "security@test.com",
				},
			})
		},
	})
	defer server.Close()

	wp, err := client.GetWorkplace(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wp.Name != "Test Corp" {
		t.Errorf("expected name 'Test Corp', got %q", wp.Name)
	}
	if wp.BillingEmail != "billing@test.com" {
		t.Errorf("expected billing email 'billing@test.com', got %q", wp.BillingEmail)
	}
}

func TestUpdateWorkplace(t *testing.T) {
	server, client := testResourceServer(t, map[string]http.HandlerFunc{
		"/v3/workplace": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Errorf("expected POST, got %s", r.Method)
			}
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"workplace": map[string]string{
					"name":           body["name"],
					"billing_email":  body["billing_email"],
					"security_email": body["security_email"],
				},
			})
		},
	})
	defer server.Close()

	wp, err := client.UpdateWorkplace(context.Background(), &Workplace{
		Name:          "Updated Corp",
		BillingEmail:  "new-billing@test.com",
		SecurityEmail: "new-sec@test.com",
	}, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wp.Name != "Updated Corp" {
		t.Errorf("expected name 'Updated Corp', got %q", wp.Name)
	}
}

// --- Projects ---

func TestListProjects(t *testing.T) {
	server, client := testResourceServer(t, map[string]http.HandlerFunc{
		"/v3/projects": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"projects": []map[string]string{
					{"name": "Project A", "slug": "project-a"},
					{"name": "Project B", "slug": "project-b"},
				},
			})
		},
	})
	defer server.Close()

	projects, err := client.ListProjects(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(projects) != 2 {
		t.Fatalf("expected 2 projects, got %d", len(projects))
	}
	if projects[0].Name != "Project A" {
		t.Errorf("expected 'Project A', got %q", projects[0].Name)
	}
}

func TestListProjectsEmpty(t *testing.T) {
	server, client := testResourceServer(t, map[string]http.HandlerFunc{
		"/v3/projects": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"projects": []interface{}{},
			})
		},
	})
	defer server.Close()

	projects, err := client.ListProjects(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(projects) != 0 {
		t.Errorf("expected 0 projects, got %d", len(projects))
	}
}

func TestGetProject(t *testing.T) {
	server, client := testResourceServer(t, map[string]http.HandlerFunc{
		"/v3/projects/project": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("project") != "my-proj" {
				t.Errorf("expected project=my-proj")
			}
			json.NewEncoder(w).Encode(map[string]interface{}{
				"project": map[string]string{
					"name": "My Project",
					"slug": "my-proj",
				},
			})
		},
	})
	defer server.Close()

	proj, err := client.GetProject(context.Background(), "my-proj")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if proj.Slug != "my-proj" {
		t.Errorf("expected slug 'my-proj', got %q", proj.Slug)
	}
}

func TestCreateProject(t *testing.T) {
	server, client := testResourceServer(t, map[string]http.HandlerFunc{
		"/v3/projects": func(w http.ResponseWriter, r *http.Request) {
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"project": map[string]string{
					"name":        body["name"],
					"slug":        "new-project",
					"description": body["description"],
				},
			})
		},
	})
	defer server.Close()

	proj, err := client.CreateProject(context.Background(), "New Project", "A description")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if proj.Slug != "new-project" {
		t.Errorf("expected slug 'new-project', got %q", proj.Slug)
	}
}

// --- Environments ---

func TestListEnvironments(t *testing.T) {
	server, client := testResourceServer(t, map[string]http.HandlerFunc{
		"/v3/environments": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("project") != "my-proj" {
				t.Errorf("expected project=my-proj")
			}
			json.NewEncoder(w).Encode(map[string]interface{}{
				"environments": []map[string]string{
					{"name": "Development", "slug": "dev"},
					{"name": "Production", "slug": "prd"},
				},
			})
		},
	})
	defer server.Close()

	envs, err := client.ListEnvironments(context.Background(), "my-proj")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(envs) != 2 {
		t.Fatalf("expected 2 environments, got %d", len(envs))
	}
}

func TestCreateEnvironment(t *testing.T) {
	server, client := testResourceServer(t, map[string]http.HandlerFunc{
		"/v3/environments": func(w http.ResponseWriter, r *http.Request) {
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"environment": map[string]string{
					"name": body["name"],
					"slug": body["slug"],
				},
			})
		},
	})
	defer server.Close()

	env, err := client.CreateEnvironment(context.Background(), "my-proj", "Staging", "stg", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if env.Slug != "stg" {
		t.Errorf("expected slug 'stg', got %q", env.Slug)
	}
}

// --- Configs ---

func TestListConfigs(t *testing.T) {
	server, client := testResourceServer(t, map[string]http.HandlerFunc{
		"/v3/configs": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"configs": []map[string]interface{}{
					{"name": "dev", "root": true, "environment": "dev"},
					{"name": "dev_feature", "root": false, "environment": "dev"},
				},
			})
		},
	})
	defer server.Close()

	configs, err := client.ListConfigs(context.Background(), "my-proj")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(configs) != 2 {
		t.Fatalf("expected 2 configs, got %d", len(configs))
	}
	if !configs[0].Root {
		t.Error("expected first config to be root")
	}
}

func TestGetConfig(t *testing.T) {
	server, client := testResourceServer(t, map[string]http.HandlerFunc{
		"/v3/configs/config": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"config": map[string]interface{}{
					"name":   "dev",
					"root":   true,
					"locked": false,
				},
			})
		},
	})
	defer server.Close()

	cfg, err := client.GetConfig(context.Background(), "my-proj", "dev")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Name != "dev" {
		t.Errorf("expected name 'dev', got %q", cfg.Name)
	}
}

func TestCreateConfig(t *testing.T) {
	server, client := testResourceServer(t, map[string]http.HandlerFunc{
		"/v3/configs": func(w http.ResponseWriter, r *http.Request) {
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"config": map[string]interface{}{
					"name":        body["name"],
					"environment": body["environment"],
					"root":        false,
				},
			})
		},
	})
	defer server.Close()

	cfg, err := client.CreateConfig(context.Background(), "my-proj", "dev", "dev_feature")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Name != "dev_feature" {
		t.Errorf("expected name 'dev_feature', got %q", cfg.Name)
	}
}

func TestLockConfig(t *testing.T) {
	server, client := testResourceServer(t, map[string]http.HandlerFunc{
		"/v3/configs/config/lock": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Errorf("expected POST, got %s", r.Method)
			}
			json.NewEncoder(w).Encode(map[string]interface{}{
				"config": map[string]interface{}{"locked": true},
			})
		},
	})
	defer server.Close()

	err := client.LockConfig(context.Background(), "my-proj", "dev")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSetConfigInheritable(t *testing.T) {
	server, client := testResourceServer(t, map[string]http.HandlerFunc{
		"/v3/configs/config/inheritable": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"config": map[string]interface{}{"inheritable": true},
			})
		},
	})
	defer server.Close()

	err := client.SetConfigInheritable(context.Background(), "my-proj", "dev", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- Secrets ---

func TestListSecrets(t *testing.T) {
	server, client := testResourceServer(t, map[string]http.HandlerFunc{
		"/v3/configs/config/secrets": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"secrets": map[string]interface{}{
					"API_KEY": map[string]string{
						"raw":           "sk-123",
						"computed":      "sk-123",
						"rawVisibility": "masked",
					},
					"DB_URL": map[string]string{
						"raw":      "postgres://localhost:5432/db",
						"computed": "postgres://localhost:5432/db",
						"note":     "Main database",
					},
				},
			})
		},
	})
	defer server.Close()

	secrets, err := client.ListSecrets(context.Background(), "my-proj", "dev")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(secrets) != 2 {
		t.Fatalf("expected 2 secrets, got %d", len(secrets))
	}
	if secrets["API_KEY"].Raw != "sk-123" {
		t.Errorf("expected raw 'sk-123', got %q", secrets["API_KEY"].Raw)
	}
	if secrets["DB_URL"].Note != "Main database" {
		t.Errorf("expected note 'Main database', got %q", secrets["DB_URL"].Note)
	}
}

func TestUpdateSecretsWithMetadata(t *testing.T) {
	server, client := testResourceServer(t, map[string]http.HandlerFunc{
		"/v3/configs/config/secrets": func(w http.ResponseWriter, r *http.Request) {
			var body map[string]interface{}
			json.NewDecoder(r.Body).Decode(&body)
			if _, ok := body["change_requests"]; !ok {
				t.Error("expected change_requests in body")
			}
			json.NewEncoder(w).Encode(map[string]interface{}{"secrets": map[string]interface{}{}})
		},
	})
	defer server.Close()

	val := "new-value"
	vis := "masked"
	err := client.UpdateSecretsWithMetadata(context.Background(), "my-proj", "dev", []SecretChangeRequest{
		{Name: "API_KEY", Value: &val, Visibility: &vis},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUpdateSecretNote(t *testing.T) {
	server, client := testResourceServer(t, map[string]http.HandlerFunc{
		"/v3/projects/project/note": func(w http.ResponseWriter, r *http.Request) {
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			if body["secret"] != "API_KEY" {
				t.Errorf("expected secret 'API_KEY', got %q", body["secret"])
			}
			if body["note"] != "Important key" {
				t.Errorf("expected note 'Important key', got %q", body["note"])
			}
			json.NewEncoder(w).Encode(map[string]string{"success": "true"})
		},
	})
	defer server.Close()

	err := client.UpdateSecretNote(context.Background(), "my-proj", "API_KEY", "Important key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- Roles ---

func TestListWorkplaceRoles(t *testing.T) {
	server, client := testResourceServer(t, map[string]http.HandlerFunc{
		"/v3/workplace/roles": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"roles": []map[string]interface{}{
					{"name": "Admin", "identifier": "admin", "is_custom_role": false},
					{"name": "DevOps", "identifier": "devops", "is_custom_role": true},
				},
			})
		},
	})
	defer server.Close()

	roles, err := client.ListWorkplaceRoles(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(roles) != 2 {
		t.Fatalf("expected 2 roles, got %d", len(roles))
	}
	if !roles[1].IsCustomRole {
		t.Error("expected second role to be custom")
	}
}

func TestCreateWorkplaceRole(t *testing.T) {
	server, client := testResourceServer(t, map[string]http.HandlerFunc{
		"/v3/workplace/roles": func(w http.ResponseWriter, r *http.Request) {
			var body map[string]interface{}
			json.NewDecoder(r.Body).Decode(&body)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"role": map[string]interface{}{
					"name":           body["name"],
					"identifier":     "custom-role",
					"is_custom_role": true,
				},
			})
		},
	})
	defer server.Close()

	role, err := client.CreateWorkplaceRole(context.Background(), "Custom Role", []string{"read", "write"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if role.Name != "Custom Role" {
		t.Errorf("expected name 'Custom Role', got %q", role.Name)
	}
}

func TestListProjectRoles(t *testing.T) {
	server, client := testResourceServer(t, map[string]http.HandlerFunc{
		"/v3/projects/roles": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"roles": []map[string]interface{}{
					{"name": "Viewer", "identifier": "viewer"},
				},
			})
		},
	})
	defer server.Close()

	roles, err := client.ListProjectRoles(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(roles) != 1 {
		t.Fatalf("expected 1 role, got %d", len(roles))
	}
}

func TestCreateProjectRole(t *testing.T) {
	server, client := testResourceServer(t, map[string]http.HandlerFunc{
		"/v3/projects/roles": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"role": map[string]interface{}{
					"name":       "Config Manager",
					"identifier": "config-manager",
				},
			})
		},
	})
	defer server.Close()

	role, err := client.CreateProjectRole(context.Background(), "Config Manager", []string{"secrets.read"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if role.Name != "Config Manager" {
		t.Errorf("expected name 'Config Manager', got %q", role.Name)
	}
}

// --- Groups ---

func TestListGroups(t *testing.T) {
	server, client := testResourceServer(t, map[string]http.HandlerFunc{
		"/v3/workplace/groups": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"groups": []map[string]string{
					{"name": "Engineering", "slug": "engineering"},
					{"name": "DevOps", "slug": "devops"},
				},
			})
		},
	})
	defer server.Close()

	groups, err := client.ListGroups(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}
}

func TestCreateGroup(t *testing.T) {
	server, client := testResourceServer(t, map[string]http.HandlerFunc{
		"/v3/workplace/groups": func(w http.ResponseWriter, r *http.Request) {
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"group": map[string]string{
					"name": body["name"],
					"slug": "new-group",
				},
			})
		},
	})
	defer server.Close()

	group, err := client.CreateGroup(context.Background(), &Group{Name: "New Group"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if group.Name != "New Group" {
		t.Errorf("expected name 'New Group', got %q", group.Name)
	}
}

func TestGetGroupMembers(t *testing.T) {
	server, client := testResourceServer(t, map[string]http.HandlerFunc{
		"/v3/workplace/groups/group/engineering/members": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"members": []map[string]string{
					{"type": "workplace_user", "slug": "user-1"},
					{"type": "workplace_user", "slug": "user-2"},
				},
			})
		},
	})
	defer server.Close()

	members, err := client.GetGroupMembers(context.Background(), "engineering")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("expected 2 members, got %d", len(members))
	}
}

func TestAddGroupMember(t *testing.T) {
	server, client := testResourceServer(t, map[string]http.HandlerFunc{
		"/v3/workplace/groups/group/engineering/members": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Errorf("expected POST, got %s", r.Method)
			}
			json.NewEncoder(w).Encode(map[string]string{"success": "true"})
		},
	})
	defer server.Close()

	err := client.AddGroupMember(context.Background(), "engineering", "workplace_user", "user-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- Service Accounts ---

func TestListServiceAccounts(t *testing.T) {
	server, client := testResourceServer(t, map[string]http.HandlerFunc{
		"/v3/workplace/service_accounts": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"service_accounts": []map[string]string{
					{"name": "CI Bot", "slug": "ci-bot"},
				},
			})
		},
	})
	defer server.Close()

	accounts, err := client.ListServiceAccounts(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(accounts) != 1 {
		t.Fatalf("expected 1 account, got %d", len(accounts))
	}
}

func TestCreateServiceAccount(t *testing.T) {
	server, client := testResourceServer(t, map[string]http.HandlerFunc{
		"/v3/workplace/service_accounts": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"service_account": map[string]string{
					"name": "Deploy Bot",
					"slug": "deploy-bot",
				},
			})
		},
	})
	defer server.Close()

	sa, err := client.CreateServiceAccount(context.Background(), &ServiceAccount{Name: "Deploy Bot"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sa.Slug != "deploy-bot" {
		t.Errorf("expected slug 'deploy-bot', got %q", sa.Slug)
	}
}

// --- Service Tokens ---

func TestListServiceTokens(t *testing.T) {
	server, client := testResourceServer(t, map[string]http.HandlerFunc{
		"/v3/configs/config/tokens": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"tokens": []map[string]interface{}{
					{
						"name":        "deploy-token",
						"slug":        "deploy-token",
						"project":     "my-proj",
						"config":      "prd",
						"environment": "prd",
						"access":      "read",
						"expires_at":  nil,
					},
				},
			})
		},
	})
	defer server.Close()

	tokens, err := client.ListServiceTokens(context.Background(), "my-proj", "prd")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tokens) != 1 {
		t.Fatalf("expected 1 token, got %d", len(tokens))
	}
	if tokens[0].Access != "read" {
		t.Errorf("expected access 'read', got %q", tokens[0].Access)
	}
}

// --- Trusted IPs ---

func TestListTrustedIPs(t *testing.T) {
	server, client := testResourceServer(t, map[string]http.HandlerFunc{
		"/v3/configs/config/trusted_ips": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"ips": []string{
					"10.0.0.0/8",
					"192.168.1.0/24",
				},
			})
		},
	})
	defer server.Close()

	ips, err := client.ListTrustedIPs(context.Background(), "my-proj", "prd")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ips) != 2 {
		t.Fatalf("expected 2 IPs, got %d", len(ips))
	}
}

func TestAddTrustedIP(t *testing.T) {
	server, client := testResourceServer(t, map[string]http.HandlerFunc{
		"/v3/configs/config/trusted_ips": func(w http.ResponseWriter, r *http.Request) {
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			if body["ip"] != "10.0.0.0/8" {
				t.Errorf("expected ip '10.0.0.0/8', got %q", body["ip"])
			}
			json.NewEncoder(w).Encode(map[string]string{"success": "true"})
		},
	})
	defer server.Close()

	err := client.AddTrustedIP(context.Background(), "my-proj", "prd", "10.0.0.0/8")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- Webhooks ---

func TestListWebhooks(t *testing.T) {
	server, client := testResourceServer(t, map[string]http.HandlerFunc{
		"/v3/webhooks": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"webhooks": []map[string]interface{}{
					{"id": "wh-1", "url": "https://example.com/hook", "enabled": true},
				},
			})
		},
	})
	defer server.Close()

	webhooks, err := client.ListWebhooks(context.Background(), "my-proj")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(webhooks) != 1 {
		t.Fatalf("expected 1 webhook, got %d", len(webhooks))
	}
	if webhooks[0].URL != "https://example.com/hook" {
		t.Errorf("expected URL 'https://example.com/hook', got %q", webhooks[0].URL)
	}
}

func TestCreateWebhook(t *testing.T) {
	server, client := testResourceServer(t, map[string]http.HandlerFunc{
		"/v3/webhooks": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"webhook": map[string]interface{}{
					"id":      "wh-new",
					"url":     "https://example.com/hook",
					"enabled": true,
				},
			})
		},
	})
	defer server.Close()

	wh, err := client.CreateWebhook(context.Background(), "my-proj", &Webhook{
		URL:     "https://example.com/hook",
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wh.ID != "wh-new" {
		t.Errorf("expected id 'wh-new', got %q", wh.ID)
	}
}

// --- Integrations ---

func TestListIntegrations(t *testing.T) {
	server, client := testResourceServer(t, map[string]http.HandlerFunc{
		"/v3/integrations": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"integrations": []map[string]interface{}{
					{"name": "AWS", "type": "aws_secrets_manager", "slug": "aws-1", "enabled": true},
					{"name": "GitHub", "type": "github_actions", "slug": "gh-1", "enabled": true},
				},
			})
		},
	})
	defer server.Close()

	integrations, err := client.ListIntegrations(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(integrations) != 2 {
		t.Fatalf("expected 2 integrations, got %d", len(integrations))
	}
	if integrations[0].Type != "aws_secrets_manager" {
		t.Errorf("expected type 'aws_secrets_manager', got %q", integrations[0].Type)
	}
}

func TestListIntegrationsEmpty(t *testing.T) {
	server, client := testResourceServer(t, map[string]http.HandlerFunc{
		"/v3/integrations": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"integrations": []interface{}{},
			})
		},
	})
	defer server.Close()

	integrations, err := client.ListIntegrations(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(integrations) != 0 {
		t.Errorf("expected 0 integrations, got %d", len(integrations))
	}
}

// --- Project Members ---

func TestListProjectMembers(t *testing.T) {
	server, client := testResourceServer(t, map[string]http.HandlerFunc{
		"/v3/projects/project/members": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"members": []map[string]interface{}{
					{
						"type":                    "workplace_user",
						"slug":                    "user-1",
						"role":                    map[string]string{"identifier": "admin"},
						"access_all_environments": true,
					},
				},
			})
		},
	})
	defer server.Close()

	members, err := client.ListProjectMembers(context.Background(), "my-proj")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(members) != 1 {
		t.Fatalf("expected 1 member, got %d", len(members))
	}
	if members[0].Role.Identifier != "admin" {
		t.Errorf("expected role 'admin', got %q", members[0].Role.Identifier)
	}
}

func TestAddProjectMember(t *testing.T) {
	server, client := testResourceServer(t, map[string]http.HandlerFunc{
		"/v3/projects/project/members": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Errorf("expected POST, got %s", r.Method)
			}
			json.NewEncoder(w).Encode(map[string]string{"success": "true"})
		},
	})
	defer server.Close()

	err := client.AddProjectMember(context.Background(), "my-proj", "workplace_user", "user-1", "admin", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- Users ---

func TestListWorkplaceUsers(t *testing.T) {
	server, client := testResourceServer(t, map[string]http.HandlerFunc{
		"/v3/workplace/users": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"workplace_users": []map[string]interface{}{
					{
						"id":     "user-1",
						"access": "owner",
						"user": map[string]string{
							"email":    "alice@test.com",
							"name":     "Alice",
							"username": "alice",
						},
					},
					{
						"id":     "user-2",
						"access": "collaborator",
						"user": map[string]string{
							"email":    "bob@test.com",
							"name":     "Bob",
							"username": "bob",
						},
					},
				},
			})
		},
	})
	defer server.Close()

	users, err := client.ListWorkplaceUsers(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("expected 2 users, got %d", len(users))
	}
	if users[0].User.Email != "alice@test.com" {
		t.Errorf("expected email 'alice@test.com', got %q", users[0].User.Email)
	}
}

func TestListWorkplaceUsersEmpty(t *testing.T) {
	server, client := testResourceServer(t, map[string]http.HandlerFunc{
		"/v3/workplace/users": func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"workplace_users": []interface{}{},
			})
		},
	})
	defer server.Close()

	users, err := client.ListWorkplaceUsers(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(users) != 0 {
		t.Errorf("expected 0 users, got %d", len(users))
	}
}
