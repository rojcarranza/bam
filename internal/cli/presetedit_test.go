package cli

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/rojcarranza/bam/internal/app"
	"github.com/rojcarranza/bam/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sharedFile(h *harness) string { return filepath.Join(h.root, ".bam.yaml") }

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(b)
}

// editScript replaces the edit form with edit, which stands in for the person.
// It returns every form offered.
func editScript(h *harness, edit func(*app.PresetForm), err error) *[]app.PresetForm {
	var offered []app.PresetForm
	h.env.EditPreset = func(_ context.Context, _ io.Reader, _ io.Writer, f app.PresetForm, _ func(context.Context) []app.RecentVar) (app.PresetForm, error) {
		offered = append(offered, f)
		if err != nil {
			return f, err
		}
		edit(&f)
		return f, nil
	}
	return &offered
}

func setField(f *app.PresetForm, key, value string) {
	for i := range f.Fields {
		if f.Fields[i].Key == key {
			f.Fields[i].Value = value
		}
	}
}

// A flag edit changes one line and keeps the options and required lists the
// preset already had.
func TestPresetEditVarChangesOneLine(t *testing.T) {
	h := newHarness(t)
	before := readFile(t, sharedFile(h))
	require.Equal(t, 0, h.run("preset", "edit", "provision-lab", "--var", "cluster_type=dcos"), h.stderr.String())
	assert.Contains(t, h.stdout.String(), `edited preset provision-lab in `+sharedFile(h))
	assert.Contains(t, h.stdout.String(), `variables.cluster_type: "k8s" -> "dcos"`)
	after := readFile(t, sharedFile(h))
	assert.Contains(t, after, `cluster_type: "dcos"`)
	assert.Contains(t, after, "options:")
	assert.Contains(t, after, "required: [cluster_name]")
	assert.Contains(t, after, "  build:\n    plan: PROJ-BUILD")
	assert.NotEqual(t, before, after)
	// The preset still loads and shows the new value.
	require.Equal(t, 0, h.run("preset", "show", "provision-lab"), h.stderr.String())
}

func TestPresetEditDryRunJSONWritesNothing(t *testing.T) {
	h := newHarness(t)
	before := readFile(t, sharedFile(h))
	require.Equal(t, 0, h.run("preset", "edit", "provision-lab", "--var", "cluster_type=dcos", "--unset", "nothing", "--dry-run", "--json"), h.stderr.String())
	assert.Equal(t, before, readFile(t, sharedFile(h)))
	var doc map[string]any
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &doc))
	assert.Equal(t, "provision-lab", doc["preset"])
	assert.Equal(t, true, doc["dry_run"])
	assert.Equal(t, true, doc["changed"])
	assert.Equal(t, []any{map[string]any{"field": "variables.cluster_type", "old": "k8s", "new": "dcos"}}, doc["changes"])
}

func TestPresetEditJSONNoChangeIsAnEmptyList(t *testing.T) {
	h := newHarness(t)
	require.Equal(t, 0, h.run("preset", "edit", "provision-lab", "--var", "cluster_type=k8s", "--json"), h.stderr.String())
	assert.JSONEq(t, `{"preset":"provision-lab","file":"`+sharedFile(h)+`","dry_run":false,"changed":false,"changes":[]}`, h.stdout.String())
}

func TestPresetEditWithoutAChangeOrATerminalIsAUsageError(t *testing.T) {
	h := newHarness(t)
	assert.Equal(t, 2, h.run("preset", "edit", "provision-lab"))
	assert.Contains(t, h.stderr.String(), "has nothing to change")
	h = newHarness(t)
	assert.Equal(t, 2, h.run("preset", "edit"))
	assert.Contains(t, h.stderr.String(), "needs a preset name")
}

func TestPresetEditRefusals(t *testing.T) {
	for name, args := range map[string][]string{
		"literal secret":    {"--var", "db_password=hunter2"},
		"value not allowed": {"--var", "cluster_type=nomad"},
		"name taken":        {"--rename", "build"},
		"bad timeout":       {"--timeout", "soon"},
		"var and unset":     {"--var", "a=1", "--unset", "a"},
		"var without value": {"--var", "novalue"},
		"unknown preset":    {"--var", "a=1"},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			before := readFile(t, sharedFile(h))
			target := "provision-lab"
			if name == "unknown preset" {
				target = "nope"
			}
			assert.Equal(t, 2, h.run(append([]string{"preset", "edit", target}, args...)...), h.stderr.String())
			assert.Equal(t, before, readFile(t, sharedFile(h)))
		})
	}
}

