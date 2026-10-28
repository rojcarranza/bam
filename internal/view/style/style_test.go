package style

import (
	"strings"
	"testing"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEveryStateHasADistinctGlyphEvenWithoutColor(t *testing.T) {
	seen := map[string]provider.State{}
	for _, s := range provider.AllStates() {
		g := Glyph(s)
		require.NotEmpty(t, g, s)
		if other, dup := seen[g]; dup && (!isDim(s) || !isDim(other)) {
			t.Fatalf("%s and %s share glyph %q", s, other, g)
		}
		seen[g] = s
		plain := State(Mode{}, s)
		assert.True(t, strings.HasPrefix(plain, g+" "), "%s renders %q", s, plain)
		assert.NotContains(t, plain, "\x1b", "no escapes with color off")
	}
}

func isDim(s provider.State) bool { return s == provider.StateNotBuilt || s == provider.StateSkipped }

func TestStateWithColor(t *testing.T) {
	out := State(Mode{Color: true}, provider.StateFailed)
	assert.Equal(t, "\x1b[31m✗ failed\x1b[0m", out)
	assert.Equal(t, "– not built", State(Mode{}, provider.StateNotBuilt))
}

func TestLinkAndStrip(t *testing.T) {
	l := Link(Mode{Links: true}, "https://bamboo.example.com/browse/PROJ-BUILD-1", "PROJ-BUILD-1")
	assert.Equal(t, "\x1b]8;;https://bamboo.example.com/browse/PROJ-BUILD-1\x1b\\PROJ-BUILD-1\x1b]8;;\x1b\\", l)
	assert.Equal(t, "PROJ-BUILD-1", Link(Mode{}, "https://bamboo.example.com/x", "PROJ-BUILD-1"))
	assert.Equal(t, "PROJ-BUILD-1", Strip(l))
	colored := Paint(Mode{Color: true}, "32", "✓ success")
	assert.Equal(t, 9, Width(colored))
}

func TestColorEnabled(t *testing.T) {
	cases := []struct {
		flag, noColor string
		tty, want     bool
	}{
		{"auto", "", true, true},
		{"auto", "", false, false},
		{"", "1", true, false},
		{"always", "1", false, true},
		{"never", "", true, false},
		{"ALWAYS", "", false, true},
		{"Never", "", true, false},
	}
	for _, tc := range cases {
		got, err := ColorEnabled(tc.flag, tc.noColor, tc.tty)
		require.NoError(t, err)
		assert.Equal(t, tc.want, got, "%+v", tc)
	}
	_, err := ColorEnabled("sometimes", "", true)
	assert.Equal(t, errs.KindUsage, errs.KindOf(err))
}
