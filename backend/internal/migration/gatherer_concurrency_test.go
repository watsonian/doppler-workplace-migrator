package migration

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/doppler/onprem-migrator/internal/doppler"
)

// slowSource is a mock source whose secret reads take time, and which records
// how many were in flight at once. The delay can depend on the config, so calls
// can be made to finish out of order.
type slowSource struct {
	*mockClient
	delay func(project, config string) time.Duration

	inFlight atomic.Int32
	peak     atomic.Int32
}

func (s *slowSource) ListSecrets(ctx context.Context, project, config string) (doppler.SecretsMap, error) {
	n := s.inFlight.Add(1)
	defer s.inFlight.Add(-1)
	for {
		p := s.peak.Load()
		if n <= p || s.peak.CompareAndSwap(p, n) {
			break
		}
	}
	select {
	case <-time.After(s.delay(project, config)):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return s.mockClient.ListSecrets(ctx, project, config)
}

// workplace builds a source with the given number of projects, each holding
// configsPer configs, with one service token per config so order is visible.
func workplace(projects, configsPer int) *mockClient {
	m := newMockClient()
	for p := 0; p < projects; p++ {
		slug := fmt.Sprintf("p%02d", p)
		m.projects = append(m.projects, doppler.Project{Slug: slug, Name: slug})
		for c := 0; c < configsPer; c++ {
			name := fmt.Sprintf("c%02d", c)
			m.configs[slug] = append(m.configs[slug], doppler.Config{Name: name, Project: slug})
			key := slug + "/" + name
			m.secrets[key] = doppler.SecretsMap{"KEY": {Raw: key}}
			m.serviceTokens[key] = []doppler.ServiceToken{{Project: slug, Config: name, Name: key}}
		}
	}
	return m
}

func TestProjectStageRunsConcurrentlyWithinItsBound(t *testing.T) {
	src := &slowSource{mockClient: workplace(4, 6), delay: func(string, string) time.Duration { return 15 * time.Millisecond }}

	if _, err := NewGatherer(src, nil).GatherAll(context.Background()); err != nil {
		t.Fatal(err)
	}

	peak := src.peak.Load()
	if peak < 2 {
		t.Fatalf("at most %d secret read in flight — the stage is still one request at a time", peak)
	}
	if peak > gatherConcurrency {
		t.Fatalf("%d secret reads in flight, above the bound of %d", peak, gatherConcurrency)
	}
}

func TestProjectStageKeepsSourceOrder(t *testing.T) {
	// Earlier configs are made slower, so they finish last. The result must
	// still read in source order — the plan, the checkpoint keys and the token
	// worklist all come out in this order.
	src := &slowSource{mockClient: workplace(3, 5), delay: func(project, config string) time.Duration {
		var p, c int
		fmt.Sscanf(project, "p%d", &p)
		fmt.Sscanf(config, "c%d", &c)
		return time.Duration(30-5*p-4*c) * time.Millisecond
	}}

	data, err := NewGatherer(src, nil).GatherAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	var got []string
	for _, pd := range data.Projects {
		for _, cd := range pd.Configs {
			got = append(got, pd.Project.Slug+"/"+cd.Config.Name)
		}
	}
	var tokens []string
	for _, st := range data.ServiceTokens {
		tokens = append(tokens, st.Name)
	}

	want := make([]string, 0, 15)
	for p := 0; p < 3; p++ {
		for c := 0; c < 5; c++ {
			want = append(want, fmt.Sprintf("p%02d/c%02d", p, c))
		}
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("configs out of order:\n got %v\nwant %v", got, want)
	}
	if strings.Join(tokens, ",") != strings.Join(want, ",") {
		t.Fatalf("service tokens out of order:\n got %v\nwant %v", tokens, want)
	}
	// And every config's own secrets landed on the right config.
	for _, pd := range data.Projects {
		for _, cd := range pd.Configs {
			if key := pd.Project.Slug + "/" + cd.Config.Name; cd.Secrets["KEY"].Raw != key {
				t.Fatalf("%s holds secrets from %s", key, cd.Secrets["KEY"].Raw)
			}
		}
	}
}

func TestProjectStageReportsTheFailureNotTheCancellation(t *testing.T) {
	// One config fails; the others are cancelled because of it. The error that
	// comes back has to be the one that explains what happened.
	m := workplace(3, 4)
	m.secretsErr["p01/c02"] = errors.New("403 forbidden")
	src := &slowSource{mockClient: m, delay: func(string, string) time.Duration { return 5 * time.Millisecond }}

	_, err := NewGatherer(src, nil).GatherAll(context.Background())
	if err == nil {
		t.Fatal("a failed read should fail the gather")
	}
	if !strings.Contains(err.Error(), "p01/c02") || !strings.Contains(err.Error(), "403") {
		t.Fatalf("got %q, want the failing config and its cause", err)
	}
	if errors.Is(err, context.Canceled) {
		t.Fatal("the cancellation the failure caused was reported instead of the failure")
	}
}

func TestProjectStageStopsWhenCancelled(t *testing.T) {
	src := &slowSource{mockClient: workplace(4, 10), delay: func(string, string) time.Duration { return 50 * time.Millisecond }}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(30*time.Millisecond, cancel)

	start := time.Now()
	_, err := NewGatherer(src, nil).GatherAll(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want the cancellation", err)
	}
	// 40 configs at 50ms, six at a time, would be ~350ms if it ignored the stop.
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("took %v after being cancelled", elapsed)
	}
}

func TestProjectStageReportsProgressPerConfig(t *testing.T) {
	// Progress used to arrive once per project, so a project with many configs
	// sat silent and the stage looked stuck. It now counts every config.
	var (
		mu     sync.Mutex
		events []GatherProgress
	)
	record := func(p GatherProgress) {
		mu.Lock()
		events = append(events, p)
		mu.Unlock()
	}
	m := workplace(3, 4)
	m.projects = append(m.projects, doppler.Project{Slug: "empty", Name: "empty"}) // no configs at all

	if _, err := NewGatherer(m, record).GatherAll(context.Background()); err != nil {
		t.Fatal(err)
	}

	var configTicks, lastProject []int
	var lastProjectMsg string
	for _, e := range events {
		switch {
		case e.Resource == "configs" && e.Total == 12:
			configTicks = append(configTicks, e.Current)
		case e.Resource == "projects" && e.Current > 0:
			lastProject = append(lastProject, e.Current)
			lastProjectMsg = e.Message
		}
	}

	if len(configTicks) < 12 {
		t.Fatalf("got %d config progress events, want one per config: %v", len(configTicks), configTicks)
	}
	for i := 1; i < len(configTicks); i++ {
		if configTicks[i] < configTicks[i-1] {
			t.Fatalf("config progress went backwards: %v", configTicks)
		}
	}
	if len(lastProject) != 4 || lastProject[len(lastProject)-1] != 4 {
		t.Fatalf("project progress: %v — every project, including the one with no configs, should finish", lastProject)
	}
	if lastProjectMsg != "4 of 4 projects" {
		t.Fatalf("got %q", lastProjectMsg)
	}
}

func TestRunPoolWithNothingToDo(t *testing.T) {
	calls := 0
	if err := runPool(context.Background(), 0, 4, func(context.Context, int) error { calls++; return nil }); err != nil || calls != 0 {
		t.Fatalf("err %v, calls %d", err, calls)
	}
}
