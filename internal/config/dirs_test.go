package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDirsDefaultToXDGLayoutOnEveryOS(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "windows"} {
		home := t.TempDir()
		d := LocateDirs(home, goos, envOf(nil))
		assert.Equal(t, filepath.Join(home, ".config", "bam"), d.Config, goos)
		assert.Equal(t, filepath.Join(home, ".cache", "bam"), d.Cache, goos)
		assert.Equal(t, filepath.Join(home, ".local", "state", "bam"), d.State, goos)
	}
}

func TestDirsHonourXDGVariables(t *testing.T) {
	home := t.TempDir()
	d := LocateDirs(home, "darwin", envOf(map[string]string{
		"XDG_CONFIG_HOME": "/x/config",
		"XDG_CACHE_HOME":  "/x/cache",
		"XDG_STATE_HOME":  "/x/state",
	}))
	assert.Equal(t, Dirs{Config: "/x/config/bam", Cache: "/x/cache/bam", State: "/x/state/bam"}, d)
}

func TestDirsIgnoreRelativeXDGVariables(t *testing.T) {
	home := t.TempDir()
	d := LocateDirs(home, "linux", envOf(map[string]string{"XDG_CONFIG_HOME": "rel"}))
	assert.Equal(t, filepath.Join(home, ".config", "bam"), d.Config)
}

func TestDirsKeepUsingAnExistingMacOSLocation(t *testing.T) {
	home := t.TempDir()
	legacyConfig := filepath.Join(home, "Library", "Application Support", "bam")
	legacyCache := filepath.Join(home, "Library", "Caches", "bam")
	require.NoError(t, os.MkdirAll(legacyConfig, 0o700))
	require.NoError(t, os.MkdirAll(legacyCache, 0o700))

	d := LocateDirs(home, "darwin", envOf(nil))
	assert.Equal(t, Dirs{Config: legacyConfig, Cache: legacyCache, State: legacyConfig}, d)

	// Linux never looks there.
	d = LocateDirs(home, "linux", envOf(nil))
	assert.Equal(t, filepath.Join(home, ".config", "bam"), d.Config)
}

func TestDirsPreferTheNewLocationOnceItExists(t *testing.T) {
	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, "Library", "Application Support", "bam"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".config", "bam"), 0o700))
	d := LocateDirs(home, "darwin", envOf(nil))
	assert.Equal(t, filepath.Join(home, ".config", "bam"), d.Config)
}

func TestDirsKeepUsingAnExistingWindowsLocation(t *testing.T) {
	home := t.TempDir()
	local := filepath.Join(home, "AppData", "Local")
	require.NoError(t, os.MkdirAll(filepath.Join(local, "bam"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(local, "cache", "bam"), 0o700))
	d := LocateDirs(home, "windows", envOf(map[string]string{"LOCALAPPDATA": local}))
	assert.Equal(t, Dirs{
		Config: filepath.Join(local, "bam"),
		Cache:  filepath.Join(local, "cache", "bam"),
		State:  filepath.Join(local, "bam"),
	}, d)
}
