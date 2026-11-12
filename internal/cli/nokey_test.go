package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rojcarranza/bam/internal/app"
	"github.com/rojcarranza/bam/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordLast records key, a build of PROJ-BUILD, as the last build triggered
// from the harness repository, as bam run does.
func recordLast(t *testing.T, h *harness, key string) {
	t.Helper()
	require.NoError(t, (&app.StateStore{Path: h.env.Paths.State}).SetLast(h.root, app.LastRecord{BuildKey: key, Origin: workOrigin, PlanKey: "PROJ-BUILD"}))
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
	assert.Equal(t, 2, h.run("build", "list"), "build list needs a plan when no build was triggered")
	assert.Contains(t, h.stderr.String(), "missing <plan|preset>")
	for _, cmd := range []string{"url", "open"} {
		assert.Equal(t, 2, h.run(cmd), "%s needs a key when no build was triggered", cmd)
		assert.Contains(t, h.stderr.String(), "which Bamboo page?", cmd)
	}
	recordLast(t, h, "PROJ-BUILD-482")
	require.Equal(t, 0, h.run("build", "list", "PROJ-BUILD"), h.stderr.String())
	named := h.stdout.String()
	assert.Equal(t, 0, h.run("build", "list"), h.stderr.String())
	assert.Equal(t, named, h.stdout.String(), "build list lists the plan of the last build")
	assert.Equal(t, 0, h.run("build", "show"), h.stderr.String())
	assert.Contains(t, h.stdout.String(), "PROJ-BUILD-482")
	assert.Equal(t, 0, h.run("tree"), h.stderr.String())
	assert.Contains(t, h.stdout.String(), "PROJ-BUILD-482")
	assert.Equal(t, 0, h.run("why"), h.stderr.String())
	assert.Equal(t, 0, h.run("url"))
	assert.Equal(t, workOrigin+"/browse/PROJ-BUILD-482\n", h.stdout.String())
	assert.Equal(t, 0, h.run("watch"), h.stderr.String())
	for _, args := range [][]string{{"logs"}, {"build", "tree"}, {"build", "investigate"}, {"investigate"}} {
		assert.Equal(t, 0, h.run(args...), "%v: %s", args, h.stderr.String())
		assert.Contains(t, h.stdout.String()+h.stderr.String(), "PROJ-BUILD-482", "%v reads the last build", args)
	}
	assert.Equal(t, 0, h.run("open"), h.stderr.String())
	assert.Equal(t, []string{workOrigin + "/browse/PROJ-BUILD-482"}, h.opened)
}

