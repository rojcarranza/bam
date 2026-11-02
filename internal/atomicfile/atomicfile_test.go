package atomicfile

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteCreatesDirAndReplaces(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a", "b", "f.json")
	require.NoError(t, Write(path, []byte("one"), 0o600))
	require.NoError(t, Write(path, []byte("two"), 0o600))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "two", string(data))

	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	assert.Len(t, entries, 1, "no temporary file is left behind")

	if runtime.GOOS != "windows" {
		st, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), st.Mode().Perm())
		dir, err := os.Stat(filepath.Dir(path))
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o700), dir.Mode().Perm())
	}
}

func TestWritePermIgnoresUmask(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no unix permissions")
	}
	path := filepath.Join(t.TempDir(), "f")
	require.NoError(t, Write(path, []byte("x"), 0o644))
	st, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o644), st.Mode().Perm())
}

func TestWriteFailureLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "target")
	require.NoError(t, os.Mkdir(path, 0o700)) // renaming a file over a directory fails
	require.NoError(t, os.WriteFile(filepath.Join(path, "keep"), nil, 0o600))

	assert.Error(t, Write(path, []byte("x"), 0o600))
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "only the directory remains")
}
