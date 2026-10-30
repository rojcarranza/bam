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
