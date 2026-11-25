package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

const editYAML = `# team presets
version: 1
presets:
  deploy-qa: # the qa deploy
    plan: PROJ-DEPLOY
    branch: "release-1"
    repositories:
      api: "main"
      web: "main"
    variables:
      ENV: "qa" # keep me
      REGION: "eu"
      db_password: "${DB_PASSWORD}"
    options:
      ENV: [qa, prod]
    required: [ENV]
    timeout: 45m
    watch: true
  other:
    plan: PROJ-OTHER
    variables:
      X: "1"
`

func editFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "proj.yaml")
	writeFile(t, path, body)
	return path
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(b)
}

func TestEditTargetChangesOnlyNamedLines(t *testing.T) {
	path := editFile(t, editYAML)
	ch, err := EditTarget(path, []string{"presets"}, "deploy-qa", PresetEdit{
		Set:   []VarValue{{Name: "ENV", Value: "prod"}, {Name: "REGION", Value: "eu"}, {Name: "NEW", Value: "1"}},
		Unset: []string{"missing"},
	}, true)
	require.NoError(t, err)
	assert.Equal(t, []FieldChange{
		{Field: "variables.ENV", Old: "qa", New: "prod"},
		{Field: "variables.NEW", New: "1"},
	}, ch)
	got := read(t, path)
	assert.Contains(t, got, `ENV: "prod" # keep me`)
	assert.Contains(t, got, `NEW: "1"`)
	for _, kept := range []string{"# team presets", "# the qa deploy", "ENV: [qa, prod]", "required: [ENV]", "timeout: 45m", "watch: true", "other:", "X: \"1\""} {
		assert.Contains(t, got, kept)
	}
}

func TestEditTargetDryRunWritesNothing(t *testing.T) {
	path := editFile(t, editYAML)
	ch, err := EditTarget(path, []string{"presets"}, "deploy-qa", PresetEdit{Set: []VarValue{{Name: "ENV", Value: "prod"}}}, false)
	require.NoError(t, err)
	assert.Len(t, ch, 1)
	assert.Equal(t, editYAML, read(t, path))
}

func TestEditTargetNoChangeIsEmpty(t *testing.T) {
	path := editFile(t, editYAML)
	ch, err := EditTarget(path, []string{"presets"}, "deploy-qa", PresetEdit{Set: []VarValue{{Name: "ENV", Value: "qa"}}}, true)
	require.NoError(t, err)
	assert.Empty(t, ch)
	assert.Equal(t, editYAML, read(t, path))
}

func TestEditTargetUnsetRemovesLine(t *testing.T) {
	path := editFile(t, editYAML)
	ch, err := EditTarget(path, []string{"presets"}, "deploy-qa", PresetEdit{Unset: []string{"REGION"}}, true)
	require.NoError(t, err)
	assert.Equal(t, []FieldChange{{Field: "variables.REGION", Old: "eu"}}, ch)
	assert.NotContains(t, read(t, path), "REGION")
}

func TestEditTargetAllFields(t *testing.T) {
	path := editFile(t, editYAML)
	branch, timeout := "release-2", "1h"
	repos := []RepoBranch{{Name: "api", Branch: "release-2"}, {Name: "docs", Branch: "main"}}
	required := []string{"ENV", "REGION"}
	ch, err := EditTarget(path, []string{"presets"}, "deploy-qa", PresetEdit{
		Branch: &branch, Repositories: &repos, Required: &required, Timeout: &timeout,
		Options: map[string][]string{"REGION": {"eu", "us"}, "ENV": nil},
		Rename:  "deploy-prod",
	}, true)
	require.NoError(t, err)
	fields := []string{}
	for _, c := range ch {
		fields = append(fields, c.Field)
	}
	assert.ElementsMatch(t, []string{"branch", "repositories.api", "repositories.web", "repositories.docs", "required", "timeout", "options.REGION", "options.ENV", "name"}, fields)
	got := read(t, path)
	assert.Contains(t, got, "deploy-prod:")
	assert.NotContains(t, got, "deploy-qa:")
	assert.Contains(t, got, `branch: "release-2"`)
	assert.NotContains(t, got, "web:")
	assert.Contains(t, got, "# the qa deploy")
}

func TestEditTargetRefusals(t *testing.T) {
	anchored := "presets:\n  a: &base\n    plan: PROJ-A\n    variables:\n      K: \"1\"\n  b: *base\n"
	merged := "presets:\n  base: {plan: PROJ-A}\n  a:\n    <<: {plan: PROJ-A}\n    variables:\n      K: \"1\"\n"
	cases := []struct {
		name, body, preset string
		edit               PresetEdit
		msg                string
	}{
		{"anchor", anchored, "a", PresetEdit{Set: []VarValue{{Name: "K", Value: "2"}}}, "anchors"},
		{"alias", anchored, "b", PresetEdit{Set: []VarValue{{Name: "K", Value: "2"}}}, "anchors"},
		{"merge key", merged, "a", PresetEdit{Set: []VarValue{{Name: "K", Value: "2"}}}, "anchors"},
		{"literal secret", editYAML, "deploy-qa", PresetEdit{Set: []VarValue{{Name: "db_password", Value: "hunter2"}}}, "secret"},
		{"unknown preset", editYAML, "nope", PresetEdit{Unset: []string{"ENV"}}, "nope"},
		{"rename clash", editYAML, "deploy-qa", PresetEdit{Rename: "other"}, "already exists"},
		{"bad timeout", editYAML, "deploy-qa", PresetEdit{Timeout: ptr("soon")}, "duration"},
		{"value outside options", editYAML, "deploy-qa", PresetEdit{Set: []VarValue{{Name: "ENV", Value: "dev"}}}, "not one of"},
		{"repositories without branch", editYAML, "deploy-qa", PresetEdit{Branch: ptr("")}, "without a branch"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := editFile(t, tc.body)
			_, err := EditTarget(path, []string{"presets"}, tc.preset, tc.edit, true)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.msg)
			assert.Equal(t, tc.body, read(t, path), "the file must be untouched")
		})
	}
}

