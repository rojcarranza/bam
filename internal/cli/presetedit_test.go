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
