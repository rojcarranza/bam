package cli

import (
	"testing"

	"github.com/rojcarranza/bam/internal/app"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordLast records PROJ-BUILD-482 as the last build triggered from the
// harness repository, as bam run does.
func recordLast(t *testing.T, h *harness) {
	t.Helper()
	require.NoError(t, (&app.StateStore{Path: h.env.Paths.State}).SetLast(h.root, app.LastRecord{BuildKey: "PROJ-BUILD-482", Origin: workOrigin}))
}

// BAM-11 P14: commands that only read use the last build when none is named.
func TestReadingCommandsDefaultToTheLastBuild(t *testing.T) {
	for _, args := range [][]string{
		{"build", "show"}, {"build", "tree"}, {"tree"}, {"build", "investigate"}, {"investigate"}, {"why"},
		{"logs"}, {"watch"},
	} {
		h := newHarness(t)
		assert.NotEqual(t, 0, h.run(args...), "%v needs a build when none was triggered", args)
		assert.Contains(t, h.stderr.String(), "which build?", "%v", args)
	}
	h := newHarness(t)
	recordLast(t, h)
	assert.Equal(t, 0, h.run("build", "show"), h.stderr.String())
	assert.Contains(t, h.stdout.String(), "PROJ-BUILD-482")
	assert.Equal(t, 0, h.run("tree"), h.stderr.String())
	assert.Contains(t, h.stdout.String(), "PROJ-BUILD-482")
	assert.Equal(t, 0, h.run("why"), h.stderr.String())
	assert.Equal(t, 0, h.run("url"))
	assert.Equal(t, workOrigin+"/browse/PROJ-BUILD-482\n", h.stdout.String())
	assert.Equal(t, 0, h.run("watch"), h.stderr.String())
}

// BAM-11 P14: cancel and rerun change builds, so they still need a key or --last.
func TestChangingCommandsStillNeedAKey(t *testing.T) {
	h := newHarness(t)
	recordLast(t, h)
	for _, args := range [][]string{{"cancel"}, {"build", "cancel"}, {"build", "rerun"}, {"tree", "--rerun-failed", "--yes"}} {
		assert.Equal(t, 2, h.run(args...), "%v", args)
		assert.Contains(t, h.stderr.String(), "which build?", "%v", args)
	}
	assert.Equal(t, 0, h.run("cancel", "--last", "--dry-run"), h.stderr.String())
}

// BAM-11 P14: ls, -w, -b, and the forms bam used to refuse.
func TestAcceptedShortFormsAndAliases(t *testing.T) {
	h := newHarness(t)
	assert.Equal(t, 0, h.run("build", "ls", "PROJ-BUILD"), h.stderr.String())
	assert.Equal(t, 0, h.run("plan", "ls"), h.stderr.String())
	assert.Equal(t, 0, h.run("preset", "ls"), h.stderr.String())
	assert.Equal(t, 0, h.run("build", "list", "provision-lab", "-b", "develop"), h.stderr.String())
	assert.Equal(t, 0, h.run("build", "show", "PROJ-BUILD", "482"), "a plan and a number as two arguments")
	assert.Contains(t, h.stdout.String(), "PROJ-BUILD-482")
	assert.Equal(t, 0, h.run("build", "show", "PROJ-BUILD", "#482"), h.stderr.String())
	assert.Equal(t, 0, h.run("build", "show", "Provision-Lab"), "a preset typed with capitals")

	recordLast(t, h)
	assert.Equal(t, 0, h.run("build", "show", "482"), "a bare number takes the plan of the last build")
	assert.Contains(t, h.stdout.String(), "PROJ-BUILD-482")

	for _, flag := range []string{"-w", "-b"} {
		assert.NotNil(t, newWatchTreeFlag(t, flag), flag)
	}
	assert.Equal(t, 0, h.run("server", "add", "lab", "https://bamboo.lab.example"), h.stderr.String())
	assert.Equal(t, 0, h.run("preset", "add", "web", "PROJ-BUILD"), h.stderr.String())
}

// newWatchTreeFlag finds the command that owns a shorthand.
func newWatchTreeFlag(t *testing.T, short string) any {
	t.Helper()
	root := newRoot(&runtime{})
	for _, path := range [][]string{{"run"}, {"build", "tree"}, {"build", "rerun"}, {"build", "list"}} {
		c, _, err := root.Find(path)
		require.NoError(t, err)
		if f := c.Flags().ShorthandLookup(short[1:]); f != nil {
			return f
		}
	}
	return nil
}
