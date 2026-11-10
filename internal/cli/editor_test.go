package cli

import (
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/rojcarranza/bam/internal/errs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubEditor writes a shell script that records its arguments, the file's
// listing and its content into dir, then runs body. $f is the file to edit.
func stubEditor(t *testing.T, body string) (cmd, dir string) {
	t.Helper()
	if goruntime.GOOS == "windows" {
		t.Skip("the stub editor is a shell script")
	}
	dir = t.TempDir()
	script := fmt.Sprintf(`#!/bin/sh
for f; do :; done
echo "$@" > %[1]s/args
ls -l "$f" > %[1]s/ls
cp "$f" %[1]s/seen
%[2]s
`, dir, body)
	cmd = filepath.Join(dir, "editor")
	require.NoError(t, os.WriteFile(cmd, []byte(script), 0o755))
	return cmd, dir
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(b)
}

func TestRunEditorReturnsWhatWasSaved(t *testing.T) {
	cmd, dir := stubEditor(t, `printf 'a=2\n' > "$f"`)
	got, err := runEditor(cmd+" --wait", []byte("a=1\n"))
	require.NoError(t, err)
	assert.Equal(t, "a=2\n", string(got))
	assert.Equal(t, "a=1\n", read(t, filepath.Join(dir, "seen")), "the editor saw the initial text")
	assert.True(t, strings.HasPrefix(read(t, filepath.Join(dir, "ls")), "-rw-------"), "the file is private: it may hold a secret")

	args := strings.Fields(read(t, filepath.Join(dir, "args")))
	require.Len(t, args, 2)
	assert.Equal(t, "--wait", args[0], "arguments of the editor command are kept")
	assert.True(t, strings.HasSuffix(args[1], ".env"))
	assert.True(t, strings.HasPrefix(filepath.Base(args[1]), "bam-run-"))
	_, err = os.Stat(args[1])
	assert.True(t, os.IsNotExist(err), "the temp file is removed")
}

func TestRunEditorCancelled(t *testing.T) {
	cmd, dir := stubEditor(t, `printf 'secret=typed\n' > "$f"; exit 1`)
	_, err := runEditor(cmd, []byte("a=1\n"))
	require.ErrorIs(t, err, errEditorCancelled)
	path := strings.TrimSpace(read(t, filepath.Join(dir, "args")))
	_, statErr := os.Stat(path)
	assert.True(t, os.IsNotExist(statErr), "the temp file is removed on cancel too")
}

func TestRunEditorNotFound(t *testing.T) {
	pattern := filepath.Join(os.TempDir(), "bam-run-*.env")
	before, err := filepath.Glob(pattern)
	require.NoError(t, err)

	for _, cmd := range []string{"bam-no-such-editor-xyz", filepath.Join(t.TempDir(), "missing")} {
		_, err := runEditor(cmd, []byte("a=1\n"))
		require.Error(t, err, cmd)
		assert.Equal(t, errs.KindConfig, errs.KindOf(err), cmd)
		var e *errs.Error
		require.ErrorAs(t, err, &e)
		assert.Equal(t, "set BAM_EDITOR or EDITOR", e.Try)
	}

	after, err := filepath.Glob(pattern)
	require.NoError(t, err)
	assert.Equal(t, before, after, "the temp file is removed even when the editor is not found")
}