func TestPresetEditRenameKeepsTheBody(t *testing.T) {
	h := newHarness(t)
	require.Equal(t, 0, h.run("preset", "edit", "provision-lab", "--rename", "lab-two"), h.stderr.String())
	assert.Contains(t, readFile(t, sharedFile(h)), "  lab-two:\n    plan: PROJ-PROV")
	assert.Equal(t, 0, h.run("preset", "show", "lab-two"), h.stderr.String())
	assert.Equal(t, 2, h.run("preset", "show", "provision-lab"))
}

// A rename also renames the preset in the stages of the sets and in the bam
// top pins of the project and machine files, so no set or pin is left naming
// a preset that is gone.
func TestPresetEditRenameUpdatesSetsAndPins(t *testing.T) {
	h := setHarness(t, provider.StateSuccess)
	path := sharedFile(h)
	require.NoError(t, os.WriteFile(path, []byte(readFile(t, path)+"monitor:\n  main: [provision-lab, PROJ-PLAN]\n"), 0o644))
	machine := h.env.Paths.MachineConfig
	require.NoError(t, os.MkdirAll(filepath.Dir(machine), 0o755))
	require.NoError(t, os.WriteFile(machine, []byte("version: 1\nsets:\n  mine:\n    stages:\n      - [provision-lab]\nmonitor:\n  extra: [provision-lab]\n"), 0o644))

	require.Equal(t, 0, h.run("preset", "edit", "provision-lab", "--rename", "lab-two"), h.stderr.String())
	for _, want := range []string{"name: \"provision-lab\" -> \"lab-two\"", "sets.release: \"provision-lab\" -> \"lab-two\"", "monitor.main:", "sets.mine:", "monitor.extra:"} {
		assert.Contains(t, h.stdout.String(), want)
	}
	project := readFile(t, path)
	assert.Contains(t, project, "target: lab-two")
	assert.Contains(t, project, "main: [lab-two, PROJ-PLAN]")
	assert.NotContains(t, project, "provision-lab")
	assert.NotContains(t, readFile(t, machine), "provision-lab")
	assert.NotContains(t, h.stderr.String(), "still pinned", "the pins moved with the name")

	h.stdout.Reset()
	assert.Equal(t, 0, h.run("set", "run", "release", "--dry-run"), h.stderr.String())
}

// A stage that reaches the preset through an alias bam cannot see is refused,
// so no set is left naming a preset that is gone.
func TestPresetEditRenameRefusesAStageReachedThroughAnAlias(t *testing.T) {
	h := newHarness(t)
	path := sharedFile(h)
	require.NoError(t, os.WriteFile(path, []byte(readFile(t, path)+"  holder:\n    plan: PROJ-BUILD\n    required: &r [provision-lab]\nsets:\n  train:\n    stages:\n      - *r\n"), 0o644))
	before := readFile(t, path)
	assert.Equal(t, 2, h.run("preset", "edit", "provision-lab", "--rename", "lab-two"), h.stderr.String())
	assert.Contains(t, h.stderr.String(), `set "train"`)
	assert.Contains(t, h.stderr.String(), "alias")
	assert.Equal(t, before, readFile(t, path))
}

// --dry-run lists the set and pin renames and writes nothing.
func TestPresetEditRenameDryRunListsSetsAndPins(t *testing.T) {
	h := setHarness(t, provider.StateSuccess)
	path := sharedFile(h)
	before := readFile(t, path)
	require.Equal(t, 0, h.run("preset", "edit", "provision-lab", "--rename", "lab-two", "--dry-run", "--json"), h.stderr.String())
	var doc struct {
		Changes []struct{ Field, Old, New string } `json:"changes"`
	}
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &doc))
	assert.Contains(t, doc.Changes, struct{ Field, Old, New string }{"sets.release", "provision-lab", "lab-two"})
	assert.Equal(t, before, readFile(t, path))
}

