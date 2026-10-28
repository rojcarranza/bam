package cli

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
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

// TestVersionFlag: --version and -v are what people type first.
func TestVersionFlag(t *testing.T) {
	for _, arg := range []string{"--version", "-v"} {
		h := newHarness(t)
		assert.Equal(t, 0, h.run(arg), arg)
		assert.Equal(t, "bam dev\n", h.stdout.String(), arg)
	}
	h := newHarness(t)
	assert.Equal(t, 0, h.run("--version", "--json"))
	assert.JSONEq(t, `{"version":"dev"}`, h.stdout.String())
}

// TestVersionJSON: --json is honoured by every command that reads data.
func TestVersionJSON(t *testing.T) {
	h := newHarness(t)
	assert.Equal(t, 0, h.run("version", "--json"))
	assert.JSONEq(t, `{"version":"dev"}`, h.stdout.String())
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

func TestUnknownCommandSuggestsTheClosest(t *testing.T) {
	for args, want := range map[string]string{
		"biuld":           "try: did you mean bam build? bam --help shows them all",
		"pln":             "try: did you mean bam plan? bam --help shows them all",
		"build lsit":      "try: did you mean bam build list? bam build --help shows them all",
		"server xyzzy":    "try: bam server --help\n",
		"build history":   "try: did you mean bam build list? bam build --help shows them all",
		"build stop":      "try: did you mean bam build cancel? bam build --help shows them all",
		"server remove":   "try: did you mean bam server rm? bam server --help shows them all",
		"cancel":          "try: did you mean bam build cancel? bam --help shows them all",
		"history":         "try: did you mean bam build list? bam --help shows them all",
		"target":          "try: did you mean bam preset? bam --help shows them all",
		"build view":      "try: did you mean bam build show? bam build --help shows them all",
		"set describe":    "try: did you mean bam set show? bam set --help shows them all",
		"set rm x":        "error: bam set has no \"rm\" command\n  sets are edited in YAML: under sets: in .bam.yaml, or in a train file\n  try: see bam set run --help\n",
		"set add":         "error: bam set has no \"add\" command\n",
		"preset run":      "error: bam preset has no \"run\" command\n  presets run with bam run\n  try: bam run <preset>\n",
		"preset run api":  "try: bam run api\n",
		"preset lst":      "try: did you mean bam preset list?",
		"preset edit api": "error: bam preset has no \"edit\" command\n  presets are edited in YAML, in the file that defines them\n  try: bam preset show api prints the file that defines it\n",
		"view":            "try: did you mean bam build show, bam plan show, bam preset show or bam set show? bam --help shows them all",
		"get":             "try: did you mean bam build show, bam plan show, bam preset show or bam set show? bam --help shows them all",
		"list":            "try: did you mean bam build list, bam plan list, bam preset list, bam project list, bam server list or bam set list?",
		"rerun":           "try: did you mean bam build rerun or bam set rerun? bam --help shows them all",
		"retry":           "try: did you mean bam build rerun or bam set rerun? bam --help shows them all",
		"status":          "try: did you mean bam build show or bam set status? bam --help shows them all",
		"build status":    "try: did you mean bam build show? bam build --help shows them all",
		"build restart":   "try: did you mean bam build rerun? bam build --help shows them all",
		"log":             "try: did you mean bam logs? bam --help shows them all",
		"del":             "try: bam --help\n",
		"ls":              "try: did you mean bam build list, bam plan list, bam preset list, bam project list, bam server list or bam set list?",
		"preset ls":       "try: did you mean bam preset list? bam preset --help shows them all",
		"branch":          "try: did you mean bam plan branches? bam --help shows them all",
		"branches":        "try: did you mean bam plan branches? bam --help shows them all",
		"plan branch":     "try: did you mean bam plan branches? bam plan --help shows them all",
		"auth":            "try: did you mean bam login? bam --help shows them all",
		"token":           "try: did you mean bam login? bam --help shows them all",
		"signout":         "try: did you mean bam logout? bam --help shows them all",
		"rnu":             "try: did you mean bam run? bam --help shows them all",
		"completion zhs":  "error: unknown command \"zhs\" for \"bam completion\"\n  try: did you mean bam completion zsh? bam completion --help shows them all\n",
	} {
		h := newHarness(t)
		assert.Equal(t, 2, h.run(strings.Fields(args)...), args)
		assert.Contains(t, h.stderr.String(), want, args)
	}
}

func TestHelpUnknownTopicIsUsageError(t *testing.T) {
	for args, want := range map[string]string{
		"help foo":       "error: unknown command \"foo\" for \"bam\"\n  try: bam --help\n",
		"help biuld":     "try: did you mean bam build? bam --help shows them all",
		"help build lst": "try: did you mean bam build list? bam build --help shows them all",
	} {
		h := newHarness(t)
		assert.Equal(t, 2, h.run(strings.Fields(args)...), args)
		assert.Contains(t, h.stderr.String(), want, args)
		assert.Empty(t, h.stdout.String(), args)
	}
	for _, args := range []string{"help", "help build", "help build show PROJ-PLAN-1"} {
		h := newHarness(t)
		assert.Equal(t, 0, h.run(strings.Fields(args)...), args)
		assert.Contains(t, h.stdout.String(), "Usage:", args)
	}
}

func TestBadFlagValueSaysWhatItWants(t *testing.T) {
	for args, want := range map[string]string{
		"build list PROJ-PLAN --limit abc": "error: --limit wants a whole number, like 10, not \"abc\"\n  try: bam build list --help\n",
		"watch PROJ-PLAN-1 --timeout 5":    "error: --timeout wants a duration, like 90s, 10m or 1h30m, not \"5\"\n",
		"build list --state":               "error: flag needs an argument: --state\n",
	} {
		h := newHarness(t)
		assert.Equal(t, 2, h.run(strings.Fields(args)...), args)
		assert.Contains(t, h.stderr.String(), want, args)
	}
}

func TestWrongArgCountPointsAtHelp(t *testing.T) {
	for args, want := range map[string]string{
		"run":              "error: missing <plan|preset>\n  try: bam run <plan|preset>; bam run --help shows more\n",
		"preset add":       "error: missing <name>\n  try: bam preset add <name> --plan KEY; bam preset add --help shows more\n",
		"build show a b":   "error: unexpected argument \"b\"\n  try: bam build show [<build>|<plan>|<preset>]; bam build show --help shows more\n",
		"server add w x y": "error: unexpected arguments \"x\", \"y\"\n  try: bam server add <alias> --url URL; bam server add --help shows more\n",
		"whoami x":         "error: unexpected argument \"x\"\n  try: bam whoami; bam whoami --help shows more\n",
	} {
		h := newHarness(t)
		assert.Equal(t, 2, h.run(strings.Fields(args)...), args)
		assert.Contains(t, h.stderr.String(), want, args)
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

// A group summary like "Plans: list, show, …" must name exactly its subcommands.
func TestGroupSummariesListTheirSubcommands(t *testing.T) {
	for _, c := range DocsRoot().Commands() {
		_, list, ok := strings.Cut(c.Short, ": ")
		if !ok || !c.HasSubCommands() || strings.Contains(strings.ReplaceAll(list, ", ", ""), " ") {
			continue
		}
		var subs []string
		for _, s := range c.Commands() {
			subs = append(subs, s.Name())
		}
		assert.ElementsMatch(t, subs, strings.Split(list, ", "), "bam %s", c.Name())
	}
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

func TestBadColorFlagFailsBeforeTheCommandRuns(t *testing.T) {
	h := newHarness(t)
	assert.Equal(t, 2, h.run("--color", "bogus", "url", "PROJ"))
	assert.Empty(t, h.stdout.String())
	assert.Contains(t, h.stderr.String(), `--color must be auto, always or never, not "bogus"`)
}