func TestEditTargetSecretReferenceAllowed(t *testing.T) {
	path := editFile(t, editYAML)
	_, err := EditTarget(path, []string{"presets"}, "deploy-qa", PresetEdit{Set: []VarValue{{Name: "db_password", Value: "${OTHER_TOKEN}"}}}, true)
	require.NoError(t, err)
	assert.Contains(t, read(t, path), "${OTHER_TOKEN}")
}

func ptr(s string) *string { return &s }

// Presets defined in two files merge; the edit writes the top file and is
// checked as bam loads the merge.
func layersFor(t *testing.T, lower string) []TargetLayer {
	t.Helper()
	path := editFile(t, lower)
	var f struct {
		Presets map[string]Target `yaml:"presets"`
	}
	require.NoError(t, yaml.Unmarshal([]byte(lower), &f))
	tg := f.Presets["deploy-qa"]
	tg.Defaults, tg.Variables = tg.Variables, nil // Load folds variables into defaults
	return []TargetLayer{{Path: path, KeyPath: []string{"presets"}, Target: tg}}
}

const lowerYAML = `presets:
  deploy-qa:
    plan: PROJ-DEPLOY
    branch: "release-1"
    repositories:
      api: "main"
    variables:
      ENV: "qa"
      OLD: "x"
    options:
      ENV: [qa, prod]
    required: [ENV]
    timeout: 45m
`

const topYAML = "presets:\n  deploy-qa:\n    variables:\n      REGION: \"eu\"\n"

func TestEditTargetOverChecksTheMerge(t *testing.T) {
	below := layersFor(t, lowerYAML)
	path := editFile(t, topYAML)
	// The options live below: a value they do not allow breaks the merge.
	_, err := EditTargetOver(path, []string{"presets"}, "deploy-qa", below, PresetEdit{Set: []VarValue{{Name: "ENV", Value: "dev"}}}, true)
	require.ErrorContains(t, err, "not one of")
	assert.Equal(t, topYAML, read(t, path))
	// Repositories in the top file are valid because the branch is below.
	repos := []RepoBranch{{Name: "api", Branch: "main"}, {Name: "web", Branch: "main"}}
	ch, err := EditTargetOver(path, []string{"presets"}, "deploy-qa", below, PresetEdit{Repositories: &repos}, true)
	require.NoError(t, err)
	assert.Equal(t, []FieldChange{{Field: "repositories.web", New: "main"}}, ch, "api is already main in the lower file")
}

func TestEditTargetOverRefusesRemovalsTheTopFileCannotMake(t *testing.T) {
	below := layersFor(t, lowerYAML)
	empty := ""
	none := []string{}
	cases := map[string]PresetEdit{
		"unset":    {Unset: []string{"OLD"}},
		"branch":   {Branch: &empty},
		"timeout":  {Timeout: &empty},
		"required": {Required: &none},
		"options":  {Options: map[string][]string{"ENV": nil}},
		"repo":     {Repositories: &[]RepoBranch{}},
	}
	for name, e := range cases {
		t.Run(name, func(t *testing.T) {
			path := editFile(t, topYAML)
			_, err := EditTargetOver(path, []string{"presets"}, "deploy-qa", below, e, true)
			require.Error(t, err)
			assert.Contains(t, err.Error(), below[0].Path, "names the file that holds it")
			assert.Equal(t, topYAML, read(t, path))
		})
	}
}

func TestRenameTargetChangesOnlyTheName(t *testing.T) {
	path := editFile(t, editYAML)
	require.NoError(t, RenameTarget(path, []string{"presets"}, "other", "third", true))
	got := read(t, path)
	assert.Contains(t, got, "third:\n    plan: PROJ-OTHER")
	assert.Contains(t, got, "deploy-qa:")
	anchored := "presets:\n  a: &x\n    plan: PROJ-A\n"
	path = editFile(t, anchored)
	require.ErrorContains(t, RenameTarget(path, []string{"presets"}, "a", "b", true), "anchors")
	assert.Equal(t, anchored, read(t, path))
}

func TestCheckEditable(t *testing.T) {
	assert.NoError(t, CheckEditable(editFile(t, editYAML), []string{"presets"}, "deploy-qa"))
	assert.ErrorContains(t, CheckEditable(editFile(t, "presets:\n  a: &x\n    plan: PROJ-A\n"), []string{"presets"}, "a"), "anchors")
}

func TestHeldBelowSeparatesWhatOnlyALowerFileHolds(t *testing.T) {
	below := layersFor(t, lowerYAML)
	top := Target{Defaults: StringMap{"ENV": "qa"}, Branch: "main"}
	h := HeldBelow(top, below)
	file := below[0].Path
	assert.Equal(t, file, h.Also["variables.ENV"])
	assert.Equal(t, file, h.Also["branch"])
	assert.Equal(t, file, h.Only["variables.OLD"])
	assert.Equal(t, file, h.Only["timeout"])
	assert.Equal(t, file, h.Only["required"])
	assert.Equal(t, file, h.Only["options.ENV"])
	assert.Equal(t, file, h.Only["repositories.api"])
	assert.NotContains(t, h.Only, "variables.ENV")
}