// A pin that carries a YAML anchor stops the whole rename, before any file is
// written.
func TestPresetEditRenameRefusesAPinAnAliasSharesAndWritesNothing(t *testing.T) {
	h := newHarness(t)
	path := sharedFile(h)
	require.NoError(t, os.WriteFile(path, []byte(readFile(t, path)+"monitor:\n  g: [&n provision-lab]\n"), 0o644))
	before := readFile(t, path)
	assert.Equal(t, 2, h.run("preset", "edit", "provision-lab", "--rename", "lab-two"), h.stderr.String())
	assert.Contains(t, h.stderr.String(), "YAML anchor")
	assert.Contains(t, h.stderr.String(), "try: spell it out in "+path)
	assert.Equal(t, before, readFile(t, path))
}

// The form opens filled with the saved preset and lists the plan variables it
// leaves out; only what the person changes is written.
func TestPresetEditFormWritesOnlyWhatChanged(t *testing.T) {
	h := newHarness(t)
	h.tty = true
	offered := editScript(h, func(f *app.PresetForm) {
		for i := range f.Rows {
			if f.Rows[i].Name == "cluster_name" {
				f.Rows[i].Value = "beta"
			}
		}
		setField(f, "branch", "main")
	}, nil)
	require.Equal(t, 0, h.run("preset", "edit", "provision-lab"), h.stderr.String())

	require.Len(t, *offered, 1)
	f := (*offered)[0]
	assert.True(t, f.Editing)
	assert.Equal(t, sharedFile(h), f.Path)
	require.Len(t, f.Rows, 2)
	assert.Equal(t, "cluster_type", f.Rows[0].Name)
	assert.True(t, f.Rows[0].InPreset)
	assert.Equal(t, []string{"k8s", "dcos"}, f.Rows[0].Choices, "choices are the preset's options")
	assert.Equal(t, "cluster_name", f.Rows[1].Name)
	assert.False(t, f.Rows[1].InPreset, "the plan declares it, the preset leaves it out")

	after := readFile(t, sharedFile(h))
	assert.Contains(t, after, `cluster_name: "beta"`)
	assert.Contains(t, after, `branch: "main"`)
	assert.Contains(t, after, `cluster_type: k8s`)
}

func TestPresetEditFormEscWritesNothing(t *testing.T) {
	h := newHarness(t)
	h.tty = true
	before := readFile(t, sharedFile(h))
	editScript(h, nil, context.Canceled)
	assert.Equal(t, 130, h.run("preset", "edit", "provision-lab"))
	assert.Equal(t, before, readFile(t, sharedFile(h)))
	assert.Contains(t, h.stderr.String(), "nothing written")
}

func TestPresetEditFormUnchangedWritesNothing(t *testing.T) {
	h := newHarness(t)
	h.tty = true
	before := readFile(t, sharedFile(h))
	editScript(h, func(*app.PresetForm) {}, nil)
	require.Equal(t, 0, h.run("preset", "edit", "provision-lab"), h.stderr.String())
	assert.Contains(t, h.stdout.String(), "nothing changed")
	assert.Equal(t, before, readFile(t, sharedFile(h)))
}

func TestPresetEditWithoutANamePicks(t *testing.T) {
	h := newHarness(t)
	h.tty = true
	editScript(h, func(f *app.PresetForm) { setField(f, "required", "cluster_name, cluster_type") }, nil)
	offered := pickScript(h, "provision-lab", nil)
	require.Equal(t, 0, h.run("preset", "edit"), h.stderr.String())
	assert.NotEmpty(t, *offered)
	assert.Contains(t, readFile(t, sharedFile(h)), `required: ["cluster_name", "cluster_type"]`)
}

