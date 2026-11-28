package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/rojcarranza/bam/internal/app"
	"github.com/rojcarranza/bam/internal/provider"
	"github.com/rojcarranza/bam/internal/view/tui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runFormHarness is a terminal with a form that gets edit applied and then
// starts the build when start is set, as ctrl-r does.
func runFormHarness(t *testing.T, start bool, edit func(*app.PresetForm)) (*harness, *[]app.PresetForm) {
	h := newHarness(t)
	h.tty = true
	h.fake.TriggerResult = queuedBuild("PROJ-PROV-9")
	offered := formScript(h, func(f *app.PresetForm) {
		if edit != nil {
			edit(f)
		}
		f.StartNow = start
	}, nil)
	return h, offered
}

func presetFiles(t *testing.T, h *harness) []string {
	var found []string
	require.NoError(t, filepath.Walk(h.home, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && filepath.Base(p) != "usage.json" {
			found = append(found, p)
		}
		return nil
	}))
	return found
}

// Intent: ctrl-s prints one line a colleague runs as plain bam run, starts
// nothing, asks for no name and writes no preset.
func TestRunPlanCtrlSPrintsTheLineAndStartsNothing(t *testing.T) {
	h, offered := runFormHarness(t, false, func(f *app.PresetForm) { f.Rows[0].Value = "alpha" })
	before := presetFiles(t, h)
	require.Equal(t, 0, h.run("run", "PROJ-PROV", "--plan"), h.stderr.String())

	f := (*offered)[0]
	assert.True(t, f.RunMode)
	assert.Empty(t, f.Name, "a run is never named")
	assert.Equal(t, "PROJ-PROV", f.Plan)
	assert.Equal(t, []string{"cluster_name", "cluster_type"}, []string{f.Rows[0].Name, f.Rows[1].Name})
	assert.Contains(t, h.stdout.String(), "bam run PROJ-PROV --var cluster_name=alpha\n")
	assert.Contains(t, h.stdout.String(), "\x1b]52;c;", "the line is copied through the terminal")
	assert.Empty(t, h.fake.Triggered)
	assert.Equal(t, before, presetFiles(t, h))
}

// Intent: ctrl-r starts the build with the form's values and prints the same
// line, which names the plan and leaves the server alias out.
func TestRunPlanCtrlRStartsTheBuildAndPrintsTheLine(t *testing.T) {
	h, _ := runFormHarness(t, true, func(f *app.PresetForm) { f.Rows[0].Value = "alpha" })
	require.Equal(t, 0, h.run("run", "PROJ-PROV", "--plan", "--server", "work", "--watch=false"), h.stderr.String())

	require.Len(t, h.fake.Triggered, 1)
	assert.Equal(t, "PROJ-PROV", h.fake.Triggered[0].PlanKey)
	assert.Equal(t, map[string]string{"cluster_name": "alpha"}, h.fake.Triggered[0].Variables)
	assert.Contains(t, h.stderr.String(), "bam run PROJ-PROV --var cluster_name=alpha\n")
	assert.NotContains(t, h.stderr.String(), "--server")
}

func TestRunPlanEscStartsNothing(t *testing.T) {
	h := newHarness(t)
	h.tty = true
	formScript(h, nil, context.Canceled)
	assert.Equal(t, 130, h.run("run", "PROJ-PROV", "--plan"))
	assert.Contains(t, h.stderr.String(), "nothing started")
	assert.Empty(t, h.fake.Triggered)
}

// Intent: with no plan the plan list and the branch list open, as in preset
// add, and no name is asked.
func TestRunPlanPicksPlanAndBranchFromLists(t *testing.T) {
	h, offered := runFormHarness(t, false, nil)
	var prompts []string
	h.env.Choose = func(_ context.Context, _ io.Reader, _ io.Writer, prompt string, _ []tui.Choice) (int, error) {
		prompts = append(prompts, prompt)
		return 1, nil
	}
	h.env.Ask = func(context.Context, io.Reader, io.Writer, string, string) (string, error) {
		t.Fatal("a name was asked")
		return "", nil
	}
	require.Equal(t, 0, h.run("run", "--plan"), h.stderr.String())
	assert.Equal(t, []string{"run which plan?", "which branch of PROJ-PROV?"}, prompts)
	assert.Equal(t, "PROJ-PROV", (*offered)[0].Plan)
	assert.Equal(t, "develop", (*offered)[0].Branch)
	assert.Contains(t, h.stdout.String(), "bam run PROJ-PROV --branch develop")
}

