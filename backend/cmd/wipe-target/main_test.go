package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/doppleruniversity/workplace-migrator/internal/doppler"
)

// stubTarget is a target workplace that records every DELETE it receives.
type stubTarget struct {
	name        string
	principal   map[string]string
	policiesErr bool

	mu      sync.Mutex
	deletes []string
}

func (s *stubTarget) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/v3")
		if r.Method == http.MethodDelete {
			s.mu.Lock()
			d := path
			if q := r.URL.RawQuery; q != "" {
				d += "?" + q
			}
			s.deletes = append(s.deletes, d)
			s.mu.Unlock()
			_, _ = w.Write([]byte(`{"success":true}`))
			return
		}

		var body any
		switch path {
		case "/workplace":
			body = map[string]any{"workplace": map[string]any{"id": "wp", "name": s.name}}
		case "/me":
			body = map[string]any{"name": "migrator", "principal": s.principal}
		case "/workplace/change_request_policies":
			if s.policiesErr {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"messages":["not on this plan"]}`))
				return
			}
			body = map[string]any{"policies": []any{map[string]any{"id": "pol-1", "name": "Payments review"}}}
		case "/projects":
			body = map[string]any{"projects": []any{
				map[string]any{"slug": "api", "name": "api"},
				map[string]any{"slug": "payments", "name": "payments"},
			}}
		case "/workplace/groups":
			body = map[string]any{"groups": []any{map[string]any{"slug": "admins", "name": "Admins"}}}
		case "/workplace/service_accounts":
			body = map[string]any{"service_accounts": []any{
				map[string]any{"slug": "ci", "name": "CI"},
				map[string]any{"slug": "migrator-sa", "name": "Migrator"},
			}}
		case "/projects/roles":
			body = map[string]any{"roles": []any{
				map[string]any{"identifier": "deployer", "name": "Deployer", "is_custom_role": true},
				map[string]any{"identifier": "viewer", "name": "Viewer", "is_custom_role": false},
			}}
		case "/workplace/roles":
			body = map[string]any{"roles": []any{
				map[string]any{"identifier": "ci-deployer", "name": "CI Deployer", "is_custom_role": true},
				map[string]any{"identifier": "owner", "name": "Owner", "is_custom_role": false},
			}}
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	})
}

func runAgainst(t *testing.T, stub *stubTarget, opts options) (int, string) {
	t.Helper()
	srv := httptest.NewServer(stub.handler(t))
	defer srv.Close()
	var out bytes.Buffer
	code := run(context.Background(), doppler.NewClient(srv.URL, "dp.sa.test"), srv.URL, opts, &out)
	return code, out.String()
}

func TestRefusesAnyOtherWorkplace(t *testing.T) {
	// The only guard between this and a real workplace.
	for _, name := range []string{"Acme Production", "migrator target", "Migrator Target ", ""} {
		stub := &stubTarget{name: name}
		code, out := runAgainst(t, stub, options{execute: true})

		if code != 2 {
			t.Errorf("%q: exit %d, want 2", name, code)
		}
		if len(stub.deletes) != 0 {
			t.Fatalf("%q: sent %d deletes to a workplace it should have refused: %v", name, len(stub.deletes), stub.deletes)
		}
		if !strings.Contains(out, "Refusing") {
			t.Errorf("%q: output does not say it refused:\n%s", name, out)
		}
	}
}

func TestDryRunDeletesNothing(t *testing.T) {
	stub := &stubTarget{name: requiredWorkplaceName}
	code, out := runAgainst(t, stub, options{})

	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if len(stub.deletes) != 0 {
		t.Fatalf("a dry run sent deletes: %v", stub.deletes)
	}
	for _, want := range []string{"Will delete 8", "api", "payments", "Admins", "Deployer", "CI Deployer", "Payments review", "--yes"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry run output is missing %q:\n%s", want, out)
		}
	}
}

func TestDeletesDependentsFirstAndSparesWhatItShould(t *testing.T) {
	stub := &stubTarget{
		name:      requiredWorkplaceName,
		principal: map[string]string{"type": "service_account", "slug": "migrator-sa"},
	}
	code, out := runAgainst(t, stub, options{execute: true})
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}

	want := []string{
		"/workplace/change_request_policies/change_request_policy/pol-1",
		"/projects/project?project=api",
		"/projects/project?project=payments",
		"/workplace/groups/group/admins",
		"/workplace/service_accounts/service_account/ci",
		"/projects/roles/role/deployer",
		"/workplace/roles/role/ci-deployer",
	}
	if strings.Join(stub.deletes, "\n") != strings.Join(want, "\n") {
		t.Fatalf("deletes:\n%s\n\nwant:\n%s", strings.Join(stub.deletes, "\n"), strings.Join(want, "\n"))
	}
	// Deleting its own account would revoke the token partway through.
	if !strings.Contains(out, "keeping service account migrator-sa") {
		t.Errorf("should say it kept the token's own account:\n%s", out)
	}
}

func TestAWorkplaceWithoutPoliciesStillWipes(t *testing.T) {
	// Policies are a paid feature; a plan without them refuses the list.
	stub := &stubTarget{name: requiredWorkplaceName, policiesErr: true}
	code, out := runAgainst(t, stub, options{execute: true})
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if len(stub.deletes) != 7 {
		t.Fatalf("got %d deletes, want everything but the policy: %v", len(stub.deletes), stub.deletes)
	}
	if !strings.Contains(out, "could not list change-request policies") {
		t.Errorf("should say why no policies were deleted:\n%s", out)
	}
}

func TestClearStateRemovesOnlyTheMigratorsFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range append(stateFiles, "unrelated.json") {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	stub := &stubTarget{name: requiredWorkplaceName}
	if code, out := runAgainst(t, stub, options{execute: true, clearState: true, stateDir: dir}); code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}

	for _, name := range stateFiles {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("%s survived --clear-state", name)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "unrelated.json")); err != nil {
		t.Error("--clear-state removed a file that is not the migrator's")
	}
}

func TestClearStateNeedsYesToo(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, stateFiles[0])
	_ = os.WriteFile(path, []byte("{}"), 0o600)

	stub := &stubTarget{name: requiredWorkplaceName}
	runAgainst(t, stub, options{clearState: true, stateDir: dir})

	if _, err := os.Stat(path); err != nil {
		t.Fatal("a dry run cleared the migrator's state")
	}
}