// A preset can live in .bam.yaml and in your presets directory; bam merges
// them and the edit writes the presets file, checked as merged.
func twoFilePreset(t *testing.T, h *harness) (lower, top string) {
	t.Helper()
	lower, top = sharedFile(h), filepath.Join(h.home, "bam", "presets", "lab.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(top), 0o755))
	require.NoError(t, os.WriteFile(top, []byte("presets:\n  provision-lab:\n    variables:\n      cluster_name: beta\n"), 0o600))
	return lower, top
}

func TestPresetEditAMergedPresetIsCheckedAsMerged(t *testing.T) {
	h := newHarness(t)
	lower, top := twoFilePreset(t, h)
	beforeLower, beforeTop := readFile(t, lower), readFile(t, top)

	// The options live in .bam.yaml: writing nomad to the presets file would break every command.
	assert.Equal(t, 2, h.run("preset", "edit", "provision-lab", "--var", "cluster_type=nomad"), h.stderr.String())
	assert.Equal(t, beforeTop, readFile(t, top))

	// The variable is in .bam.yaml: removing it from the presets file does nothing, so bam says so.
	h.stderr.Reset()
	assert.Equal(t, 2, h.run("preset", "edit", "provision-lab", "--unset", "cluster_type"))
	assert.Contains(t, h.stderr.String(), lower)
	assert.NotContains(t, h.stdout.String(), "already has these values")
	assert.Equal(t, beforeTop, readFile(t, top))

	h.stderr.Reset()
	require.Equal(t, 0, h.run("preset", "edit", "provision-lab", "--var", "cluster_type=dcos"), h.stderr.String())
	assert.Contains(t, readFile(t, top), `cluster_type: "dcos"`)
	assert.Equal(t, beforeLower, readFile(t, lower), "the lower file is not touched by a value edit")
	require.Equal(t, 0, h.run("preset", "show", "provision-lab"), h.stderr.String())
}

func TestPresetEditRenameRenamesEveryFileThatDefinesIt(t *testing.T) {
	h := newHarness(t)
	lower, top := twoFilePreset(t, h)
	require.Equal(t, 0, h.run("preset", "edit", "provision-lab", "--rename", "lab-two"), h.stderr.String())
	assert.Contains(t, readFile(t, lower), "  lab-two:\n    plan: PROJ-PROV")
	assert.Contains(t, readFile(t, top), "  lab-two:\n")
	require.Equal(t, 0, h.run("preset", "show", "lab-two"), h.stderr.String())
}

// A refusal bam knows before the form opens is given before it opens.
func TestPresetEditAnchoredPresetIsRefusedBeforeTheForm(t *testing.T) {
	h := newHarness(t)
	h.tty = true
	path := sharedFile(h)
	require.NoError(t, os.WriteFile(path, []byte(readFile(t, path)+"  anchored: &a\n    plan: PROJ-PROV\n    branch: develop\n"), 0o644))
	offered := editScript(h, func(*app.PresetForm) {}, nil)
	assert.Equal(t, 2, h.run("preset", "edit", "anchored"), h.stderr.String())
	assert.Contains(t, h.stderr.String(), "anchors")
	assert.Empty(t, *offered, "the form never opened")
}

// A set that uses the preset no longer blocks its rename; a copy in a file
// that cannot be renamed in place does, with what to try.
func TestPresetEditFormCarriesTheRenameRules(t *testing.T) {
	h := newHarness(t)
	h.tty = true
	path := sharedFile(h)
	require.NoError(t, os.WriteFile(path, []byte(readFile(t, path)+"sets:\n  train:\n    stages:\n      - [provision-lab]\n"), 0o644))
	offered := editScript(h, func(*app.PresetForm) {}, nil)
	require.Equal(t, 0, h.run("preset", "edit", "provision-lab"), h.stderr.String())
	f := (*offered)[0]
	assert.Contains(t, f.TakenNames, "build")
	assert.Empty(t, f.RenameBlocked, "the set is renamed with the preset")

	_, top := twoFilePreset(t, h)
	require.NoError(t, os.WriteFile(top, []byte("presets:\n  provision-lab: &p\n    variables:\n      cluster_name: beta\n"), 0o600))
	h.stdout.Reset()
	h.stderr.Reset()
	require.NotEqual(t, 0, h.run("preset", "edit", "provision-lab"))
	assert.Contains(t, h.stderr.String(), "anchors")
}

// What another file holds reaches the form, so a removal it cannot make is
// refused while the form is open.
func TestPresetEditFormCarriesWhatAnotherFileHolds(t *testing.T) {
	h := newHarness(t)
	h.tty = true
	lower, top := twoFilePreset(t, h)
	_ = top
	offered := editScript(h, func(*app.PresetForm) {}, nil)
	require.Equal(t, 0, h.run("preset", "edit", "provision-lab"), h.stderr.String())
	f := (*offered)[0]
	assert.Equal(t, lower, f.Held.Only["variables.cluster_type"])
	assert.Equal(t, lower, f.Held.Only["branch"])
	assert.NotContains(t, f.Held.Only, "variables.cluster_name", "the presets file holds that one itself")
	assert.NoError(t, f.Validate())
	for i := range f.Rows {
		if f.Rows[i].Name == "cluster_type" {
			f.Rows[i].Removed = true
		}
	}
	assert.ErrorContains(t, f.Validate(), lower)
}

func TestPresetEditFormShowsALiteralSecretMasked(t *testing.T) {
	h := newHarness(t)
	h.tty = true
	path := filepath.Join(h.home, "bam", "presets", "lit.yaml") // only presets files may hold a literal secret
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("presets:\n  with-secret:\n    plan: PROJ-PROV\n    variables:\n      api_secret: hunter2\n"), 0o600))
	offered := editScript(h, func(*app.PresetForm) {}, nil)
	require.Equal(t, 0, h.run("preset", "edit", "with-secret"), h.stderr.String())
	var row *app.FormRow
	for i := range (*offered)[0].Rows {
		if (*offered)[0].Rows[i].Name == "api_secret" {
			row = &(*offered)[0].Rows[i]
		}
	}
	require.NotNil(t, row)
	assert.True(t, row.Literal)
	assert.NotContains(t, h.stdout.String()+h.stderr.String(), "hunter2")
}

