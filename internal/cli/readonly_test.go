package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rojcarranza/bam/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The read-only switch is the captain's ask: "refuse every command that
// changes anything in Bamboo", set by flag, environment or settings file.

// writeCommands is every bam command line that changes something in Bamboo.
// bam agent disable and enable join it when they merge.
var writeCommands = map[string][]string{
	"run a preset":      {"run", "build"},
	"run a plan":        {"run", "PROJ-BUILD", "--var", "x=1"},
	"build run":         {"build", "run", "PROJ-BUILD"},
	"cancel":            {"cancel", "PROJ-BUILD-483"},
	"build cancel":      {"build", "cancel", "PROJ-BUILD-483"},
	"build rerun":       {"build", "rerun", "PROJ-PROV-9"},
	"build tree rerun":  {"build", "tree", "PROJ-BUILD-482", "--rerun-failed", "--yes"},
	"set run":           {"set", "run", "release"},
	"set run in a file": {"set", "run", "-f", "train.yaml"},
	"set resume":        {"set", "resume"},
	"set rerun":         {"set", "rerun", "--failed"},
}

// readOnlyOn turns the switch on one way: "flag", "env" or "config".
func readOnlyOn(t *testing.T, h *harness, how string) []string {
	t.Helper()
	switch how {
	case "flag":
		return []string{"--read-only"}
	case "env":
		h.vars["BAM_READ_ONLY"] = "1"
	case "config":
		path := filepath.Join(h.home, ".config", "bam", "config.yaml")
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte("version: 1\nread_only: true\n"), 0o644))
	}
	return nil
}

func untouched(t *testing.T, h *harness) {
	t.Helper()
	assert.Empty(t, h.fake.Triggered, "nothing was triggered")
	assert.Empty(t, h.fake.Stopped, "nothing was stopped")
	assert.Empty(t, h.fake.Restarted, "nothing was restarted")
}

func TestReadOnlyRefusesEveryWriteCommand(t *testing.T) {
	for _, how := range []string{"flag", "env", "config"} {
		for name, cmd := range writeCommands {
			t.Run(how+"/"+name, func(t *testing.T) {
				h := treeHarness(t, provider.StateFailed)
				require.NoError(t, os.WriteFile(filepath.Join(h.root, ".bam.yaml"), []byte(setYAML+"\n"), 0o644))
				require.NoError(t, os.WriteFile(filepath.Join(h.root, "train.yaml"), []byte("name: t\nstages:\n  - [build]\n"), 0o644))
				h.fake.History["PROJ-BUILD"] = append(h.fake.History["PROJ-BUILD"],
					provider.Build{Key: "PROJ-BUILD-483", PlanKey: "PROJ-BUILD", Number: 483, State: provider.StateRunning})
				extra := readOnlyOn(t, h, how)
				code := h.run(append(cmd, extra...)...)
				assert.Equal(t, 2, code, "stderr: %s", h.stderr.String())
				assert.Contains(t, h.stderr.String(), "read-only mode: refused to")
				assert.Contains(t, h.stderr.String(), "will not change anything in Bamboo")
				assert.Empty(t, h.stdout.String())
				untouched(t, h)
			})
		}
	}
}

func TestReadOnlyRefusalIsInJSONMode(t *testing.T) {
	h := newHarness(t)
	assert.Equal(t, 2, h.run("run", "build", "--read-only", "--json"))
	assert.Contains(t, h.stderr.String(), "read-only mode")
	untouched(t, h)
}

func TestReadOnlyStillReadsAndDryRuns(t *testing.T) {
	h := newHarness(t)
	for _, cmd := range [][]string{
		{"plan", "list"}, {"plan", "show", "PROJ-BUILD"}, {"build", "list", "PROJ-BUILD"},
		{"build", "show", "PROJ-BUILD-482"}, {"logs", "PROJ-PROV12-8", "--failed"}, {"queue"},
		{"run", "build", "--dry-run"}, {"build", "cancel", "PROJ-BUILD-482", "--dry-run"},
		{"set", "run", "-f", "none", "--dry-run"},
	} {
		code := h.run(append(cmd, "--read-only")...)
		assert.NotContains(t, h.stderr.String(), "read-only mode", "%v", cmd)
		if cmd[0] != "set" && cmd[0] != "logs" {
			assert.Equal(t, 0, code, "%v: %s", cmd, h.stderr.String())
		}
	}
	untouched(t, h)
}

func TestWithoutTheSwitchWritesStillWork(t *testing.T) {
	h := newHarness(t)
	h.fake.TriggerResults = map[string]provider.Build{"PROJ-BUILD": {Key: "PROJ-BUILD-484", PlanKey: "PROJ-BUILD", State: provider.StateQueued}}
	h.vars["BAM_READ_ONLY"] = "0"
	require.Equal(t, 0, h.run("run", "build"), h.stderr.String())
	assert.Len(t, h.fake.Triggered, 1)
}

func TestReadOnlyEnvMustBeABool(t *testing.T) {
	h := newHarness(t)
	h.vars["BAM_READ_ONLY"] = "maybe"
	assert.Equal(t, 2, h.run("run", "build"))
	assert.Contains(t, h.stderr.String(), `BAM_READ_ONLY is "maybe"`)
	untouched(t, h)
}

// The commands ask first, but the backend refuses on its own too: a command
// that forgot the check still cannot reach the server.
func TestReadOnlyBackendRefusesWhatACommandForgot(t *testing.T) {
	h := newHarness(t)
	r := &runtime{env: h.env}
	r.flags.readOnly = true
	svc, _, err := r.connect(t.Context(), "")
	require.NoError(t, err)
	_, err = svc.P.Trigger(t.Context(), provider.TriggerRequest{PlanKey: "PROJ-BUILD"})
	require.ErrorContains(t, err, "read-only mode")
	require.ErrorContains(t, svc.P.StopBuild(t.Context(), "PROJ-BUILD-483"), "read-only mode")
	_, err = svc.P.RestartBuild(t.Context(), "PROJ-PROV-9")
	require.ErrorContains(t, err, "read-only mode")
	untouched(t, h)
}
