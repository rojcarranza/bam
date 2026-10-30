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
