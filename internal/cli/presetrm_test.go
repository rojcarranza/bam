package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rojcarranza/bam/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pinnedPreset adds a preset to the shared file and pins it on the board.
func pinnedPreset(t *testing.T, h *harness) string {
	t.Helper()
	require.Equal(t, 0, h.run("preset", "add", "build", "--plan", "PROJ-BUILD", "--file", "mine"), h.stderr.String())
	shared := filepath.Join(h.root, ".bam.yaml")
	data, err := os.ReadFile(shared)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(shared, append(data, []byte("monitor:\n  integration: [PROJ-BUILD, build]\n")...), 0o644))
	h.stdout.Reset()
	h.stderr.Reset()
	return shared
}

// On a terminal a typed name asks first, naming the plan, branch and files.
// Only y or yes removes it.
func TestPresetRmAsksOnATerminal(t *testing.T) {
	h := newHarness(t)
	personal := filepath.Join(h.home, "bam", "presets", "mine.yaml")
	require.Equal(t, 0, h.run("preset", "add", "build", "--plan", "PROJ-BUILD", "--file", "mine"), h.stderr.String())
	h.stderr.Reset()
	h.tty = true
	h.stdin = "y\n"

	require.Equal(t, 0, h.run("preset", "rm", "build"), h.stderr.String())
	shared := filepath.Join(h.root, ".bam.yaml")
	assert.Contains(t, h.stderr.String(), "Remove preset build (PROJ-BUILD) from "+shared+", "+personal+"? [y/N] ")
	assert.Contains(t, h.stdout.String(), "removed preset build from "+personal)
	assert.Equal(t, 2, h.run("preset", "show", "build"))
}

func TestPresetRmAnythingButYesRemovesNothing(t *testing.T) {
	for _, answer := range []string{"\n", "n\n", "", "maybe\n"} {
		h := newHarness(t)
		h.tty = true
		h.stdin = answer
		require.Equal(t, 0, h.run("preset", "rm", "provision-lab"), h.stderr.String())
		assert.Contains(t, h.stderr.String(), "nothing removed", answer)
		assert.Empty(t, h.stdout.String(), answer)
		assert.Equal(t, 0, h.run("preset", "show", "provision-lab"), answer)
	}
}

func TestPresetRmYesSkipsTheQuestion(t *testing.T) {
	h := newHarness(t)
	h.tty = true
	require.Equal(t, 0, h.run("preset", "rm", "provision-lab", "--yes"), h.stderr.String())
	assert.NotContains(t, h.stderr.String(), "[y/N]")
	assert.Contains(t, h.stdout.String(), "removed preset provision-lab from")
}

// Bare rm on a terminal picks from the list, then asks; Esc removes nothing.
func TestPresetRmWithoutANamePicks(t *testing.T) {
	h := newHarness(t)
	h.tty = true
	h.stdin = "y\n"
	offered := pickScript(h, "provision-lab", nil)
	require.Equal(t, 0, h.run("preset", "rm"), h.stderr.String())
	assert.NotEmpty(t, *offered)
	assert.Contains(t, h.stderr.String(), "Remove preset provision-lab (PROJ-PROV · develop)")
	assert.Equal(t, 2, h.run("preset", "show", "provision-lab"))

	h = newHarness(t)
	h.tty = true
	pickScript(h, "", context.Canceled)
	assert.Equal(t, 130, h.run("preset", "rm"))
	assert.Equal(t, 0, h.run("preset", "show", "provision-lab"))
}

// A pipe, --json, --no-tui and BAM_NO_TUI keep today's behaviour: rm NAME
// removes at once, bare rm is a usage error, and nothing is asked.
func TestPresetRmWithoutATerminalNeverAsks(t *testing.T) {
	cases := map[string]struct {
		tty  bool
		args []string
		env  map[string]string
	}{
		"pipe":       {false, nil, nil},
		"json":       {true, []string{"--json"}, nil},
		"no-tui":     {true, []string{"--no-tui"}, nil},
		"BAM_NO_TUI": {true, nil, map[string]string{"BAM_NO_TUI": "1"}},
	}
	for name, tc := range cases {
		h := newHarness(t)
		h.tty = tc.tty
		for k, v := range tc.env {
			h.vars[k] = v
		}
		offered := pickScript(h, "provision-lab", nil)

		assert.Equal(t, 2, h.run(append([]string{"preset", "rm"}, tc.args...)...), name)
		assert.Contains(t, h.stderr.String(), "bam preset rm needs a preset name", name)
		assert.Contains(t, h.stderr.String(), "try: bam preset list", name)
		assert.Empty(t, *offered, name)

		h.stdout.Reset()
		h.stderr.Reset()
		require.Equal(t, 0, h.run(append([]string{"preset", "rm", "provision-lab"}, tc.args...)...), name+": "+h.stderr.String())
		assert.Contains(t, h.stdout.String(), "removed preset provision-lab from", name)
		assert.NotContains(t, h.stderr.String(), "[y/N]", name)
		assert.Equal(t, 2, h.run("preset", "show", "provision-lab"), name)
	}
}

// A set that uses the preset keeps it, before any question is asked.
func TestPresetRmUsedBySetAsksNothing(t *testing.T) {
	h := setHarness(t, provider.StateSuccess)
	h.tty = true
	assert.Equal(t, 2, h.run("preset", "rm", "provision-lab"))
	assert.Contains(t, h.stderr.String(), `preset "provision-lab" is used by set "release"`)
	assert.NotContains(t, h.stderr.String(), "[y/N]")
}

// A preset still pinned on the board is removed, and bam names the group.
func TestPresetRmNotesTheBoardPin(t *testing.T) {
	h := newHarness(t)
	pinnedPreset(t, h)
	require.Equal(t, 0, h.run("preset", "rm", "build"), h.stderr.String())
	assert.Contains(t, h.stderr.String(), "preset build is still pinned in the bam top group integration")
}

func TestPresetRmWithNoPresetsAndNoNameOnATerminal(t *testing.T) {
	h := newHarness(t)
	h.tty = true
	h.env.WorkDir = t.TempDir()
	pickScript(h, "x", nil)
	assert.Equal(t, 2, h.run("preset", "rm"))
	assert.Contains(t, h.stderr.String(), "no presets")
}
