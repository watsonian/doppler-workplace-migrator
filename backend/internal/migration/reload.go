package migration

import (
	"context"
	"fmt"
	"strings"

	"github.com/doppler/onprem-migrator/internal/doppler"
)

// Secret values do not outlive the write that needs them. Once a config has
// been migrated its values are dropped from memory, so the workplace's secrets
// are not sitting in the process for the rest of the session. SecretCount stays.
//
// Anything that diffs against the source after that — a comparison once a run
// has finished, or the second stage of a full run after a partial — has to put
// them back first. Diffing a config whose values were dropped sees a source
// with no secrets, and every secret on the target then reads as "removed from
// the source": nothing is updated, and a run asked to delete what the source no
// longer has deletes all of it.

// readSourceSecrets reads one config's secrets as the migration needs them:
// raw values, so ${REFERENCE} syntax survives, and without the DOPPLER_
// secrets the instance manages itself and the API will not accept.
func readSourceSecrets(ctx context.Context, source DopplerClient, project, config string) (doppler.SecretsMap, error) {
	secrets, err := source.ListSecrets(ctx, project, config)
	if err != nil {
		return nil, err
	}
	for name := range secrets {
		if strings.HasPrefix(name, "DOPPLER_") {
			delete(secrets, name)
		}
	}
	if secrets == nil {
		secrets = doppler.SecretsMap{}
	}
	return secrets, nil
}

// valuesDropped reports whether a config's values were cleared after a write,
// as opposed to the config genuinely holding none.
func valuesDropped(cd *ConfigData) bool {
	return cd.Secrets == nil
}

// ReloadSecrets re-reads source values for configs whose values were dropped,
// in the listed projects (all of them when projects is empty). It returns how
// many configs it reloaded.
func ReloadSecrets(ctx context.Context, source DopplerClient, data *MigrationData, projects []string) (int, error) {
	want := map[string]bool{}
	for _, slug := range projects {
		want[slug] = true
	}

	type job struct{ project, config int }
	var jobs []job
	for p := range data.Projects {
		if len(want) > 0 && !want[data.Projects[p].Project.Slug] {
			continue
		}
		for c := range data.Projects[p].Configs {
			if valuesDropped(&data.Projects[p].Configs[c]) {
				jobs = append(jobs, job{p, c})
			}
		}
	}

	err := runPool(ctx, len(jobs), gatherConcurrency, func(ctx context.Context, i int) error {
		pd := &data.Projects[jobs[i].project]
		cd := &pd.Configs[jobs[i].config]
		secrets, err := readSourceSecrets(ctx, source, pd.Project.Slug, cd.Config.Name)
		if err != nil {
			return fmt.Errorf("reload secrets for %s/%s: %w", pd.Project.Slug, cd.Config.Name, err)
		}
		// Each job owns a distinct config, so these writes never overlap.
		cd.Secrets = secrets
		cd.SecretCount = len(secrets)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return len(jobs), nil
}
