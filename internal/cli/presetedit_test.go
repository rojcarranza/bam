package cli

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/rojcarranza/bam/internal/app"
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

// A set that runs the preset would fail once its name is gone.
func TestPresetEditRefusesToRenameAPresetASetUses(t *testing.T) {
	h := newHarness(t)
	path := sharedFile(h)
	require.NoError(t, os.WriteFile(path, []byte(readFile(t, path)+"sets:\n  train:\n    stages:\n      - [provision-lab]\n"), 0o644))
	before := readFile(t, path)
	assert.Equal(t, 2, h.run("preset", "edit", "provision-lab", "--rename", "lab-two"), h.stderr.String())
	assert.Contains(t, h.stderr.String(), `used by set "train"`)
	assert.Contains(t, h.stderr.String(), "try: change the set first, or leave the name as it is")
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

func TestPresetEditFormCarriesTheRenameRules(t *testing.T) {
	h := newHarness(t)
	h.tty = true
	path := sharedFile(h)
	require.NoError(t, os.WriteFile(path, []byte(readFile(t, path)+"sets:\n  train:\n    stages:\n      - [provision-lab]\n"), 0o644))
	offered := editScript(h, func(*app.PresetForm) {}, nil)
	require.Equal(t, 0, h.run("preset", "edit", "provision-lab"), h.stderr.String())
	f := (*offered)[0]
	assert.Contains(t, f.TakenNames, "build")
	assert.Contains(t, f.RenameBlocked, `used by set "train"`)
	assert.Contains(t, f.RenameBlocked, "change the set first", "the form says what to try")
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