// Preset names are shared by every file: a name another file holds is taken.
func TestPresetEditRenameToANameAnotherFileHoldsIsRefused(t *testing.T) {
	h := newHarness(t)
	other := filepath.Join(h.home, "bam", "presets", "other.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(other), 0o755))
	require.NoError(t, os.WriteFile(other, []byte("presets:\n  lab-x:\n    plan: PROJ-BUILD\n"), 0o600))
	before := readFile(t, sharedFile(h))
	assert.Equal(t, 2, h.run("preset", "edit", "provision-lab", "--rename", "lab-x"), h.stderr.String())
	assert.Contains(t, h.stderr.String(), `preset "lab-x" already exists`)
	assert.Equal(t, before, readFile(t, sharedFile(h)))
}

// Replacing a hand-written literal secret with a reference never prints the old value.
func TestPresetEditNeverPrintsALiteralSecret(t *testing.T) {
	h := newHarness(t)
	path := filepath.Join(h.home, "bam", "presets", "lit.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("presets:\n  with-secret:\n    plan: PROJ-PROV\n    variables:\n      api_secret: hunter2\n"), 0o600))
	require.Equal(t, 0, h.run("preset", "edit", "with-secret", "--var", "api_secret=${API_SECRET}"), h.stderr.String())
	assert.NotContains(t, h.stdout.String()+h.stderr.String(), "hunter2")
	assert.Contains(t, h.stdout.String(), `"********" -> "${API_SECRET}"`)
	assert.NotContains(t, readFile(t, path), "hunter2")
}

// When both files hold a value, removing it from the presets file lets the
// other file's value apply, and the text output says so; JSON keeps its fields.
func TestPresetEditRemovalSaysWhichOtherFileValueApplies(t *testing.T) {
	h := newHarness(t)
	lower, top := twoFilePreset(t, h)
	require.NoError(t, os.WriteFile(top, []byte("presets:\n  provision-lab:\n    branch: main\n    variables:\n      cluster_type: dcos\n"), 0o600))

	require.Equal(t, 0, h.run("preset", "edit", "provision-lab", "--unset", "cluster_type", "--branch", "", "--dry-run"), h.stderr.String())
	assert.Contains(t, h.stdout.String(), `variables.cluster_type: removed (was "dcos"); the value in `+lower+` applies`)
	assert.Contains(t, h.stdout.String(), `branch: removed (was "main"); the value in `+lower+` applies`)

	h.stdout.Reset()
	require.Equal(t, 0, h.run("preset", "edit", "provision-lab", "--unset", "cluster_type", "--dry-run", "--json"), h.stderr.String())
	assert.NotContains(t, h.stdout.String(), "applies")
}

// --repository sets a repository's branch, an empty one removes it, and the
// next repository in the file stays.
func TestPresetEditRepositoryFlags(t *testing.T) {
	h := newHarness(t)
	path := sharedFile(h)
	require.NoError(t, os.WriteFile(path, []byte(readFile(t, path)+"  with-repos:\n    plan: PROJ-PROV\n    branch: develop\n    repositories:\n      api: main\n      web: main\n      docs: main\n"), 0o644))
	require.Equal(t, 0, h.run("preset", "edit", "with-repos", "--repository", "api=", "--repository", "web=dev", "--repository", "new=x"), h.stderr.String())
	assert.Contains(t, h.stdout.String(), `repositories.api: removed (was "main")`)
	after := readFile(t, path)
	assert.NotContains(t, after, "api:")
	assert.Contains(t, after, `web: "dev"`)
	assert.Contains(t, after, "docs: main", "the entry after a removed one is kept")
	assert.Contains(t, after, `new: "x"`)

	h.stdout.Reset()
	assert.Equal(t, 2, h.run("preset", "edit", "with-repos", "--repository", "nobranch"))
	assert.Contains(t, h.stderr.String(), "is not name=branch")
}

func TestPresetEditRequiredAndOptionFlags(t *testing.T) {
	h := newHarness(t)
	path := sharedFile(h)
	require.Equal(t, 0, h.run("preset", "edit", "provision-lab", "--required", "cluster_name,cluster_type", "--option", "cluster_type=k8s,dcos,nomad"), h.stderr.String())
	after := readFile(t, path)
	assert.Contains(t, after, `required: ["cluster_name", "cluster_type"]`)
	assert.Contains(t, after, `cluster_type: ["k8s", "dcos", "nomad"]`)

	require.Equal(t, 0, h.run("preset", "edit", "provision-lab", "--required", "", "--option", "cluster_type="), h.stderr.String())
	after = readFile(t, path)
	assert.NotContains(t, after, "required:")
	assert.NotContains(t, after, "options:")

	assert.Equal(t, 2, h.run("preset", "edit", "provision-lab", "--option", "cluster_type"))
	assert.Contains(t, h.stderr.String(), "is not name=a,b")
}

// A repository only a lower file holds is not removed by a flag either.
func TestPresetEditRepositoryFlagRefusesWhatAnotherFileHolds(t *testing.T) {
	h := newHarness(t)
	lower, top := twoFilePreset(t, h)
	require.NoError(t, os.WriteFile(lower, []byte(readFile(t, lower)+"  low:\n    plan: PROJ-PROV\n    branch: develop\n    repositories:\n      api: main\n"), 0o644))
	require.NoError(t, os.WriteFile(top, []byte("presets:\n  low:\n    variables:\n      x: \"1\"\n"), 0o600))
	before := readFile(t, top)
	assert.Equal(t, 2, h.run("preset", "edit", "low", "--repository", "api="))
	assert.Contains(t, h.stderr.String(), lower)
	assert.Equal(t, before, readFile(t, top))
}

// The form is given a way to read the repositories of the branch typed in it.
func TestPresetEditFormCanReadRepositoriesOfANewBranch(t *testing.T) {
	h := newHarness(t)
	h.tty = true
	built := provider.Build{Key: "PROJ-PROV12-9", PlanKey: "PROJ-PROV12", Number: 9, State: provider.StateSuccess,
		Stages: []provider.Stage{{Name: "Apply", Jobs: []provider.Job{{Key: "PROJ-PROV12-TF-9", Branches: map[string]string{"infra": "develop"}}}}}}
	h.fake.History["PROJ-PROV12"] = []provider.Build{built}
	var got []app.RepoBranch
	var readErr error
	editScript(h, func(f *app.PresetForm) {
		require.NotNil(t, f.ReadRepos)
		got, readErr = f.ReadRepos(context.Background(), "develop")
		f.UseRepositories(got)
	}, nil)
	require.Equal(t, 0, h.run("preset", "edit", "provision-lab"), h.stderr.String())
	require.NoError(t, readErr)
	assert.Equal(t, []app.RepoBranch{{Name: "infra", Branch: "develop"}}, got)
	assert.Contains(t, readFile(t, sharedFile(h)), `infra: "develop"`)
}
