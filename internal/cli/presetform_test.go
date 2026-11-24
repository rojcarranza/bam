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

// formScript replaces the form with edit, which stands in for the person: it
// gets the form bam offered after the recent values were read, and returns
// the form they confirm, or err when they press Esc. It returns every form
// offered.
func formScript(h *harness, edit func(*app.PresetForm), err error) *[]app.PresetForm {
	var offered []app.PresetForm
	h.env.EditPreset = func(ctx context.Context, _ io.Reader, _ io.Writer, f app.PresetForm, load func(context.Context) []app.RecentVar) (app.PresetForm, error) {
		f.AddRecent(load(ctx))
		offered = append(offered, f)
		if err != nil {
			return f, err
		}
		edit(&f)
		return f, nil
	}
	return &offered
}

func recentProv(h *harness) {
	h.fake.History["PROJ-PROV12"] = []provider.Build{{Key: "PROJ-PROV12-9", Number: 9}, {Key: "PROJ-PROV12-8", Number: 8}}
	h.fake.BuildVars["PROJ-PROV12-9"] = map[string]string{"cluster_type": "k8s", "cluster_name": "beta"}
}

func provFile(h *harness) string { return filepath.Join(h.home, "bam", "presets", "proj.yaml") }

// On a terminal the values chosen in the form are what the file holds, and
// the recent-values box, whose values were in the form, is not printed.
func TestPresetAddWritesTheValuesChosenInTheForm(t *testing.T) {
	h := newHarness(t)
	h.tty = true
	recentProv(h)
	offered := formScript(h, func(f *app.PresetForm) {
		f.Rows[0].Value = "dcos"
		f.Rows[1].Value = "gamma"
	}, nil)
	require.Equal(t, 0, h.run("preset", "add", "mine", "--plan", "PROJ-PROV", "--branch", "develop"), h.stderr.String())

	require.Len(t, *offered, 1)
	f := (*offered)[0]
	assert.Equal(t, "mine", f.Name)
	assert.Equal(t, provFile(h), f.Path)
	assert.Equal(t, []string{"cluster_type", "cluster_name"}, []string{f.Rows[0].Name, f.Rows[1].Name})
	assert.Equal(t, []string{"k8s", "dcos"}, f.Rows[0].Choices, "plan default, then the recent value that differs")

	data, err := os.ReadFile(provFile(h))
	require.NoError(t, err)
	assert.Contains(t, string(data), `cluster_type: "dcos"`)
	assert.Contains(t, string(data), `cluster_name: "gamma"`)
	assert.Contains(t, h.stdout.String(), "added preset mine to "+provFile(h))
	assert.NotContains(t, h.stdout.String(), "values to choose from")
}

// A secret the plan stores is offered as its stored value or an environment
// variable; choosing the variable writes an active ${ENV} line, never a value.
func TestPresetAddFormSecretWritesOnlyAnEnvReference(t *testing.T) {
	h := newHarness(t)
	h.tty = true
	h.fake.Variables["PROJ-PROV"] = []provider.Variable{{Name: "deploy_token", Value: "********", Masked: true}}
	offered := formScript(h, func(f *app.PresetForm) {
		f.Rows[0].Commented = false
		f.Rows[0].Value = "${QA_TOKEN}"
	}, nil)
	require.Equal(t, 0, h.run("preset", "add", "mine", "--plan", "PROJ-PROV"), h.stderr.String())
	assert.True(t, (*offered)[0].Rows[0].Secret)
	data, err := os.ReadFile(provFile(h))
	require.NoError(t, err)
	assert.Contains(t, string(data), `deploy_token: "${QA_TOKEN}"`)
	assert.NotContains(t, string(data), "********")
}

// No terminal, --no-tui, --print, --json: the form never opens and the file
// is the one bam wrote before the form existed.
func TestPresetAddOpensNoFormOffATerminal(t *testing.T) {
	baseline := newHarness(t)
	require.Equal(t, 0, baseline.run("preset", "add", "mine", "--plan", "PROJ-PROV"))
	want, err := os.ReadFile(provFile(baseline))
	require.NoError(t, err)

	for name, tc := range map[string]struct {
		tty   bool
		term  string
		args  []string
		write bool
	}{
		"pipe":    {false, "xterm", nil, true},
		"no-tui":  {true, "xterm", []string{"--no-tui"}, true},
		"dumb":    {true, "dumb", nil, true},
		"print":   {true, "xterm", []string{"--print"}, false},
		"json":    {true, "xterm", []string{"--json"}, true},
		"no term": {true, "", nil, true},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.tty = tc.tty
			h.vars["TERM"] = tc.term
			h.env.EditPreset = func(context.Context, io.Reader, io.Writer, app.PresetForm, func(context.Context) []app.RecentVar) (app.PresetForm, error) {
				t.Fatal("the form opened")
				return app.PresetForm{}, nil
			}
			require.Equal(t, 0, h.run(append([]string{"preset", "add", "mine", "--plan", "PROJ-PROV"}, tc.args...)...), h.stderr.String())
			if tc.write {
				got, err := os.ReadFile(provFile(h))
				require.NoError(t, err)
				assert.Equal(t, string(want), string(got))
			} else {
				assert.NoFileExists(t, provFile(h))
			}
		})
	}
}

func TestPresetAddWithNoVariablesOpensNoForm(t *testing.T) {
	h := newHarness(t)
	h.tty = true
	offered := formScript(h, func(*app.PresetForm) {}, nil)
	require.Equal(t, 0, h.run("preset", "add", "b2", "--plan", "PROJ-BUILD"), h.stderr.String())
	assert.Empty(t, *offered)
}

