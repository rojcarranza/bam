package view

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rojcarranza/bam/internal/view/style"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var update = flag.Bool("update", false, "rewrite golden files")

var fixedNow = time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)

// testOut returns an Out writing to a buffer: TTY layout, color off, 100 columns.
func testOut(tty bool) (Out, *bytes.Buffer) {
	var buf bytes.Buffer
	return Out{W: &buf, TTY: tty, Width: 100, Style: style.Mode{}, Now: func() time.Time { return fixedNow }}, &buf
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		require.NoError(t, os.MkdirAll("testdata", 0o755))
		require.NoError(t, os.WriteFile(path, []byte(got), 0o644))
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err, "run: go test ./internal/view -update, then review the golden file")
	assert.Equal(t, string(want), got)
}
