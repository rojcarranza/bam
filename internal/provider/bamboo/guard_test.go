package bamboo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrg/xdg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFixtureGuard fails when any fixture names a host outside the allowlist.
// make check-fixtures runs only this test; the no-mistakes gate runs it with everything else.
func TestFixtureGuard(t *testing.T) {
	v, err := FixtureHostViolations("testdata")
	require.NoError(t, err)
	require.Empty(t, v, "fixtures must use placeholder hosts:\n%s", strings.Join(v, "\n"))

	// Load denylist and check for private terms
	path := DenylistPath(os.Getenv)
	terms, err := LoadDenylist(path)
	require.NoError(t, err)
	if len(terms) == 0 {
		// Older bam versions kept the denylist in adrg/xdg's config home and,
		// on Windows, in os.UserConfigDir.
		oldDirs := []string{filepath.Join(xdg.ConfigHome, "bam")}
		if dir, err := os.UserConfigDir(); err == nil {
			oldDirs = append(oldDirs, filepath.Join(dir, "bam"))
		}
		stranded := strandedDenylist(os.Getenv, oldDirs...)
		require.Empty(t, stranded, "the fixture denylist is still in an old location; move it to %s", path)
		t.Log("no fixture denylist; private-term check skipped")
		return
	}

	violations, err := FixtureTermViolations("testdata", terms)
	require.NoError(t, err)
	require.Empty(t, violations, "fixtures contain private terms:\n%s", strings.Join(violations, "\n"))
}

// strandedDenylist returns a fixture denylist still in one of oldDirs, the
// directories older bam versions kept it in, when DenylistPath has none and
// BAM_FIXTURE_DENYLIST names none. The private-term check must not read such a
// state as "no denylist". It returns "" otherwise.
func strandedDenylist(getenv func(string) string, oldDirs ...string) string {
	if getenv("BAM_FIXTURE_DENYLIST") != "" {
		return ""
	}
	if _, err := os.Stat(DenylistPath(getenv)); err == nil {
		return ""
	}
	for _, dir := range oldDirs {
		old := filepath.Join(dir, denylistFile)
		if _, err := os.Stat(old); err == nil {
			return old
		}
	}
	return ""
}

func TestStrandedDenylist(t *testing.T) {
	base := t.TempDir()
	oldDir, newDir := filepath.Join(base, "old"), filepath.Join(base, "xdg", "bam")
	vars := map[string]string{"XDG_CONFIG_HOME": filepath.Join(base, "xdg")}
	getenv := func(k string) string { return vars[k] }
	require.NoError(t, os.MkdirAll(oldDir, 0o700))
	require.NoError(t, os.MkdirAll(newDir, 0o700))

	assert.Empty(t, strandedDenylist(getenv, oldDir), "no denylist anywhere")

	old := filepath.Join(oldDir, "fixture-denylist.txt")
	require.NoError(t, os.WriteFile(old, []byte("term\n"), 0o600))
	assert.Equal(t, old, strandedDenylist(getenv, filepath.Join(base, "absent"), oldDir))

	vars["BAM_FIXTURE_DENYLIST"] = filepath.Join(base, "override.txt")
	assert.Empty(t, strandedDenylist(getenv, oldDir), "an override names the denylist to use")
	delete(vars, "BAM_FIXTURE_DENYLIST")

	require.NoError(t, os.WriteFile(filepath.Join(newDir, "fixture-denylist.txt"), []byte("term\n"), 0o600))
	assert.Empty(t, strandedDenylist(getenv, oldDir), "the current path has one")
}
