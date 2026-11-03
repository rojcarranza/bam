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
  # repo_branches:  # branch per repository, also built on the child plans that follow; remove branch first
  #   repo-a: "feature/PROJ-101"
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
  # repo_branches:  # branch per repository, also built on the child plans that follow; remove branch first
  #   repo-a: "feature/PROJ-101"
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
	// repo_branches is a commented block; uncomment it with its entries.
	d := sampleDraft()
	d.Repos = []string{"repo-a", "repo-b"}
	var f PresetsFile
	var kept []string
	in := false
	for _, l := range strings.Split(DraftYAML("presets", d), "\n") {
		t := strings.TrimSpace(l)
		switch {
		case strings.HasPrefix(t, "# repo_branches:"):
			in = true
			kept = append(kept, "    repo_branches:")
		case in && strings.HasPrefix(t, "#   repo-"):
			kept = append(kept, "      "+strings.TrimPrefix(t, "#   "))
		default:
			in = false
			kept = append(kept, l)
		}
	}
	require.NoError(t, yaml.Unmarshal([]byte(strings.Join(kept, "\n")), &f))
	assert.Equal(t, StringMap{"repo-a": "feature/PROJ-101", "repo-b": "feature/PROJ-101"}, f.Presets["provision-lab"].RepoBranches)
}

func TestDraftYAMLIsStandaloneDocument(t *testing.T) {
	out := DraftYAML("targets", TargetDraft{Name: "b", Plan: "PROJ-B"})
	assert.Contains(t, out, "targets:\n  b:\n    plan: PROJ-B\n    # server: work")
	assert.Contains(t, out, "    watch: true  #")
}
