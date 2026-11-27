package cli

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	assert.Contains(t, h.stderr.String(), "--unset, --branch, --repository, --required, --option, --timeout, --rename")
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
// top pins of the project file, and in the sets under this repository's entry
// of the machine file, so none is left naming a preset that is gone.
func TestPresetEditRenameUpdatesSetsAndPins(t *testing.T) {
	h := setHarness(t, provider.StateSuccess)
	path := sharedFile(h)
	require.NoError(t, os.WriteFile(path, []byte(readFile(t, path)+"monitor:\n  main: [provision-lab, PROJ-PLAN]\n"), 0o644))
	machine := h.env.Paths.MachineConfig
	require.NoError(t, os.MkdirAll(filepath.Dir(machine), 0o755))
	require.NoError(t, os.WriteFile(machine, []byte("version: 1\nrepos:\n  "+h.root+":\n    sets:\n      mine:\n        stages:\n          - [provision-lab]\n"), 0o644))

	require.Equal(t, 0, h.run("preset", "edit", "provision-lab", "--rename", "lab-two"), h.stderr.String())
	for _, want := range []string{"name: \"provision-lab\" -> \"lab-two\"", "sets.release: \"provision-lab\" -> \"lab-two\"", "monitor.main:", "sets.mine:"} {
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

// The machine file's own sets and pins apply in every repository, so a preset
// only this repository defines is not renamed under them: the rename is
// refused, naming the set to change first, and nothing is written.
func TestPresetEditRenameRefusesMachineWideSetsOfARepositoryOnlyPreset(t *testing.T) {
	h := setHarness(t, provider.StateSuccess)
	machine := h.env.Paths.MachineConfig
	require.NoError(t, os.MkdirAll(filepath.Dir(machine), 0o755))
	body := "version: 1\nsets:\n  mine:\n    stages:\n      - [provision-lab]\n"
	require.NoError(t, os.WriteFile(machine, []byte(body), 0o644))
	before := readFile(t, sharedFile(h))
	assert.Equal(t, 2, h.run("preset", "edit", "provision-lab", "--rename", "lab-two"))
	assert.Contains(t, h.stderr.String(), "sets.mine")
	assert.Contains(t, h.stderr.String(), "applies in every repository")
	assert.Equal(t, before, readFile(t, sharedFile(h)))
	assert.Equal(t, body, readFile(t, machine))
}

// A top-level pin alone refuses the rename of a preset only this repository
// defines; the hint names bam's pin commands and no stand-in name.
func TestPresetEditRenameRefusesATopLevelPinOfARepositoryOnlyPreset(t *testing.T) {
	h := setHarness(t, provider.StateSuccess)
	machine := h.env.Paths.MachineConfig
	require.NoError(t, os.MkdirAll(filepath.Dir(machine), 0o755))
	body := "version: 1\nmonitor:\n  extra: [provision-lab]\n"
	require.NoError(t, os.WriteFile(machine, []byte(body), 0o644))
	assert.Equal(t, 2, h.run("preset", "edit", "provision-lab", "--rename", "lab-two"))
	assert.Contains(t, h.stderr.String(), "run bam pin rm provision-lab, rename the preset, then run bam pin add extra with the new name")
	assert.NotContains(t, h.stderr.String(), "-renamed")
	assert.Equal(t, body, readFile(t, machine))
}

// A shared preset cannot take a name another repository's own .bam.yaml
// defines, where bam knows that repository from a recorded set run.
func TestPresetEditSharedRenameRefusesANameAnotherRepositorysProjectFileDefines(t *testing.T) {
	h := newHarness(t)
	twoFilePreset(t, h)
	repoB := filepath.Join(h.home, "repo-b")
	require.NoError(t, os.MkdirAll(repoB, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repoB, ".bam.yaml"), []byte("version: 1\ntargets:\n  lab-three:\n    plan: PROJ-B\n"), 0o644))
	train := filepath.Join(h.home, "b.yaml")
	require.NoError(t, os.WriteFile(train, []byte("stages:\n  - [provision-lab]\n"), 0o644))
	saveRun(t, h, repoB, train, 1)
	before := readFile(t, sharedFile(h))
	assert.Equal(t, 2, h.run("preset", "edit", "provision-lab", "--rename", "lab-three"))
	assert.Contains(t, h.stderr.String(), "already exists in the repository "+repoB)
	assert.Equal(t, before, readFile(t, sharedFile(h)))
	assert.Equal(t, "stages:\n  - [provision-lab]\n", readFile(t, train))
}

// A machine entry that holds only sets still makes its repository known, so a
// shared preset cannot take a name that repository's .bam.yaml defines.
func TestPresetEditSharedRenameRefusesANameOfASetsOnlyEntrysProjectFile(t *testing.T) {
	h := newHarness(t)
	twoFilePreset(t, h)
	repoB := filepath.Join(h.home, "repo-b")
	require.NoError(t, os.MkdirAll(repoB, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repoB, ".bam.yaml"), []byte("version: 1\ntargets:\n  lab-three:\n    plan: PROJ-B\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Dir(h.env.Paths.MachineConfig), 0o755))
	require.NoError(t, os.WriteFile(h.env.Paths.MachineConfig, []byte("version: 1\nrepos:\n  "+repoB+":\n    sets:\n      nightly: [[provision-lab]]\n"), 0o644))
	before := readFile(t, sharedFile(h))
	machine := readFile(t, h.env.Paths.MachineConfig)
	assert.Equal(t, 2, h.run("preset", "edit", "provision-lab", "--rename", "lab-three"))
	assert.Contains(t, h.stderr.String(), "already exists in the repository "+repoB)
	assert.Equal(t, before, readFile(t, sharedFile(h)))
	assert.Equal(t, machine, readFile(t, h.env.Paths.MachineConfig))
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

	lower, _ := twoFilePreset(t, h)
	require.NoError(t, os.WriteFile(lower, []byte(strings.Replace(readFile(t, lower), "  provision-lab:\n", "  provision-lab: &p\n", 1)), 0o644))
	*offered = nil
	require.Equal(t, 0, h.run("preset", "edit", "provision-lab"), h.stderr.String())
	require.Len(t, *offered, 1, "the anchored lower copy does not stop the form from opening")
	assert.Contains(t, (*offered)[0].RenameBlocked, "anchors")
	assert.Contains(t, (*offered)[0].RenameBlocked, "spell the preset out in "+lower, "the form says what to try")
}

// A pin that reaches the name through an alias is refused inside the form,
// so a rename typed there is not lost to a refusal after ctrl-s.
func TestPresetEditFormCarriesAPinAliasRefusal(t *testing.T) {
	h := newHarness(t)
	h.tty = true
	path := sharedFile(h)
	require.NoError(t, os.WriteFile(path, []byte(readFile(t, path)+"monitor:\n  g: *r\n"), 0o644))
	data := strings.Replace(readFile(t, path), "  build:\n    plan: PROJ-BUILD\n", "  build:\n    plan: PROJ-BUILD\n    required: &r [provision-lab]\n", 1)
	require.NoError(t, os.WriteFile(path, []byte(data), 0o644))
	offered := editScript(h, func(*app.PresetForm) {}, nil)
	require.Equal(t, 0, h.run("preset", "edit", "provision-lab"), h.stderr.String())
	require.Len(t, *offered, 1)
	assert.Contains(t, (*offered)[0].RenameBlocked, `monitor group "g"`)
	assert.Contains(t, (*offered)[0].RenameBlocked, "spell the pins out in "+path)
}

// The text output says which file each set and pin rename lands in; a dry run
// names the machine file before anything is written.
func TestPresetEditRenameOutputNamesEachFile(t *testing.T) {
	h := setHarness(t, provider.StateSuccess)
	machine := h.env.Paths.MachineConfig
	require.NoError(t, os.MkdirAll(filepath.Dir(machine), 0o755))
	require.NoError(t, os.WriteFile(machine, []byte("version: 1\nrepos:\n  "+h.root+":\n    sets:\n      extra: [[provision-lab]]\n"), 0o644))
	require.Equal(t, 0, h.run("preset", "edit", "provision-lab", "--rename", "lab-two", "--dry-run"), h.stderr.String())
	assert.Contains(t, h.stdout.String(), `sets.release: "provision-lab" -> "lab-two" (in `+sharedFile(h)+")")
	assert.Contains(t, h.stdout.String(), `sets.extra: "provision-lab" -> "lab-two" (in `+machine+")")
	assert.Equal(t, 0, h.run("preset", "edit", "provision-lab", "--rename", "lab-two", "--dry-run", "--json"))
	assert.Contains(t, h.stdout.String(), `"file": "`+machine+`"`, "each set or pin change names the file it lands in")
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

// A rename also renames the preset in each train file a past set run used,
// and --dry-run lists the file without writing it.
func TestPresetEditRenameUpdatesTrainFilesPastRunsUsed(t *testing.T) {
	h := setHarness(t, provider.StateSuccess)
	train := trainPath(t, h)
	require.NoError(t, os.WriteFile(train, []byte("# hotfix train\nstages:\n  - [provision-lab]\n"), 0o644))
	store := &app.SetStore{Dir: h.env.Paths.Sets}
	require.NoError(t, store.Save(app.SetRecord{Version: 1, ID: app.NewSetID("hotfix", fixedNow), Set: "hotfix", File: train,
		Origin: workOrigin, Repo: h.root, StartedAt: fixedNow, Status: app.SetInterrupted, VarNames: []string{}}))

	require.Equal(t, 0, h.run("preset", "edit", "provision-lab", "--rename", "lab-two", "--dry-run"), h.stderr.String())
	assert.Contains(t, h.stdout.String(), "train file")
	assert.Contains(t, h.stdout.String(), "(in "+train+")")
	assert.Contains(t, readFile(t, train), "provision-lab")

	h.stdout.Reset()
	require.Equal(t, 0, h.run("preset", "edit", "provision-lab", "--rename", "lab-two"), h.stderr.String())
	got := readFile(t, train)
	assert.Contains(t, got, "# hotfix train")
	assert.Contains(t, got, "lab-two")
	assert.NotContains(t, got, "provision-lab")
}

// A train file that hides the name behind an anchor stops the rename before
// the project file is written.
func TestPresetEditRenameRefusesAnAnchoredTrainFileAndWritesNothing(t *testing.T) {
	h := setHarness(t, provider.StateSuccess)
	train := trainPath(t, h)
	require.NoError(t, os.WriteFile(train, []byte("stages:\n  - [&n provision-lab]\n"), 0o644))
	store := &app.SetStore{Dir: h.env.Paths.Sets}
	require.NoError(t, store.Save(app.SetRecord{Version: 1, ID: app.NewSetID("hotfix", fixedNow), Set: "hotfix", File: train,
		Origin: workOrigin, Repo: h.root, StartedAt: fixedNow, Status: app.SetInterrupted, VarNames: []string{}}))
	before := readFile(t, sharedFile(h))

	assert.Equal(t, 2, h.run("preset", "edit", "provision-lab", "--rename", "lab-two"), h.stderr.String())
	assert.Contains(t, h.stderr.String(), "YAML anchor")
	assert.Equal(t, before, readFile(t, sharedFile(h)))
	assert.Contains(t, readFile(t, train), "provision-lab")
}

// A preset defined in two files is renamed in both; the text output names the
// lower file too, so a dry run shows every file the rename writes, and the
// JSON keeps one name change.
func TestPresetEditRenameOutputNamesTheLowerFile(t *testing.T) {
	h := newHarness(t)
	lower, top := twoFilePreset(t, h)
	beforeLower := readFile(t, lower)
	require.Equal(t, 0, h.run("preset", "edit", "provision-lab", "--rename", "lab-two", "--dry-run"), h.stderr.String())
	assert.Contains(t, h.stdout.String(), `name: "provision-lab" -> "lab-two" (in `+lower+")")
	assert.Equal(t, beforeLower, readFile(t, lower), "a dry run writes nothing")

	h.stdout.Reset()
	require.Equal(t, 0, h.run("preset", "edit", "provision-lab", "--rename", "lab-two", "--dry-run", "--json"))
	assert.Equal(t, 1, strings.Count(h.stdout.String(), `"field": "name"`), top)
}

// A train file that hides the name behind an anchor is refused inside the
// form, so a rename typed there is not lost to a refusal after ctrl-s.
func TestPresetEditFormCarriesATrainFileRefusal(t *testing.T) {
	h := newHarness(t)
	h.tty = true
	train := filepath.Join(h.home, "train.yaml")
	require.NoError(t, os.WriteFile(train, []byte("stages:\n  - [&n provision-lab]\n"), 0o644))
	store := &app.SetStore{Dir: h.env.Paths.Sets}
	require.NoError(t, store.Save(app.SetRecord{Version: 1, ID: app.NewSetID("hotfix", fixedNow), Set: "hotfix", File: train,
		Origin: workOrigin, Repo: h.root, StartedAt: fixedNow, Status: app.SetInterrupted, VarNames: []string{}}))
	offered := editScript(h, func(*app.PresetForm) {}, nil)
	require.Equal(t, 0, h.run("preset", "edit", "provision-lab"), h.stderr.String())
	require.Len(t, *offered, 1)
	assert.Contains(t, (*offered)[0].RenameBlocked, "train file "+train)
	assert.Contains(t, (*offered)[0].RenameBlocked, "spell it out in "+train)
}

// saveRun records a past set run of repo that used train.
func saveRun(t *testing.T, h *harness, repo, train string, n int) {
	t.Helper()
	store := &app.SetStore{Dir: h.env.Paths.Sets}
	at := fixedNow.Add(time.Duration(n) * time.Minute)
	require.NoError(t, store.Save(app.SetRecord{Version: 1, ID: app.NewSetID("hotfix", at), Set: "hotfix", File: train,
		Origin: workOrigin, Repo: repo, StartedAt: at, Status: app.SetInterrupted, VarNames: []string{}}))
}

// A preset a presets file holds works from any folder, so a train file a run
// started elsewhere is renamed, and the JSON names each file. A preset only
// this repository defines leaves the other folder's train file alone.
func TestPresetEditRenameFollowsASharedPresetIntoOtherFoldersTrainFiles(t *testing.T) {
	h := newHarness(t)
	twoFilePreset(t, h)
	elsewhere := filepath.Join(h.home, "trains", "release.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(elsewhere), 0o755))
	require.NoError(t, os.WriteFile(elsewhere, []byte("stages:\n  - [provision-lab]\n"), 0o644))
	saveRun(t, h, filepath.Join(h.home, "trains"), elsewhere, 1)

	require.Equal(t, 0, h.run("preset", "edit", "provision-lab", "--rename", "lab-two", "--dry-run", "--json"), h.stderr.String())
	var doc struct {
		Changes []struct{ Field, File string } `json:"changes"`
	}
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &doc))
	assert.Contains(t, doc.Changes, struct{ Field, File string }{"train file", elsewhere})
	assert.Contains(t, readFile(t, elsewhere), "provision-lab", "a dry run writes nothing")

	require.Equal(t, 0, h.run("preset", "edit", "provision-lab", "--rename", "lab-two"), h.stderr.String())
	assert.Contains(t, readFile(t, elsewhere), "lab-two")
}

// A repository whose own machine entry defines a preset of the same name keeps
// that entry on the old name, so its train files keep the name, as its sets do.
func TestPresetEditSharedRenameLeavesTrainFilesOfARepositoryWithItsOwnPreset(t *testing.T) {
	h := newHarness(t)
	twoFilePreset(t, h)
	require.NoError(t, os.MkdirAll(filepath.Dir(h.env.Paths.MachineConfig), 0o755))
	require.NoError(t, os.WriteFile(h.env.Paths.MachineConfig, []byte("version: 1\nrepos:\n  /somewhere/else:\n    targets:\n      provision-lab:\n        plan: PROJ-PROV\n"), 0o644))
	own := filepath.Join(h.home, "own.yaml")
	other := filepath.Join(h.home, "other.yaml")
	for _, p := range []string{own, other} {
		require.NoError(t, os.WriteFile(p, []byte("stages:\n  - [provision-lab]\n"), 0o644))
	}
	saveRun(t, h, "/somewhere/else", own, 1)
	saveRun(t, h, filepath.Join(h.home, "trains"), other, 2)
	require.Equal(t, 0, h.run("preset", "edit", "provision-lab", "--rename", "lab-two"), h.stderr.String())
	assert.Contains(t, readFile(t, own), "provision-lab", "that repository's own preset keeps its name")
	assert.Contains(t, readFile(t, other), "lab-two")
}

// A repository whose own .bam.yaml layers a preset of that name keeps it on the
// old name too, so its train files are left as its sets are.
func TestPresetEditSharedRenameLeavesTrainFilesOfARepositoryWhoseProjectFileDefinesIt(t *testing.T) {
	h := newHarness(t)
	twoFilePreset(t, h)
	repoB := filepath.Join(h.home, "repo-b")
	require.NoError(t, os.MkdirAll(repoB, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repoB, ".bam.yaml"), []byte("version: 1\ntargets:\n  provision-lab:\n    variables:\n      cluster_name: gamma\n"), 0o644))
	own, other := filepath.Join(h.home, "b.yaml"), filepath.Join(h.home, "c.yaml")
	for _, p := range []string{own, other} {
		require.NoError(t, os.WriteFile(p, []byte("stages:\n  - [provision-lab]\n"), 0o644))
	}
	saveRun(t, h, repoB, own, 1)
	saveRun(t, h, filepath.Join(h.home, "trains"), other, 2)
	require.Equal(t, 0, h.run("preset", "edit", "provision-lab", "--rename", "lab-two"), h.stderr.String())
	assert.Contains(t, readFile(t, own), "provision-lab")
	assert.Contains(t, readFile(t, other), "lab-two")
}

func TestPresetEditRenameLeavesOtherFoldersTrainFilesOfARepositoryOnlyPreset(t *testing.T) {
	h := newHarness(t)
	elsewhere := filepath.Join(h.home, "trains", "release.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(elsewhere), 0o755))
	require.NoError(t, os.WriteFile(elsewhere, []byte("stages:\n  - [provision-lab]\n"), 0o644))
	saveRun(t, h, filepath.Join(h.home, "trains"), elsewhere, 1)
	require.Equal(t, 0, h.run("preset", "edit", "provision-lab", "--rename", "lab-two"), h.stderr.String())
	assert.Contains(t, readFile(t, elsewhere), "provision-lab", "the name means another preset in that folder")
}

// A store bam cannot read refuses a rename, since every train file would be
// left on the old name unseen; other edits still work.
func TestPresetEditRenameIsRefusedWhenPastRunsCannotBeRead(t *testing.T) {
	h := newHarness(t)
	require.NoError(t, os.MkdirAll(filepath.Dir(h.env.Paths.Sets), 0o755))
	require.NoError(t, os.WriteFile(h.env.Paths.Sets, []byte("not a folder"), 0o644))
	before := readFile(t, sharedFile(h))
	assert.Equal(t, 3, h.run("preset", "edit", "provision-lab", "--rename", "lab-two"))
	assert.Contains(t, h.stderr.String(), "past set runs")
	assert.Equal(t, before, readFile(t, sharedFile(h)))
	h.stderr.Reset()
	assert.Equal(t, 0, h.run("preset", "edit", "provision-lab", "--var", "cluster_name=beta"), h.stderr.String())
}

// When a later write fails, the error says what was written and what was
// not, and the train files, which are written first, are already done.
func TestPresetEditRenameSaysWhatWasWrittenWhenAWriteFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes into a read-only folder")
	}
	h := newHarness(t)
	train := filepath.Join(h.home, "train.yaml")
	require.NoError(t, os.WriteFile(train, []byte("stages:\n  - [provision-lab]\n"), 0o644))
	saveRun(t, h, h.root, train, 1)
	dir := filepath.Dir(sharedFile(h))
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	assert.NotEqual(t, 0, h.run("preset", "edit", "provision-lab", "--rename", "lab-two"))
	assert.Contains(t, h.stderr.String(), "only partly renamed")
	assert.Contains(t, h.stderr.String(), "written: "+train)
	assert.Contains(t, h.stderr.String(), "not written: "+sharedFile(h))
	assert.Contains(t, readFile(t, train), "lab-two")
}

// Each train file is its own write: when the second cannot be written, the
// error already says the first was.
func TestPresetEditRenameNamesEachTrainFileItWroteBeforeAFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes into a read-only folder")
	}
	h := newHarness(t)
	first := filepath.Join(h.home, "a", "train.yaml")
	second := filepath.Join(h.home, "b", "train.yaml")
	for _, p := range []string{first, second} {
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte("stages:\n  - [provision-lab]\n"), 0o644))
	}
	saveRun(t, h, h.root, first, 2)
	saveRun(t, h, h.root, second, 1)
	locked := filepath.Dir(second)
	require.NoError(t, os.Chmod(locked, 0o500))
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	assert.NotEqual(t, 0, h.run("preset", "edit", "provision-lab", "--rename", "lab-two"))
	assert.Contains(t, h.stderr.String(), "only partly renamed")
	assert.Contains(t, h.stderr.String(), "written: "+first)
	assert.Contains(t, h.stderr.String(), "not written: "+second)
	assert.Contains(t, readFile(t, first), "lab-two")
	assert.Contains(t, readFile(t, sharedFile(h)), "provision-lab", "bam's own files are untouched")
}

