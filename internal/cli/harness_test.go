package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/provider/bamboo"
	"github.com/r0jjames/bam-cli/internal/provider/fake"
	"github.com/r0jjames/bam-cli/internal/view/tui"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

const workOrigin = "https://bamboo.example.com"

const projectYAML = `version: 1
servers:
  work:
    url: https://bamboo.example.com
default_server: work
projects: [PROJ]
targets:
  build:
    plan: PROJ-BUILD
  provision-lab:
    plan: PROJ-PROV
    branch: develop
    defaults:
      cluster_type: k8s
    options:
      cluster_type: [k8s, dcos]
    required: [cluster_name]
`

type memKeyring struct {
	mu sync.Mutex
	m  map[string]string
}

func (k *memKeyring) Get(s, u string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	v, ok := k.m[s+"|"+u]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return v, nil
}

func (k *memKeyring) Set(s, u, p string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.m[s+"|"+u] = p
	return nil
}

func (k *memKeyring) Delete(s, u string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if _, ok := k.m[s+"|"+u]; !ok {
		return keyring.ErrNotFound
	}
	delete(k.m, s+"|"+u)
	return nil
}

type instantClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *instantClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *instantClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	ch := make(chan time.Time, 1)
	ch <- c.now
	return ch
}

type fakeBackend struct {
	*fake.Provider
	probe []bamboo.ProbeResult
}

func (f fakeBackend) Probe(context.Context, string) []bamboo.ProbeResult { return f.probe }

type harness struct {
	t           *testing.T
	root, home  string
	env         Env
	stdout      *bytes.Buffer
	stderr      *bytes.Buffer
	fake        *fake.Provider
	kr          *memKeyring
	vars        map[string]string
	secret      string
	stdin       string
	tty         bool
	opened      []string
	probe       []bamboo.ProbeResult
	connectOpts []bamboo.Options
	tuiRuns     []tui.Deps
	// editorReplies answers the editor's Nth opening with the Nth function;
	// editorSeen and editorCmds record every buffer and command it was given.
	editorReplies []func(in string) (string, error)
	editorSeen    []string
	editorCmds    []string
}

var fixedNow = time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)

