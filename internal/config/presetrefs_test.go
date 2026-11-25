package config

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const refsProject = `version: 1
targets:
  lab:
    plan: PROJ-LAB
  other:
    plan: PROJ-OTHER
sets:
  train:   # the release train
    stages:
      - [other]
      - - target: lab
          vars: {A: "1"}
  short: [[lab], [other]]
monitor:
  main: [lab, PROJ-PLAN, other]
`

const refsMachine = `version: 1
sets:
  mine:
    stages:
      - [lab]
monitor:
  extra: [lab]
repos:
  /work/repo:
    sets:
      here: [[lab]]
  /work/elsewhere:
    sets:
      there: [[lab]]
`

func refsConfig(t *testing.T, project, machine string) *Config {
	t.Helper()
	dir := t.TempDir()
	c := &Config{ProjectPath: filepath.Join(dir, ".bam.yaml"), MachinePath: filepath.Join(dir, "config.yaml"), RepoRoot: "/work/repo"}
	writeFile(t, c.ProjectPath, project)
	writeFile(t, c.MachinePath, machine)
	c.Machine = &MachineFile{Repos: map[string]Repo{"/work/repo": {}, "/work/elsewhere": {}}}
	return c
}

// Every stage and pin that names the preset is renamed in place, in the
// project file and the machine file; another repository's sets and every
// comment stay as they were.
func TestRenameRefsRenamesStagesAndPinsInPlace(t *testing.T) {
	c := refsConfig(t, refsProject, refsMachine)
	ch, err := c.RenameRefs("lab", "lab-two", true)
	require.NoError(t, err)
	assert.ElementsMatch(t, []FieldChange{
		{Field: "sets.train", Old: "lab", New: "lab-two"},
		{Field: "sets.short", Old: "lab", New: "lab-two"},
		{Field: "monitor.main", Old: "lab", New: "lab-two"},
		{Field: "sets.mine", Old: "lab", New: "lab-two"},
		{Field: "monitor.extra", Old: "lab", New: "lab-two"},
		{Field: "sets.here", Old: "lab", New: "lab-two"},
	}, ch)

	project := read(t, c.ProjectPath)
	assert.Contains(t, project, "# the release train")
	assert.Contains(t, project, "target: lab-two")
	assert.Contains(t, project, "short: [[lab-two], [other]]")
	assert.Contains(t, project, "main: [lab-two, PROJ-PLAN, other]")
	assert.Contains(t, project, "  lab:\n    plan: PROJ-LAB", "the preset's own definition is not a reference")
	machine := read(t, c.MachinePath)
	assert.Contains(t, machine, "here: [[lab-two]]")
	assert.Contains(t, machine, "there: [[lab]]", "another repository's sets name that repository's presets")
}

func TestRenameRefsDryRunWritesNothing(t *testing.T) {
	c := refsConfig(t, refsProject, refsMachine)
	ch, err := c.RenameRefs("lab", "lab-two", false)
	require.NoError(t, err)
	assert.Len(t, ch, 6)
	assert.Equal(t, refsProject, read(t, c.ProjectPath))
	assert.Equal(t, refsMachine, read(t, c.MachinePath))
}

func TestRenameRefsNothingToRename(t *testing.T) {
	c := refsConfig(t, refsProject, refsMachine)
	ch, err := c.RenameRefs("unused", "unused-two", true)
	require.NoError(t, err)
	assert.Empty(t, ch)
	assert.Equal(t, refsProject, read(t, c.ProjectPath))
}

// A stage reused through a YAML alias would change under another set, and a
// stage the walk cannot see would keep the old name: both are refused, and
// neither file is written.
func TestRenameRefsRefusesAnchorsAndAliases(t *testing.T) {
	anchored := "version: 1\ntargets:\n  lab:\n    plan: PROJ-LAB\nsets:\n  a:\n    stages:\n      - [&l lab]\n"
	c := refsConfig(t, anchored, refsMachine)
	_, err := c.RenameRefs("lab", "lab-two", true)
	require.ErrorContains(t, err, "anchor")
	assert.Equal(t, anchored, read(t, c.ProjectPath))
	assert.Equal(t, refsMachine, read(t, c.MachinePath), "the machine file is not written when the project file is refused")

	aliased := "version: 1\ntargets:\n  lab:\n    plan: PROJ-LAB\nx-stage: &st [lab]\nsets:\n  a:\n    stages:\n      - *st\n"
	c = refsConfig(t, aliased, refsMachine)
	_, err = c.RenameRefs("lab", "lab-two", true)
	require.ErrorContains(t, err, "alias")
	assert.Equal(t, aliased, read(t, c.ProjectPath))
}

// An empty branch in a repositories edit removes the repository and does not
// skip the entry after it.
func TestEditTargetEmptyRepositoryBranchRemovesAndKeepsTheNext(t *testing.T) {
	path := editFile(t, editYAML)
	repos := []RepoBranch{{Name: "api", Branch: ""}, {Name: "web", Branch: "dev"}}
	ch, err := EditTarget(path, []string{"presets"}, "deploy-qa", PresetEdit{Repositories: &repos}, true)
	require.NoError(t, err)
	assert.Equal(t, []FieldChange{{Field: "repositories.api", Old: "main"}, {Field: "repositories.web", Old: "main", New: "dev"}}, ch)
	got := read(t, path)
	assert.NotContains(t, got, "api:")
	assert.Contains(t, got, `web: "dev"`)
}
