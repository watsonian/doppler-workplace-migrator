package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/doppler/workplace-migrator/internal/doppler"
	"github.com/doppler/workplace-migrator/internal/migration"
)

// The contents travel through the handler to the page, not just out of the diff.
func TestCompareCountsANewProjectsContents(t *testing.T) {
	s, h := scopeServer(t)
	pd := &s.migrationData.Projects[0]
	pd.Environments = []doppler.Environment{{Slug: "dev"}, {Slug: "stg"}, {Slug: "prd"}}

	if rec := postJSON(t, h, "/api/v1/scope", migration.Scope{Mode: migration.ScopePartial, Projects: []string{"payments"}}); rec.Code != http.StatusOK {
		t.Fatalf("scope: %d %s", rec.Code, rec.Body.String())
	}
	rec := postJSON(t, h, "/api/v1/compare", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("compare: %d %s", rec.Code, rec.Body.String())
	}
	var out compareResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	for _, d := range out.Differences {
		if d.Kind == migration.KindProject {
			if d.Contains[migration.KindEnvironment] != 3 {
				t.Errorf("contains = %v", d.Contains)
			}
			return
		}
	}
	t.Fatal("no project create")
}

// A comparison that finds nothing — the result a top-up most hopes for — sent
// null for its differences, and the Compare page crashed looping over it.
func TestACompareThatFindsNothingSendsAnEmptyList(t *testing.T) {
	out := buildCompareResponse(&migration.Diff{Matched: map[migration.ResourceKind]int{}}, false)
	raw, _ := json.Marshal(out)
	if !strings.Contains(string(raw), `"differences":[]`) {
		t.Fatalf("got %s", raw)
	}
	raw, _ = json.Marshal(migration.ComputeDiff(&migration.MigrationData{}, nil))
	if !strings.Contains(string(raw), `"differences":[]`) {
		t.Fatalf("got %s", raw)
	}
}
