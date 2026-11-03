package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecuteMovesConfigFromTheOldDirectory(t *testing.T) {
	h := newHarness(t)
	legacy := filepath.Join(h.home, "Library", "Application Support", "bam")
	h.env.Paths.LegacyConfigDir = legacy
	require.NoError(t, os.MkdirAll(legacy, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(legacy, "config.yaml"), []byte(
		"version: 1\nservers:\n  home:\n    url: http://bamboo.lab.example:8085\n"), 0o644))

	assert.Equal(t, 0, h.run("server", "list"))
	assert.Regexp(t, `home\s+http://bamboo.lab.example:8085\s+machine`, h.stdout.String())
	assert.Equal(t, "moved config.yaml from "+legacy+" to "+filepath.Dir(h.env.Paths.MachineConfig)+"\n", h.stderr.String())
	assert.FileExists(t, h.env.Paths.MachineConfig)
	assert.NoFileExists(t, filepath.Join(legacy, "config.yaml"))

	assert.Equal(t, 0, h.run("server", "list"))
	assert.Empty(t, h.stderr.String(), "the move is reported once")
}

func TestExecuteLeavesFilesAnOverrideNames(t *testing.T) {
	h := newHarness(t)
	legacy := filepath.Join(h.home, "Library", "Application Support", "bam")
	h.env.Paths.LegacyConfigDir = legacy
	require.NoError(t, os.MkdirAll(legacy, 0o700))
	h.env.Paths.MachineConfig = filepath.Join(legacy, "config.yaml")
	require.NoError(t, os.WriteFile(h.env.Paths.MachineConfig, []byte(
		"version: 1\nservers:\n  home:\n    url: http://bamboo.lab.example:8085\n"), 0o644))
	denylist := filepath.Join(legacy, "fixture-denylist.txt")
	require.NoError(t, os.WriteFile(denylist, []byte("term\n"), 0o600))
	h.vars["BAM_FIXTURE_DENYLIST"] = denylist

	assert.Equal(t, 0, h.run("server", "list"))
	assert.Regexp(t, `home\s+http://bamboo.lab.example:8085\s+machine`, h.stdout.String())
	assert.Empty(t, h.stderr.String())
	assert.FileExists(t, h.env.Paths.MachineConfig)
	assert.FileExists(t, denylist)
}

func TestSystemEnvConfigPathsAreAbsoluteWithoutHome(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("BAM_CONFIG", "")

	env := SystemEnv()
	assert.True(t, filepath.IsAbs(env.Paths.MachineConfig), env.Paths.MachineConfig)
	assert.True(t, filepath.IsAbs(env.Paths.Credentials), env.Paths.Credentials)
}

// Issue #126: an upgrade keeps its servers and token when the move fails;
// bam warns and reads the files it could not move from the old directory.
func TestExecuteReadsTheOldConfigWhenTheMoveFails(t *testing.T) {
	h := newHarness(t)
	legacy := filepath.Join(h.home, "Library", "Application Support", "bam")
	h.env.Paths.LegacyConfigDir = legacy
	require.NoError(t, os.MkdirAll(legacy, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(legacy, "config.yaml"), []byte(
		"version: 1\nservers:\n  home:\n    url: http://bamboo.lab.example:8085\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(legacy, "credentials.yaml"), []byte(
		"http://bamboo.lab.example:8085: tok-home\n"), 0o600))
	// ~/.config is a file, so ~/.config/bam cannot be made.
	require.NoError(t, os.WriteFile(filepath.Join(h.home, ".config"), nil, 0o644))

	assert.Equal(t, 0, h.run("server", "list"))
	assert.Regexp(t, `home\s+http://bamboo.lab.example:8085\s+machine\s+yes`, h.stdout.String())
	stderr := h.stderr.String()
	assert.Contains(t, stderr, "warning: could not move config from "+legacy+": ")
	assert.Contains(t, stderr, "reading config.yaml from "+legacy+"\n")
	assert.Contains(t, stderr, "reading credentials.yaml from "+legacy+"\n")
	assert.FileExists(t, filepath.Join(legacy, "config.yaml"))
	assert.FileExists(t, filepath.Join(legacy, "credentials.yaml"))
}
