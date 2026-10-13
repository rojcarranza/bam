package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
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

func TestRenderDraft(t *testing.T) {
	want := `provision-lab:
  plan: PROJ-PROV
  # branch: develop
  defaults:
    cluster_type: "k8s"  # plan default
    cluster_name: ""  # no plan default; last used "beta" in PROJ-PROV-97
    db_password: "${DB_PASSWORD}"  # masked by Bamboo; set env var DB_PASSWORD
  # options:
  #   cluster_type: ["k8s"]
  # required: [cluster_name]
  # watch: true
`
	assert.Equal(t, want, sampleDraft().Render(""))
}

func TestRenderDraftWithBranchAndNote(t *testing.T) {
	d := TargetDraft{Name: "b", Plan: "PROJ-B", Branch: "develop", Note: "plan variables are not readable on this server; add them under defaults"}
	want := `b:
  plan: PROJ-B
  # plan variables are not readable on this server; add them under defaults
  branch: "develop"
  # watch: true
`
	assert.Equal(t, want, d.Render(""))
}

func TestDraftYAMLIsStandaloneDocument(t *testing.T) {
	out := DraftYAML("targets", TargetDraft{Name: "b", Plan: "PROJ-B"})
	assert.Equal(t, "targets:\n  b:\n    plan: PROJ-B\n    # branch: develop\n    # watch: true\n", out)
}