// Esc writes nothing, says so, and exits 130 like the run picker.
func TestPresetAddFormCancelWritesNothing(t *testing.T) {
	h := newHarness(t)
	h.tty = true
	formScript(h, nil, context.Canceled)
	assert.Equal(t, 130, h.run("preset", "add", "mine", "--plan", "PROJ-PROV"))
	assert.Contains(t, h.stderr.String(), "nothing written")
	assert.NoFileExists(t, provFile(h))
}

// A name that is taken fails before the form opens, so a filled form is
// never refused at write time.
func TestPresetAddTakenNameFailsBeforeTheForm(t *testing.T) {
	h := newHarness(t)
	require.Equal(t, 0, h.run("preset", "add", "mine", "--plan", "PROJ-PROV"))
	h.tty = true
	offered := formScript(h, func(*app.PresetForm) {}, nil)
	assert.Equal(t, 2, h.run("preset", "add", "mine", "--plan", "PROJ-PROV"))
	assert.Contains(t, h.stderr.String(), `preset "mine" already exists`)
	assert.Empty(t, *offered)

	assert.Equal(t, 0, h.run("preset", "add", "mine", "--plan", "PROJ-PROV", "--force"), h.stderr.String())
	assert.Len(t, *offered, 1, "--force replaces, so the form opens")
}

// Bare bam preset add on a terminal asks for the plan, its branch and the
// name, then fills the form.
func TestPresetAddGuidesPlanBranchAndName(t *testing.T) {
	h := newHarness(t)
	h.tty = true
	var prompts []string
	var planItems, branchItems []tui.Choice
	h.env.Choose = func(_ context.Context, _ io.Reader, _ io.Writer, prompt string, items []tui.Choice) (int, error) {
		prompts = append(prompts, prompt)
		if len(prompts) == 1 {
			planItems = items
			return 1, nil
		}
		branchItems = items
		return 1, nil
	}
	var initial string
	h.env.Ask = func(_ context.Context, _ io.Reader, _ io.Writer, _, init string) (string, error) {
		initial = init
		return "dev-lab", nil
	}
	formScript(h, func(*app.PresetForm) {}, nil)
	require.Equal(t, 0, h.run("preset", "add"), h.stderr.String())

	assert.Equal(t, []tui.Choice{{Label: "PROJ-BUILD", Detail: "Build and test"}, {Label: "PROJ-PROV", Detail: "Provision lab"}}, planItems)
	assert.Equal(t, []string{"add a preset for which plan?", "which branch of PROJ-PROV?"}, prompts)
	assert.Equal(t, []tui.Choice{{Label: "(no branch)", Detail: "the plan itself"}, {Label: "develop"}}, branchItems)
	assert.Equal(t, "prov", initial, "the name starts as the plan's own")
	data, err := os.ReadFile(provFile(h))
	require.NoError(t, err)
	assert.Contains(t, string(data), "dev-lab:")
	assert.Contains(t, string(data), "plan: PROJ-PROV\n")
	assert.Contains(t, string(data), `branch: "develop"`)
}

// Without projects: configured the plan list refuses, as bam plan list does,
// instead of listing every plan on the server.
func TestPresetAddGuideWithoutProjectsIsUsageError(t *testing.T) {
	h := newHarness(t)
	h.tty = true
	require.NoError(t, os.WriteFile(filepath.Join(h.root, ".bam.yaml"), []byte("version: 1\nservers:\n  work:\n    url: https://bamboo.example.com\n"), 0o644))
	h.env.Choose = func(context.Context, io.Reader, io.Writer, string, []tui.Choice) (int, error) {
		t.Fatal("a plan list opened")
		return 0, nil
	}
	assert.Equal(t, 2, h.run("preset", "add"))
	assert.Contains(t, h.stderr.String(), "no projects configured")
	assert.Contains(t, h.stderr.String(), "projects on work: PROJ")
	assert.Equal(t, 0, h.run("preset", "add", "mine", "--plan", "PROJ-PROV", "--no-tui"), h.stderr.String())
}

// Only what is missing is asked: a name and --plan skip the plan list, and
// a plan with the name missing asks for the name alone.
func TestPresetAddAsksOnlyForWhatIsMissing(t *testing.T) {
	h := newHarness(t)
	h.tty = true
	h.env.Choose = func(context.Context, io.Reader, io.Writer, string, []tui.Choice) (int, error) {
		t.Fatal("the plan or branch was asked")
		return 0, nil
	}
	h.env.Ask = func(_ context.Context, _ io.Reader, _ io.Writer, _, init string) (string, error) { return init, nil }
	formScript(h, func(*app.PresetForm) {}, nil)
	require.Equal(t, 0, h.run("preset", "add", "--plan", "PROJ-PROV"), h.stderr.String())
	assert.Contains(t, h.stdout.String(), "added preset prov to ")
}

func TestPresetAddGuideCancelWritesNothing(t *testing.T) {
	h := newHarness(t)
	h.tty = true
	h.env.Choose = func(context.Context, io.Reader, io.Writer, string, []tui.Choice) (int, error) {
		return -1, context.Canceled
	}
	assert.Equal(t, 130, h.run("preset", "add"))
	assert.Contains(t, h.stderr.String(), "nothing written")
}

// Without a terminal a missing name is the usage error it always was.
func TestPresetAddWithoutANameOffATerminalIsAUsageError(t *testing.T) {
	h := newHarness(t)
	assert.Equal(t, 2, h.run("preset", "add"))
	assert.Contains(t, h.stderr.String(), "missing <name>")
}
