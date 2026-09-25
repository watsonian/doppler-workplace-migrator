package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	envSource = "dp.sa.source0000000000000000000000000000000000abcd"
	envTarget = "dp.sa.target0000000000000000000000000000000000wxyz"
)

func envServer(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	t.Setenv("SOURCE_DOPPLER_TOKEN", envSource)
	t.Setenv("TARGET_DOPPLER_TOKEN", "  "+envTarget+"\n") // pasted with whitespace
	t.Setenv("DEV_MODE", "")
	s := NewServer()
	return s, newRouterWithServer(s, nil)
}

func getConfigure(t *testing.T, h http.Handler) (map[string]any, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newAPIRequest("GET", "/api/v1/configure", nil))
	var out map[string]any
	raw := rec.Body.String()
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	return out, raw
}

func TestEnvironmentTokensPrefillBeforeAnythingIsConfigured(t *testing.T) {
	_, h := envServer(t)
	out, raw := getConfigure(t, h)

	if out["configured"] != false {
		t.Fatalf("nothing has been configured yet: %v", out)
	}
	if out["has_source_token"] != true || out["has_target_token"] != true {
		t.Fatalf("the environment tokens should read as held: %v", out)
	}
	if out["source_token_from_env"] != true || out["target_token_from_env"] != true {
		t.Fatalf("the page should be told where they came from: %v", out)
	}
	if hint, _ := out["source_token_hint"].(string); !strings.HasPrefix(hint, "dp.sa.") || !strings.HasSuffix(hint, "abcd") {
		t.Fatalf("hint %q, want the prefix and last four", hint)
	}
	// The value itself never reaches the browser.
	if strings.Contains(raw, envSource) || strings.Contains(raw, envTarget) {
		t.Fatal("GET /configure returned a token value")
	}
}

func TestSubmittingWithoutTokensUsesTheEnvironments(t *testing.T) {
	s, h := envServer(t)
	body, _ := json.Marshal(map[string]any{
		"source_host": "https://api.doppler.com", "target_host": "https://target.example.com", "dev_mode": true,
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newAPIRequest("POST", "/api/v1/configure", bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("configure: %d %s", rec.Code, rec.Body.String())
	}
	if s.config.SourceToken != envSource || s.config.TargetToken != envTarget {
		t.Fatalf("got %q / %q, want the environment's (trimmed)", s.config.SourceToken, s.config.TargetToken)
	}
}

func TestATypedTokenBeatsTheEnvironment(t *testing.T) {
	s, h := envServer(t)
	typed := "dp.pt.typed000000000000000000000000000000000000001"
	body, _ := json.Marshal(map[string]any{
		"source_host": "https://api.doppler.com", "source_token": typed,
		"target_host": "https://target.example.com", "dev_mode": true,
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newAPIRequest("POST", "/api/v1/configure", bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("configure: %d %s", rec.Code, rec.Body.String())
	}
	if s.config.SourceToken != typed {
		t.Fatalf("got %q, want the typed token", s.config.SourceToken)
	}
	if s.config.TargetToken != envTarget {
		t.Fatalf("the untouched target should still come from the environment, got %q", s.config.TargetToken)
	}

	// And once configured, the page reports the typed one, not the environment's.
	out, _ := getConfigure(t, h)
	if out["source_token_from_env"] != false || out["target_token_from_env"] != true {
		t.Fatalf("got %v", out)
	}
}

func TestNoEnvironmentTokensMeansNothingHeld(t *testing.T) {
	t.Setenv("SOURCE_DOPPLER_TOKEN", "")
	t.Setenv("TARGET_DOPPLER_TOKEN", "")
	out, _ := getConfigure(t, newRouterWithServer(NewServer(), nil))
	if out["has_source_token"] != false || out["has_target_token"] != false {
		t.Fatalf("got %v", out)
	}
}