// A preset only a presets file defines, named by a set in the project file and
// a pin in the machine file: when the machine file fails after the project
// file was written, only the machine file is reported as not written.
func TestPresetEditRenameNamesTheProjectFileWhenTheMachineFileFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes into a read-only folder")
	}
	h := newHarness(t)
	path := sharedFile(h)
	require.NoError(t, os.WriteFile(path, []byte(readFile(t, path)+"sets:\n  solo:\n    stages:\n      - [only-here]\n"), 0o644))
	presets := filepath.Join(h.home, "bam", "presets", "only.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(presets), 0o755))
	require.NoError(t, os.WriteFile(presets, []byte("presets:\n  only-here:\n    plan: PROJ-PROV\n"), 0o600))
	machine := h.env.Paths.MachineConfig
	require.NoError(t, os.MkdirAll(filepath.Dir(machine), 0o755))
	require.NoError(t, os.WriteFile(machine, []byte("version: 1\nmonitor:\n  extra: [only-here]\n"), 0o644))
	dir := filepath.Dir(machine)
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	assert.NotEqual(t, 0, h.run("preset", "edit", "only-here", "--rename", "lab-two"))
	assert.Contains(t, h.stderr.String(), "written: "+presets+", "+path)
	assert.Contains(t, h.stderr.String(), "not written: "+machine)
}

