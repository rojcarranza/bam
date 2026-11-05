package config

import (
	"strings"
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
			{Name: "cluster_type", Value: "k8s", Comment: "plan default"},
			{Name: "cluster_name", Value: "", Comment: `no plan default; last used "beta" in PROJ-PROV-97`},
			{Name: "db_password", Value: "${DB_PASSWORD}", Comment: "masked by Bamboo; set env var DB_PASSWORD"},
		},
		Required: []string{"cluster_name"},
	}
}

// BAM-40: every preset key is written, in schema order, with watch: true and
// required (variables without a plan default) active, the rest commented.
func TestRenderDraftWritesAllNineKeys(t *testing.T) {
	want := `provision-lab:
  plan: PROJ-PROV
  # server: work  # server alias to run on; default is the one bam picks
  # branch: develop  # plan branch to run; default is the main plan
  # repositories:  # branch each repository of the plan branch builds; needs branch:
  #   repo-a: "BRANCH"
  defaults:
    cluster_type: "k8s"  # plan default
    cluster_name: ""  # no plan default; last used "beta" in PROJ-PROV-97
    db_password: "${DB_PASSWORD}"  # masked by Bamboo; set env var DB_PASSWORD
  # options:  # allowed values per variable; a run with another value is refused
  #   cluster_type: ["k8s"]
  required: [cluster_name]  # a run with an empty value is refused
  watch: true  # follow the build without --watch; --watch=false skips it
`
	assert.Equal(t, want, sampleDraft().Render(""))
}

func TestRenderDraftWithBranchServerAndNote(t *testing.T) {
	d := TargetDraft{Name: "b", Plan: "PROJ-B", Server: "lab", Branch: "develop", Note: "plan variables are not readable on this server; add them under defaults"}
	want := `b:
  plan: PROJ-B
  # plan variables are not readable on this server; add them under defaults
  server: lab  # the server this preset runs on
  branch: "develop"
  # repositories:  # branch each repository of the plan branch builds; needs branch:
  #   repo-a: "BRANCH"
  # options:  # allowed values per variable; a run with another value is refused
  #   NAME: ["a", "b"]
  # required: [NAME]  # refuse a run where these have no value
  watch: true  # follow the build without --watch; --watch=false skips it
`
	assert.Equal(t, want, d.Render(""))
}

// A plan whose variables all have defaults still gets an example required line.
func TestRenderDraftRequiredExampleNamesFirstVariable(t *testing.T) {
	d := TargetDraft{Name: "b", Plan: "PROJ-B", Vars: []DraftVar{{Name: "env", Value: "qa", Comment: "plan default"}}}
	out := d.RenderAs("", "variables")
	assert.Contains(t, out, "  # required: [env]  ")
	assert.Contains(t, out, "  #   env: [\"qa\"]\n")
}

// Each optional line, uncommented on its own, must leave a file that loads.
func TestRenderDraftEveryLineUncommentedStillParses(t *testing.T) {
	out := DraftYAML("presets", sampleDraft())
	var base PresetsFile
	require.NoError(t, yaml.Unmarshal([]byte(out), &base))
	tg := base.Presets["provision-lab"]
	assert.Equal(t, "PROJ-PROV", tg.Plan)
	require.NotNil(t, tg.Watch)
	assert.True(t, *tg.Watch)
	assert.Equal(t, []string{"cluster_name"}, tg.Required)
	for _, c := range []string{"server: work", "branch: develop"} {
		var f PresetsFile
		lines := strings.Split(out, "\n")
		for i, l := range lines {
			if strings.HasPrefix(strings.TrimSpace(l), "# "+c) {
				lines[i] = strings.Replace(l, "# ", "", 1)
			}
		}
		require.NoError(t, yaml.Unmarshal([]byte(strings.Join(lines, "\n")), &f), c)
	}
}

// BAW-7: a preset with a branch and the branch each repository builds now
// writes repositories active; the file loads and keeps every entry.
func TestRenderDraftWritesCurrentRepositoryBranches(t *testing.T) {
	d := TargetDraft{Name: "b", Plan: "PROJ-B", Branch: "tst", Repos: []DraftRepo{
		{Name: "app", Branch: "develop"}, {Name: "infra-scripts", Branch: "feature/cli-tools"}}}
	out := DraftYAML("presets", d)
	assert.Contains(t, out, "    repositories:  #")
	var f PresetsFile
	require.NoError(t, yaml.Unmarshal([]byte(out), &f))
	assert.Equal(t, StringMap{"app": "develop", "infra-scripts": "feature/cli-tools"}, f.Presets["b"].Repositories)
}

// BAW-7: without a plan branch repositories would not load, so the lines are
// commented even when the branches are known; with a repository whose branch
// is unknown they are commented too.
func TestRenderDraftCommentsRepositoriesWithoutBranchOrBuild(t *testing.T) {
	known := TargetDraft{Name: "b", Plan: "PROJ-B", Repos: []DraftRepo{{Name: "app", Branch: "develop"}}}
	assert.Contains(t, known.Render(""), "  # repositories:  #")
	assert.Contains(t, known.Render(""), "  #   app: \"develop\"\n")
	unknown := TargetDraft{Name: "b", Plan: "PROJ-B", Branch: "tst", Repos: []DraftRepo{{Name: "app"}}}
	assert.Contains(t, unknown.Render(""), "  # repositories:  #")
	assert.Contains(t, unknown.Render(""), "  #   app: \"BRANCH\"\n")
}

func TestDraftYAMLIsStandaloneDocument(t *testing.T) {
	out := DraftYAML("targets", TargetDraft{Name: "b", Plan: "PROJ-B"})
	assert.Contains(t, out, "targets:\n  b:\n    plan: PROJ-B\n    # server: work")
	assert.Contains(t, out, "    watch: true  #")
}
