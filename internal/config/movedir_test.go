package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMoveDir(t *testing.T) {
	base := t.TempDir()
	from, to := filepath.Join(base, "old"), filepath.Join(base, "new")
	require.NoError(t, os.MkdirAll(from, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(from, "config.yaml"), []byte("servers: {}\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(from, "credentials.yaml"), []byte("tokens: {}\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(from, "state.json"), []byte("{}"), 0o600))

	moved, err := MoveDir(from, to)
	require.NoError(t, err)
	assert.Equal(t, []string{"config.yaml", "credentials.yaml"}, moved)

	b, err := os.ReadFile(filepath.Join(to, "config.yaml"))
	require.NoError(t, err)
	assert.Equal(t, "servers: {}\n", string(b))
	fi, err := os.Stat(filepath.Join(to, "credentials.yaml"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
	di, err := os.Stat(to)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), di.Mode().Perm())

	assert.NoFileExists(t, filepath.Join(from, "config.yaml"))
	assert.NoFileExists(t, filepath.Join(from, "credentials.yaml"))
	// Files that are not config stay where they are.
	assert.FileExists(t, filepath.Join(from, "state.json"))
	assert.NoFileExists(t, filepath.Join(to, "state.json"))

	// A second run finds nothing to move.
	moved, err = MoveDir(from, to)
	require.NoError(t, err)
	assert.Empty(t, moved)
}

func TestMoveDirKeepsExistingFiles(t *testing.T) {
	base := t.TempDir()
	from, to := filepath.Join(base, "old"), filepath.Join(base, "new")
	require.NoError(t, os.MkdirAll(from, 0o700))
	require.NoError(t, os.MkdirAll(to, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(from, "config.yaml"), []byte("old\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(to, "config.yaml"), []byte("new\n"), 0o644))

	moved, err := MoveDir(from, to)
	require.NoError(t, err)
	assert.Empty(t, moved)
	b, err := os.ReadFile(filepath.Join(to, "config.yaml"))
	require.NoError(t, err)
	assert.Equal(t, "new\n", string(b))
	assert.FileExists(t, filepath.Join(from, "config.yaml"))
}

func TestMoveDirSameOrMissing(t *testing.T) {
	base := t.TempDir()
	moved, err := MoveDir(base, base)
	require.NoError(t, err)
	assert.Empty(t, moved)

	moved, err = MoveDir(filepath.Join(base, "absent"), filepath.Join(base, "new"))
	require.NoError(t, err)
	assert.Empty(t, moved)
	assert.NoDirExists(t, filepath.Join(base, "new"))
}

func TestMoveDirLeavesFilesAnOverrideNames(t *testing.T) {
	base := t.TempDir()
	from, to := filepath.Join(base, "old"), filepath.Join(base, "new")
	require.NoError(t, os.MkdirAll(from, 0o700))
	for _, name := range []string{"config.yaml", "credentials.yaml", "fixture-denylist.txt"} {
		require.NoError(t, os.WriteFile(filepath.Join(from, name), []byte("x\n"), 0o600))
	}
	// An override may reach the file through another path, such as a symlink.
	link := filepath.Join(base, "link.txt")
	require.NoError(t, os.Symlink(filepath.Join(from, "fixture-denylist.txt"), link))

	moved, err := MoveDir(from, to, filepath.Join(from, "config.yaml"), link, "", filepath.Join(base, "absent"))
	require.NoError(t, err)
	assert.Equal(t, []string{"credentials.yaml"}, moved)
	assert.FileExists(t, filepath.Join(from, "config.yaml"))
	assert.FileExists(t, filepath.Join(from, "fixture-denylist.txt"))
	assert.NoFileExists(t, filepath.Join(to, "config.yaml"))
	assert.NoFileExists(t, filepath.Join(to, "fixture-denylist.txt"))
}

// Issue #126: the move must never take files from the working directory,
// which a relative or empty old directory (no home) would point at.
func TestMoveDirIgnoresARelativeSource(t *testing.T) {
	wd, to := t.TempDir(), filepath.Join(t.TempDir(), "new")
	t.Chdir(wd)
	require.NoError(t, os.WriteFile("config.yaml", []byte("planted\n"), 0o644))
	require.NoError(t, os.MkdirAll("bam", 0o700))
	require.NoError(t, os.WriteFile(filepath.Join("bam", "config.yaml"), []byte("planted\n"), 0o644))

	for _, from := range []string{"", "bam"} {
		moved, err := MoveDir(from, to)
		require.NoError(t, err)
		assert.Empty(t, moved, from)
	}
	assert.FileExists(t, filepath.Join(wd, "config.yaml"))
	assert.FileExists(t, filepath.Join(wd, "bam", "config.yaml"))
	assert.NoDirExists(t, to)
}
