package app

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/config"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/provider/fake"
)

// fakeClock fires every After immediately and records the requested waits.
type fakeClock struct {
	mu    sync.Mutex
	now   time.Time
	waits []time.Duration
}

func newClock() *fakeClock { return &fakeClock{now: time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.waits = append(c.waits, d)
	c.now = c.now.Add(d)
	ch := make(chan time.Time, 1)
	ch <- c.now
	return ch
}

func (c *fakeClock) Waits() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.waits...)
}

func testConfig(root string) *config.Config {
	yes := true
	return &config.Config{
		RepoRoot:    root,
		ProjectPath: filepath.Join(root, ".bam.yaml"),
		MachinePath: "/home/jdoe/.config/bam/config.yaml",
		Project: &config.ProjectFile{Version: 1,
			Servers:       map[string]config.Server{"work": {URL: "https://bamboo.example.com"}},
			DefaultServer: "work",
			Projects:      []string{"PROJ"},
			Targets: map[string]config.Target{
				"build": {Plan: "PROJ-BUILD"},
				"provision-lab": {Plan: "PROJ-PROV", Branch: "develop",
					Defaults: config.StringMap{"cluster_type": "k8s", "compute_nodes": "2", "db_password": "${LAB_DB_PASSWORD}"},
					Options:  config.StringListMap{"cluster_type": {"k8s", "dcos"}},
					Required: []string{"cluster_name"},
					Watch:    &yes},
			}},
		Machine: &config.MachineFile{Version: 1},
	}
}

func fakeBamboo() *fake.Provider {
	return &fake.Provider{
		BaseURL: "https://bamboo.example.com",
		Plans: map[string][]provider.Plan{"PROJ": {
			{Key: "PROJ-BUILD", Name: "Build and test", ProjectKey: "PROJ"},
			{Key: "PROJ-PROV", Name: "Provision lab", ProjectKey: "PROJ"},
		}},
		Branches: map[string][]provider.Branch{"PROJ-PROV": {
			{Key: "PROJ-PROV12", Name: "Provision lab - develop", ShortName: "develop", PlanKey: "PROJ-PROV"},
			{Key: "PROJ-PROV7", Name: "Provision lab - feat/foo", ShortName: "feat/foo", PlanKey: "PROJ-PROV"},
		}},
		Variables: map[string][]provider.Variable{"PROJ-PROV": {
			{Name: "cluster_type", Value: "k8s"},
			{Name: "compute_nodes", Value: "1"},
			{Name: "cluster_name", Value: ""},
			{Name: "db_password", Value: "********", Masked: true},
		}},
		History: map[string][]provider.Build{
			"PROJ-BUILD": {{Key: "PROJ-BUILD-482", Number: 482, State: provider.StateSuccess, Reason: "Scheduled"}},
			"PROJ-PROV12": {
				{Key: "PROJ-PROV12-9", Number: 9, State: provider.StateFailed, Reason: "Scheduled"},
				{Key: "PROJ-PROV12-8", Number: 8, State: provider.StateSuccess, Reason: "Manual run by jdoe"},
			},
		},
		BuildVars: map[string]map[string]string{"PROJ-PROV12-8": {
			"cluster_type": "dcos", "cluster_name": "beta", "compute_nodes": "3", "db_password": "********", "global_flag": "x",
		}},
	}
}

func newService(t *testing.T, p *fake.Provider) *Service {
	t.Helper()
	root := t.TempDir()
	return &Service{
		P:      p,
		Cfg:    testConfig(root),
		Server: config.ResolvedServer{Alias: "work", URL: "https://bamboo.example.com"},
		Origin: "https://bamboo.example.com",
		Clock:  newClock(),
		State:  &StateStore{Path: filepath.Join(t.TempDir(), "state.json")},
		Sets: &SetStore{Dir: filepath.Join(t.TempDir(), "sets"),
			Host: func() (string, error) { return "laptop", nil }, PID: func() int { return 4242 }},
		Getenv: func(string) string { return "" },
	}
}
