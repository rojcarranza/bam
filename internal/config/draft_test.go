package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func sampleDraft() TargetDraft {
	return TargetDraft{
		Name: "provision-lab",
		Plan: "PROJ-PROV",
		Vars: []DraftVar{
			{Name: "cluster_type", Value: "k8s"},
			{Name: "cluster_name", Value: ""},
			{Name: "db_password", Value: "${DB_PASSWORD}"},
		},
	}
}

// The draft writes only the keys the person set, no per-line comments
// except the two warnings, and neither required: nor options:.
func TestRenderDraftWritesOnlySetKeysWithoutComments(t *testing.T) {
	d := sampleDraft()
	d.Vars = append(d.Vars, DraftVar{Name: "extra", Value: "x", Comment: "not declared by the plan"})
	want := `provision-lab:
  plan: PROJ-PROV
  defaults:
    cluster_type: "k8s"
    cluster_name: ""
    db_password: "${DB_PASSWORD}"
    extra: "x"  # not declared by the plan
  watch: true
`
	assert.Equal(t, want, d.Render(""))
}

func TestRenderDraftWithBranchServerAndNote(t *testing.T) {
	d := TargetDraft{Name: "b", Plan: "PROJ-B", Server: "lab", Branch: "develop", Note: "plan variables are not readable on this server; add them under defaults"}
	want := `b:
  plan: PROJ-B
  # plan variables are not readable on this server; add them under defaults
  server: lab
  branch: "develop"
  watch: true
`
	assert.Equal(t, want, d.Render(""))
}

// The generated file loads, with watch on and nothing required.
func TestRenderDraftParses(t *testing.T) {
	var base PresetsFile
	require.NoError(t, yaml.Unmarshal([]byte(DraftYAML("presets", sampleDraft())), &base))
	tg := base.Presets["provision-lab"]
	assert.Equal(t, "PROJ-PROV", tg.Plan)
	require.NotNil(t, tg.Watch)
	assert.True(t, *tg.Watch)
	assert.Empty(t, tg.Required)
}

// BAW-7: a preset with a branch and the branch each repository builds now
// writes repositories active; the file loads and keeps every entry.
func TestRenderDraftWritesCurrentRepositoryBranches(t *testing.T) {
	d := TargetDraft{Name: "b", Plan: "PROJ-B", Branch: "tst", Repos: []DraftRepo{
		{Name: "app", Branch: "develop"}, {Name: "infra-scripts", Branch: "feature/cli-tools"}}}
	out := DraftYAML("presets", d)
	assert.Contains(t, out, "    repositories:\n      app: \"develop\"\n")
	var f PresetsFile
	require.NoError(t, yaml.Unmarshal([]byte(out), &f))
	assert.Equal(t, StringMap{"app": "develop", "infra-scripts": "feature/cli-tools"}, f.Presets["b"].Repositories)
}

// BAW-7: without a plan branch repositories would not load, so none are
// written; with a repository whose branch is unknown they are commented so the
// names are not lost.
func TestRenderDraftCommentsRepositoriesWithoutBuild(t *testing.T) {
	known := TargetDraft{Name: "b", Plan: "PROJ-B", Repos: []DraftRepo{{Name: "app", Branch: "develop"}}}
	assert.NotContains(t, known.Render(""), "repositories")
	unknown := TargetDraft{Name: "b", Plan: "PROJ-B", Branch: "tst", Repos: []DraftRepo{{Name: "app"}}}
	assert.Contains(t, unknown.Render(""), "  # repositories:\n  # app: \"BRANCH\"\n")
}

func TestDraftYAMLIsStandaloneDocument(t *testing.T) {
	out := DraftYAML("targets", TargetDraft{Name: "b", Plan: "PROJ-B"})
	assert.Equal(t, "targets:\n  b:\n    plan: PROJ-B\n    watch: true\n", out)
}
