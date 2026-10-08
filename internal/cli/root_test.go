package cli

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"testing"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVersionPrintsVersion(t *testing.T) {
	h := newHarness(t)
	assert.Equal(t, 0, h.run("version"))
	assert.Equal(t, "bam dev\n", h.stdout.String())
}

// TestVersionFallsBackToBuildInfo: go install cannot pass -ldflags, so a
// binary built that way reports the module version Go recorded in it.
func TestVersionFallsBackToBuildInfo(t *testing.T) {
	info := func(v string) func() (*debug.BuildInfo, bool) {
		return func() (*debug.BuildInfo, bool) {
			return &debug.BuildInfo{Main: debug.Module{Path: "github.com/r0jjames/bam-cli", Version: v}}, true
		}
	}
	noInfo := func() (*debug.BuildInfo, bool) { return nil, false }

	assert.Equal(t, "v0.2.1", resolveVersion("v0.2.1", info("v0.2.0")), "-ldflags wins")
	assert.Equal(t, "v0.2.1", resolveVersion("dev", info("v0.2.1")), "go install @v0.2.1")
	assert.Equal(t, "v0.2.2-0.20261008120000-021f33300000", resolveVersion("dev", info("v0.2.2-0.20261008120000-021f33300000")), "go install @main")
	assert.Equal(t, "dev", resolveVersion("dev", info("(devel)")), "go build without VCS stamping")
	assert.Equal(t, "dev", resolveVersion("dev", info("")))
	assert.Equal(t, "dev", resolveVersion("dev", noInfo))
}

func TestUnknownCommandIsUsageError(t *testing.T) {
	h := newHarness(t)
	assert.Equal(t, 2, h.run("nope"))
	assert.Contains(t, h.stderr.String(), "unknown command")
}

func TestUnknownSubcommandIsUsageError(t *testing.T) {
	for _, args := range [][]string{{"server", "remove", "work"}, {"plan", "nope"}, {"build", "nope"}} {
		h := newHarness(t)
		assert.Equal(t, 2, h.run(args...), args)
		assert.Contains(t, h.stderr.String(), "unknown command", args)
	}
}

func TestGroupCommandWithoutArgsPrintsHelp(t *testing.T) {
	h := newHarness(t)
	assert.Equal(t, 0, h.run("server"))
	assert.Contains(t, h.stdout.String(), "Usage:")
}

// Bare bam opened the UI as of v0.2 part A; on a pipe it still prints help.
// The entry matrix itself is covered by ui_test.go.
func TestBareBamOnAPipeStillPrintsHelp(t *testing.T) {
	h := newHarness(t)
	h.tty = false
	assert.Equal(t, 0, h.run())
	assert.Contains(t, h.stdout.String(), "Usage:")
	assert.NotContains(t, h.stdout.String(), "interactive mode")
	assert.Empty(t, h.tuiRuns)
}

func TestDocsRootIncludesCompletion(t *testing.T) {
	root := DocsRoot()
	c, _, err := root.Find([]string{"completion"})
	require.NoError(t, err)
	assert.Equal(t, "completion", c.Name())
}

func TestExitCodes(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{nil, 0},
		{&resultError{}, 1},
		{errs.Usagef("x"), 2},
		{errs.Configf("x"), 3},
		{errs.Authf("x"), 4},
		{errs.Bamboof("x"), 5},
		{errs.New(errs.KindTimeout, "x"), 6},
		{errs.New(errs.KindInternal, "x"), 5},
		{fmt.Errorf("wrapped: %w", context.Canceled), 130},
		{context.DeadlineExceeded, 6},
		{errors.New("accepts 1 arg(s), received 0"), 2},
		{errs.Bamboof("cannot reach x").Wrap(context.DeadlineExceeded), 5},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, exitCode(tc.err), "%v", tc.err)
	}
}
