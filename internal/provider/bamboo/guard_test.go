package bamboo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrg/xdg"
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
		stranded := StrandedDenylist(os.Getenv, oldDirs...)
		require.Empty(t, stranded, "the fixture denylist is still in an old location; move it to %s", path)
		t.Log("no fixture denylist; private-term check skipped")
		return
	}

	violations, err := FixtureTermViolations("testdata", terms)
	require.NoError(t, err)
	require.Empty(t, violations, "fixtures contain private terms:\n%s", strings.Join(violations, "\n"))
}