// A file whose preset copy was renamed but whose pins were not yet is not
// reported as done: the machine file keeps a preset and a pin, and the project
// file, whose folder bam cannot write, keeps a set.
func TestPresetEditRenameReportsAFileWithTwoStepsAsPartlyWritten(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes into a read-only folder")
	}
	h := newHarness(t)
	path := sharedFile(h)
	require.NoError(t, os.WriteFile(path, []byte(readFile(t, path)+"sets:\n  solo:\n    stages:\n      - [mach-only]\n"), 0o644))
	machine := h.env.Paths.MachineConfig
	require.NoError(t, os.MkdirAll(filepath.Dir(machine), 0o755))
	require.NoError(t, os.WriteFile(machine, []byte("version: 1\ntargets:\n  mach-only:\n    plan: PROJ-PROV\nmonitor:\n  extra: [mach-only]\n"), 0o644))
	dir := filepath.Dir(path)
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	assert.NotEqual(t, 0, h.run("preset", "edit", "mach-only", "--rename", "lab-two"))
	out := h.stderr.String()
	assert.Contains(t, out, "; written: ; not written: "+path+", "+machine)
	assert.Contains(t, out, "; partly written: "+machine)
}

// Options are printed and saved in clear, so a secret variable takes none; one
// written by hand earlier is still printed masked when it is removed.
func TestPresetEditRefusesOptionsOfASecretAndMasksOldOnes(t *testing.T) {
	h := newHarness(t)
	before := readFile(t, sharedFile(h))
	assert.Equal(t, 2, h.run("preset", "edit", "provision-lab", "--option", "db_password=hunter2,s3cret"), h.stderr.String())
	assert.Contains(t, h.stderr.String(), "db_password is a secret")
	assert.Equal(t, before, readFile(t, sharedFile(h)))

	path := filepath.Join(h.home, "bam", "presets", "lit.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("presets:\n  with-secret:\n    plan: PROJ-PROV\n    options:\n      api_secret: [hunter2, s3cret]\n"), 0o600))
	h.stdout.Reset()
	require.Equal(t, 0, h.run("preset", "edit", "with-secret", "--option", "api_secret="), h.stderr.String())
	assert.NotContains(t, h.stdout.String()+h.stderr.String(), "hunter2")
	assert.Contains(t, h.stdout.String(), `removed (was "********")`)
}

// A preset the other repositories share is renamed in their sets too, so a
// name another repository's own preset has is refused.
func TestPresetEditSharedRenameRefusesANameAnotherRepositoryDefines(t *testing.T) {
	h := newHarness(t)
	twoFilePreset(t, h)
	require.NoError(t, os.MkdirAll(filepath.Dir(h.env.Paths.MachineConfig), 0o755))
	require.NoError(t, os.WriteFile(h.env.Paths.MachineConfig, []byte("version: 1\nrepos:\n  /somewhere/else:\n    targets:\n      lab-two:\n        plan: PROJ-PROV\n"), 0o644))
	before := readFile(t, sharedFile(h))
	assert.Equal(t, 2, h.run("preset", "edit", "provision-lab", "--rename", "lab-two"), h.stderr.String())
	assert.Contains(t, h.stderr.String(), `preset "lab-two" already exists in the repository /somewhere/else`)
	assert.Equal(t, before, readFile(t, sharedFile(h)))

	h.tty = true
	offered := editScript(h, func(*app.PresetForm) {}, nil)
	require.Equal(t, 0, h.run("preset", "edit", "provision-lab"), h.stderr.String())
	assert.Equal(t, "/somewhere/else", (*offered)[0].TakenElsewhere["lab-two"], "the form refuses it while open")
}

// Emptying a plan variable in the form saves it empty, as --var NAME= does.
func TestPresetEditFormSavesAnEmptiedPlanVariable(t *testing.T) {
	h := newHarness(t)
	h.tty = true
	editScript(h, func(f *app.PresetForm) {
		for i := range f.Rows {
			if f.Rows[i].Name == "cluster_name" {
				f.Rows[i].Orig, f.Rows[i].Value = "alpha", ""
			}
		}
	}, nil)
	require.Equal(t, 0, h.run("preset", "edit", "provision-lab"), h.stderr.String())
	assert.Contains(t, readFile(t, sharedFile(h)), `cluster_name: ""`)
	assert.NotContains(t, h.stdout.String(), "already has these values")
	assert.Contains(t, h.stdout.String(), `variables.cluster_name: set to ""`, "the line says what the file holds, not that it was removed")
	assert.NotContains(t, h.stdout.String(), "removed")
}

// A value set to empty and a removal read differently in the text and in JSON.
func TestPresetEditEmptyValueIsNotARemoval(t *testing.T) {
	h := newHarness(t)
	require.Equal(t, 0, h.run("preset", "edit", "provision-lab", "--var", "cluster_type=", "--dry-run", "--json"), h.stderr.String())
	var set struct {
		Changes []struct {
			Field   string
			Removed bool
		}
	}
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &set))
	require.Len(t, set.Changes, 1)
	assert.False(t, set.Changes[0].Removed)

	h.stdout.Reset()
	require.Equal(t, 0, h.run("preset", "edit", "provision-lab", "--var", "cluster_type=", "--dry-run"), h.stderr.String())
	assert.Contains(t, h.stdout.String(), `variables.cluster_type: "k8s" -> ""`)

	h.stdout.Reset()
	require.Equal(t, 0, h.run("preset", "edit", "provision-lab", "--unset", "cluster_type", "--dry-run", "--json"), h.stderr.String())
	var unset = set
	unset.Changes = nil
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &unset))
	require.Len(t, unset.Changes, 1)
	assert.True(t, unset.Changes[0].Removed)
}

// Source: the captain asked for no restriction in preset names; a rename takes
// any name too.
func TestPresetEditRenameTakesAnyName(t *testing.T) {
	h := newHarness(t)
	require.Equal(t, 0, h.run("preset", "edit", "provision-lab", "--rename", "Lab: EU #2"), h.stderr.String())
	assert.Equal(t, 0, h.run("preset", "show", "Lab: EU #2"), h.stderr.String())
	assert.Equal(t, 2, h.run("preset", "edit", "Lab: EU #2", "--rename", "  "))
}