// Intent: --from a build key names the plan and its values spell out in the line.
func TestRunPlanFromBuildImpliesPlanAndSpellsOutValues(t *testing.T) {
	h, offered := runFormHarness(t, false, nil)
	h.fake.Plans["PROJ"] = append(h.fake.Plans["PROJ"], provider.Plan{Key: "PROJ-PROV12", MasterKey: "PROJ-PROV", ProjectKey: "PROJ"})
	h.env.Choose = func(context.Context, io.Reader, io.Writer, string, []tui.Choice) (int, error) {
		t.Fatal("a list opened although the build names the plan")
		return 0, nil
	}
	require.Equal(t, 0, h.run("run", "--plan", "--from", "PROJ-PROV12-8"), h.stderr.String())
	f := (*offered)[0]
	assert.Equal(t, "PROJ-PROV", f.Plan)
	assert.Equal(t, "develop", f.Branch, "the build's plan branch")
	assert.Contains(t, h.stdout.String(), "bam run PROJ-PROV --branch develop --var cluster_name=beta --var cluster_type=dcos\n")
	assert.NotContains(t, h.stdout.String(), "--from", "the values are spelled out, never read from a build later")
}

// Intent: a secret is written as a shell reference that stops when unset, never as a value.
func TestRunPlanSecretIsAShellReference(t *testing.T) {
	h, _ := runFormHarness(t, false, func(f *app.PresetForm) {
		f.Rows[0].Commented = false
		f.Rows[0].Value = "${QA_TOKEN}"
	})
	h.fake.Variables["PROJ-PROV"] = []provider.Variable{{Name: "deploy_token", Value: "********", Masked: true}}
	require.Equal(t, 0, h.run("run", "PROJ-PROV", "--plan"), h.stderr.String())
	assert.Contains(t, h.stdout.String(), `bam run PROJ-PROV --var deploy_token="${QA_TOKEN:?}"`+"\n")
	assert.NotContains(t, h.stdout.String(), "********")
}

// Intent: ctrl-r reads a secret from the shell and refuses before starting when it is unset.
func TestRunPlanRunNeedsTheSecretsEnvironmentVariable(t *testing.T) {
	h, _ := runFormHarness(t, true, func(f *app.PresetForm) {
		f.Rows[0].Commented = false
		f.Rows[0].Value = "${QA_TOKEN}"
	})
	h.fake.Variables["PROJ-PROV"] = []provider.Variable{{Name: "deploy_token", Value: "********", Masked: true}}
	assert.Equal(t, 2, h.run("run", "PROJ-PROV", "--plan"))
	assert.Contains(t, h.stderr.String(), "needs environment variable QA_TOKEN")
	assert.Empty(t, h.fake.Triggered)

	h.vars["QA_TOKEN"] = "s3cret"
	require.Equal(t, 0, h.run("run", "PROJ-PROV", "--plan"), h.stderr.String())
	require.Len(t, h.fake.Triggered, 1)
	assert.Equal(t, "s3cret", h.fake.Triggered[0].Variables["deploy_token"])
	assert.NotContains(t, h.stderr.String()+h.stdout.String(), "s3cret")
}

// Intent: without a terminal --plan exits 2 before any network call.
func TestRunPlanNeedsATerminal(t *testing.T) {
	h := newHarness(t)
	assert.Equal(t, 2, h.run("run", "PROJ-PROV", "--plan"))
	assert.Contains(t, h.stderr.String(), "--plan needs a terminal")
	assert.Empty(t, h.connectOpts)
}

// Intent: --read-only still prints a line with ctrl-s and refuses ctrl-r.
func TestRunPlanReadOnly(t *testing.T) {
	h, _ := runFormHarness(t, false, nil)
	require.Equal(t, 0, h.run("run", "PROJ-PROV", "--plan", "--read-only"), h.stderr.String())
	assert.Contains(t, h.stdout.String(), "bam run PROJ-PROV")

	h, _ = runFormHarness(t, true, nil)
	assert.NotEqual(t, 0, h.run("run", "PROJ-PROV", "--plan", "--read-only"))
	assert.Empty(t, h.fake.Triggered)
}

// Intent: with --json ctrl-s prints the line as a document and leaves the clipboard alone.
func TestRunPlanJSONPrintsTheCommand(t *testing.T) {
	h, _ := runFormHarness(t, false, nil)
	require.Equal(t, 0, h.run("run", "PROJ-PROV", "--plan", "--json"), h.stderr.String())
	assert.JSONEq(t, `{"command":"bam run PROJ-PROV"}`, h.stdout.String())
}

// Intent: personal flags stay out of the line while --branch and --repository carry over.
func TestRunPlanLineLeavesPersonalFlagsOut(t *testing.T) {
	h, _ := runFormHarness(t, false, nil)
	require.Equal(t, 0, h.run("run", "PROJ-PROV", "--plan", "--branch", "develop", "--watch", "--timeout", "5m", "--server", "work"), h.stderr.String())
	assert.Contains(t, h.stdout.String(), "bam run PROJ-PROV --branch develop\n")
	assert.NotContains(t, h.stdout.String(), "--watch")
	assert.NotContains(t, h.stdout.String(), "--timeout")
	assert.NotContains(t, h.stdout.String(), "--server")
}
