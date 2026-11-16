package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rojcarranza/bam/internal/config"
	"github.com/stretchr/testify/require"
)

const liveProject = `version: 1
servers: {work: {url: https://bamboo.example.com}}
default_server: work
targets:
  build: {plan: PROJ-BUILD}
  provision-lab:
    plan: PROJ-PROV
    branch: develop
    defaults: {cluster_type: k8s, compute_nodes: "2"}
`

// liveService is a Service over real files, as the terminal UI has it: the
// files are written after it starts, and the service must see them.
func liveService(t *testing.T) (*Service, string) {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0o755))
	path := filepath.Join(root, ".bam.yaml")
	writeLive(t, path, liveProject)
	src, err := config.NewSource(func() config.LoadOptions {
		return config.LoadOptions{WorkDir: root, Home: root, MachineFile: filepath.Join(root, "machine.yaml"),
			Getenv: func(string) string { return "" }}
	})
	require.NoError(t, err)
	svc := newService(t, fakeBamboo())
	svc.Cfg = src
	return svc, path
}

// writeLive replaces path and moves its modification time on, so the test does
// not depend on the file system's clock resolution.
func writeLive(t *testing.T, path, body string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	at := time.Now().Add(time.Duration(len(body)) * time.Millisecond)
	require.NoError(t, os.Chtimes(path, at, at))
}

// BAM-53 machine 8, defect 1: a set added while the UI is open was listed by
// the panel but running it failed with unknown set.
func TestASetAddedWhileOpenCanBePlanned(t *testing.T) {
	svc, path := liveService(t)
	_, err := svc.PlanSet(context.Background(), "nightly", SetOptions{})
	require.ErrorContains(t, err, `unknown set "nightly"`)

	writeLive(t, path, liveProject+"sets:\n  nightly:\n    stages:\n      - [build]\n")
	p, err := svc.PlanSet(context.Background(), "nightly", SetOptions{})
	require.NoError(t, err)
	require.Equal(t, "PROJ-BUILD", p.Stages[0][0].Ref.PlanKey)
}

// Defect 2: after a preset edit, running the preset and a set that holds it
// sent different variables, because they read different copies.
func TestAPresetEditReachesTheSetsHoldingIt(t *testing.T) {
	svc, path := liveService(t)
	edited := liveProject + "sets:\n  lab:\n    stages:\n      - [provision-lab]\n"
	writeLive(t, path, edited)
	first, err := svc.PlanSet(context.Background(), "lab", SetOptions{})
	require.NoError(t, err)
	n, _ := first.Stages[0][0].Vars.Get("compute_nodes")
	require.Equal(t, "2", n.Value)

	edited2 := replaceOnce(t, edited, `compute_nodes: "2"`, `compute_nodes: "6"`)
	writeLive(t, path, edited2)

	set, err := svc.PlanSet(context.Background(), "lab", SetOptions{})
	require.NoError(t, err)
	got, _ := set.Stages[0][0].Vars.Get("compute_nodes")
	require.Equal(t, "6", got.Value, "the set sees the edited preset")

	ref, err := svc.ResolvePlan(context.Background(), "provision-lab", "")
	require.NoError(t, err)
	require.Equal(t, "6", ref.Target.Defaults["compute_nodes"], "the preset alone sees the same edit")
}

// Defect 3: pinning a preset added while the UI is open worked, and the board
// then showed unknown preset until restart.
func TestAPinnedPresetAddedWhileOpenIsKnownToTheBoard(t *testing.T) {
	svc, path := liveService(t)
	writeLive(t, path, replaceOnce(t, liveProject, "targets:\n", "targets:\n  deploy-lab: {plan: PROJ-BUILD}\n"))
	svc.P = boardBamboo(svc.Clock.Now())
	b, err := svc.Board(context.Background(), pins("integration", "deploy-lab"))
	require.NoError(t, err)
	pin := b.Groups[0].Pins[0]
	require.NoError(t, pin.Err)
	require.Equal(t, "PROJ-BUILD", pin.PlanKey)
}

func TestSourceReloadsAfterBamsOwnWriteWithUnchangedStamp(t *testing.T) {
	svc, path := liveService(t)
	st, err := os.Stat(path)
	require.NoError(t, err)
	body := replaceOnce(t, liveProject, "branch: develop", "branch: develoq")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	require.NoError(t, os.Chtimes(path, st.ModTime(), st.ModTime()))
	svc.Cfg.MarkStale()
	cfg := svc.Config()
	tg, ok, err := cfg.Target("provision-lab")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "develoq", tg.Branch)
}

func TestSourceKeepsTheLastGoodConfigWhenAFileIsBroken(t *testing.T) {
	svc, path := liveService(t)
	writeLive(t, path, "version: 1\ntargets: [unbalanced\n")
	_, err := svc.Cfg.Current()
	require.Error(t, err)
	cfg := svc.Config()
	require.NotNil(t, cfg, "the UI keeps working on the last good configuration")
	_, ok, _ := cfg.Target("provision-lab")
	require.True(t, ok)
}

func replaceOnce(t *testing.T, s, old, repl string) string {
	t.Helper()
	require.Contains(t, s, old)
	return strings.Replace(s, old, repl, 1)
}

// A save that no longer parses must stop a run, not send the last good values.
func TestARunIsRefusedWhileTheConfigFileDoesNotParse(t *testing.T) {
	svc, path := liveService(t)
	writeLive(t, path, "version: 1\ntargets: [unbalanced\n")
	_, err := svc.ResolvePlanToRun(context.Background(), "provision-lab", "")
	require.Error(t, err)
	_, err = svc.ResolvePlan(context.Background(), "PROJ-BUILD", "")
	require.NoError(t, err, "browsing a plan key keeps working on the last good copy")
	_, err = svc.PlanSet(context.Background(), "nightly", SetOptions{})
	require.Error(t, err)
	require.NotContains(t, err.Error(), "unknown set", "the parse error is shown, not a missing set")
}