// BAM-11 P14: cancel and rerun change builds, so they still need a key or --last.
func TestChangingCommandsStillNeedAKey(t *testing.T) {
	h := newHarness(t)
	recordLast(t, h, "PROJ-BUILD-482")
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
	qh := queueHarness(t)
	require.Equal(t, 0, qh.run("agent", "list"), qh.stderr.String())
	agents := qh.stdout.String()
	require.Equal(t, 0, qh.run("agent", "ls"), qh.stderr.String())
	assert.Equal(t, agents, qh.stdout.String(), "agent ls is agent list")
	assert.Equal(t, 0, h.run("build", "list", "provision-lab", "-b", "develop"), h.stderr.String())
	assert.Equal(t, 0, h.run("build", "show", "PROJ-BUILD", "482"), "a plan and a number as two arguments")
	assert.Contains(t, h.stdout.String(), "PROJ-BUILD-482")
	assert.Equal(t, 0, h.run("build", "show", "PROJ-BUILD", "#482"), h.stderr.String())
	assert.Equal(t, 0, h.run("build", "show", "Provision-Lab"), "a preset typed with capitals")

	recordLast(t, h, "PROJ-BUILD-480")
	assert.Equal(t, 0, h.run("build", "show", "482"), "a bare number takes the plan of the last build")
	assert.Contains(t, h.stdout.String(), "PROJ-BUILD-482")
	assert.Equal(t, 0, h.run("build", "show", "#482"), h.stderr.String())
	assert.Contains(t, h.stdout.String(), "PROJ-BUILD-482")

	assert.Equal(t, 0, h.run("build", "list", "PROJ-PROV", "-b", "develop"), h.stderr.String())
	assert.Contains(t, h.stdout.String(), "PROJ-PROV12-8", "-b lists the develop branch")

	assert.Equal(t, 0, h.run("server", "add", "lab", "https://bamboo.lab.example"), h.stderr.String())
	assert.Equal(t, 0, h.run("server", "list"), h.stderr.String())
	assert.Regexp(t, `lab\s+https://bamboo.lab.example`, h.stdout.String())
	assert.Equal(t, 0, h.run("preset", "add", "web", "PROJ-BUILD"), h.stderr.String())
	assert.Equal(t, 0, h.run("preset", "show", "web", "--json"), h.stderr.String())
	var shown map[string]any
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &shown))
	assert.Equal(t, "PROJ-BUILD", shown["plan_key"])

	w := newHarness(t)
	w.fake.TriggerResult = queuedBuild("PROJ-BUILD-483")
	w.fake.Sequences = map[string][]provider.Build{"PROJ-BUILD-483": {
		{Key: "PROJ-BUILD-483", State: provider.StateRunning},
		{Key: "PROJ-BUILD-483", State: provider.StateSuccess},
	}}
	assert.Equal(t, 0, w.run("run", "PROJ-BUILD", "-w"), w.stderr.String())
	assert.Contains(t, w.stdout.String(), "PROJ-BUILD-483  ✓ success", "-w follows the build")
}

// BAM-11 P14: a preset typed with capitals is that preset everywhere: its
// timeout applies and bam url browses its plan.
func TestCapitalizedPresetKeepsItsSettings(t *testing.T) {
	h := newHarness(t)
	yaml := strings.Replace(projectYAML, "  build:\n    plan: PROJ-BUILD\n", "  build:\n    plan: PROJ-BUILD\n    timeout: 50ms\n", 1)
	require.NoError(t, os.WriteFile(filepath.Join(h.root, ".bam.yaml"), []byte(yaml), 0o644))
	h.fake.Sequences = map[string][]provider.Build{"PROJ-BUILD-482": {{Key: "PROJ-BUILD-482", State: provider.StateRunning}}}
	assert.Equal(t, 6, h.run("watch", "Build"))
	assert.Contains(t, h.stderr.String(), "timed out after 50ms")
	assert.Equal(t, 0, h.run("url", "Build"), h.stderr.String())
	assert.Equal(t, workOrigin+"/browse/PROJ-BUILD\n", h.stdout.String())
	assert.Equal(t, 0, h.run("open", "Provision-Lab"), h.stderr.String())
	assert.Equal(t, []string{workOrigin + "/browse/PROJ-PROV12"}, h.opened)
}

// BAM-11 P14: build list with no plan refuses, as --last does, when the last
// build ran on another server than the one --server names.
func TestBuildListRefusesTheLastBuildOfAnotherServer(t *testing.T) {
	h := newHarness(t)
	require.NoError(t, os.WriteFile(filepath.Join(h.root, ".bam.yaml"), []byte(insertServer(projectYAML)), 0o644))
	h.kr.m["bam|https://bamboo.lab.example"] = "tok2"
	recordLast(t, h, "PROJ-BUILD-482")
	assert.Equal(t, 3, h.run("build", "list", "--server", "lab"))
	assert.Contains(t, h.stderr.String(), `the last build PROJ-BUILD-482 ran on `+workOrigin+`, not on server "lab"`)
	assert.Equal(t, 3, h.run("build", "show", "--last", "--server", "lab"))
	assert.Contains(t, h.stderr.String(), `the last build PROJ-BUILD-482 ran on `+workOrigin+`, not on server "lab"`)
}