func sampleFake() *fake.Provider {
	return &fake.Provider{
		BaseURL:  workOrigin,
		User:     provider.User{Name: "jdoe", FullName: "J Doe"},
		Info:     provider.ServerInfo{Version: "9.6.2"},
		Projects: []provider.Project{{Key: "PROJ", Name: "Example Project", URL: workOrigin + "/browse/PROJ"}},
		Plans: map[string][]provider.Plan{"PROJ": {
			{Key: "PROJ-BUILD", Name: "Build and test", ProjectKey: "PROJ", URL: workOrigin + "/browse/PROJ-BUILD",
				LastBuild: &provider.BuildSummary{Key: "PROJ-BUILD-482", Number: 482, State: provider.StateSuccess, FinishedAt: fixedNow.Add(-12 * time.Minute), Reason: "Manual run by jdoe"}},
			{Key: "PROJ-PROV", Name: "Provision lab", ProjectKey: "PROJ", URL: workOrigin + "/browse/PROJ-PROV"},
		}},
		Branches: map[string][]provider.Branch{"PROJ-PROV": {{Key: "PROJ-PROV12", Name: "Provision lab - develop", ShortName: "develop", PlanKey: "PROJ-PROV"}}},
		Variables: map[string][]provider.Variable{
			"PROJ-PROV":  {{Name: "cluster_type", Value: "k8s"}, {Name: "cluster_name", Value: ""}},
			"PROJ-BUILD": {},
		},
		History: map[string][]provider.Build{
			"PROJ-BUILD": {{Key: "PROJ-BUILD-482", URL: workOrigin + "/browse/PROJ-BUILD-482", PlanKey: "PROJ-BUILD", Number: 482, State: provider.StateSuccess, Reason: "Manual run by jdoe", StartedAt: fixedNow.Add(-14 * time.Minute), Duration: 131 * time.Second}},
			"PROJ-PROV12": {{Key: "PROJ-PROV12-8", URL: workOrigin + "/browse/PROJ-PROV12-8", PlanKey: "PROJ-PROV12", Number: 8, State: provider.StateFailed, Reason: "Manual run by jdoe",
				Stages: []provider.Stage{{Name: "Apply", State: provider.StateFailed, Jobs: []provider.Job{{Key: "PROJ-PROV12-TF-8", Name: "Terraform", State: provider.StateFailed}}}}}},
		},
		BuildVars: map[string]map[string]string{"PROJ-PROV12-8": {"cluster_type": "dcos", "cluster_name": "beta"}},
		Logs:      map[string][]string{"PROJ-PROV12-TF-8": {"terraform apply", "Error: quota exceeded"}},
	}
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	base := t.TempDir()
	home := filepath.Join(base, "home")
	root := filepath.Join(home, "src", "repo")
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".git"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".bam.yaml"), []byte(projectYAML), 0o644))

	h := &harness{t: t, root: root, home: home, stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{},
		fake: sampleFake(), kr: &memKeyring{m: map[string]string{"bam|" + workOrigin: "tok"}},
		// A real terminal sets TERM; the UI gate reads it, so the harness
		// supplies one and the dumb-terminal test overrides it.
		vars: map[string]string{"TERM": "xterm-256color"}}
	cfgDir := filepath.Join(home, ".config", "bam")
	h.env = Env{
		Stdout:  h.stdout,
		Stderr:  h.stderr,
		Getenv:  func(k string) string { return h.vars[k] },
		Width:   func() int { return 100 },
		WorkDir: root,
		Home:    home,
		Paths: Paths{
			MachineConfig: filepath.Join(cfgDir, "config.yaml"),
			Credentials:   filepath.Join(cfgDir, "credentials.yaml"),
			Capabilities:  filepath.Join(home, ".cache", "bam", "capabilities.json"),
			State:         filepath.Join(home, ".local", "state", "bam", "state.json"),
			Sets:          filepath.Join(home, ".local", "state", "bam", "sets"),
		},
		Clock:   &instantClock{now: fixedNow},
		Keyring: h.kr,
		Connect: func(o bamboo.Options) (Backend, error) {
			h.connectOpts = append(h.connectOpts, o)
			return fakeBackend{Provider: h.fake, probe: h.probe}, nil
		},
		OpenBrowser: func(u string) error { h.opened = append(h.opened, u); return nil },
		RunPager:    func(_ string, r io.Reader) error { _, err := io.Copy(h.stdout, r); return err },
		RunEditor: func(cmd string, in []byte) ([]byte, error) {
			h.editorCmds = append(h.editorCmds, cmd)
			h.editorSeen = append(h.editorSeen, string(in))
			if len(h.editorReplies) == 0 {
				return nil, errors.New("harness: the editor opened more often than the test scripted")
			}
			next := h.editorReplies[0]
			h.editorReplies = h.editorReplies[1:]
			out, err := next(string(in))
			return []byte(out), err
		},
		ReadSecret: func() (string, error) { return h.secret, nil },
		RunTUI:     func(_ context.Context, d tui.Deps) error { h.tuiRuns = append(h.tuiRuns, d); return nil },
		GOOS:       "linux",
	}
	return h
}

// run executes bam with args and returns the exit code. Output is in stdout/stderr.
func (h *harness) run(args ...string) int {
	h.stdout.Reset()
	h.stderr.Reset()
	env := h.env
	env.Stdin = strings.NewReader(h.stdin)
	env.StdoutTTY = h.tty
	env.StdinTTY = h.tty
	env.StderrTTY = h.tty
	return Execute(context.Background(), args, env)
}

// runCtx is run with a caller-supplied context, e.g. an already cancelled one.
func (h *harness) runCtx(ctx context.Context, args ...string) int {
	h.stdout.Reset()
	h.stderr.Reset()
	env := h.env
	env.Stdin = strings.NewReader(h.stdin)
	env.StdoutTTY = h.tty
	env.StdinTTY = h.tty
	env.StderrTTY = h.tty
	return Execute(ctx, args, env)
}
