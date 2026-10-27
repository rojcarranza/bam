package app

import (
	"testing"

	"github.com/r0jjames/bam-cli/internal/config"
	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateTargetFromDeclaredVariables(t *testing.T) {
	s := newService(t, fakeBamboo())
	d, err := s.GenerateTarget(bg, GenerateOptions{Name: "provision-lab", PlanKey: "PROJ-PROV", Branch: "develop", Full: true})
	require.NoError(t, err)
	assert.Equal(t, config.TargetDraft{
		Name: "provision-lab", Plan: "PROJ-PROV", Branch: "develop", Full: true,
		Vars: []config.DraftVar{
			{Name: "cluster_type", Value: "k8s", Comment: "plan default"},
			{Name: "compute_nodes", Value: "1", Comment: "plan default"},
			{Name: "cluster_name", Value: "", Comment: `no plan default; last used "beta" in PROJ-PROV12-8`},
			{Name: "db_password", Value: "${DB_PASSWORD}", Comment: "masked by Bamboo; set env var DB_PASSWORD"},
		},
		Required: []string{"cluster_name"},
	}, d)
}

func TestGenerateTargetWithoutDeclaredList(t *testing.T) {
	p := fakeBamboo()
	p.VariablesErr = errs.Bamboof("no").Wrap(errs.ErrUnsupported)
	s := newService(t, p)
	d, err := s.GenerateTarget(bg, GenerateOptions{Name: "prov", PlanKey: "PROJ-PROV", Branch: "develop"})
	require.NoError(t, err)
	assert.Contains(t, d.Note, "names come from PROJ-PROV12-8")
	assert.Len(t, d.Vars, 5)
	assert.Equal(t, config.DraftVar{Name: "cluster_name", Value: "beta", Comment: "from PROJ-PROV12-8"}, d.Vars[0])
}

func TestGenerateTargetWithNothingReadable(t *testing.T) {
	p := fakeBamboo()
	p.VariablesErr = errs.Bamboof("no").Wrap(errs.ErrUnsupported)
	p.BuildVarsErr = errs.Bamboof("no").Wrap(errs.ErrUnsupported)
	s := newService(t, p)
	d, err := s.GenerateTarget(bg, GenerateOptions{Name: "prov", PlanKey: "PROJ-PROV"})
	require.NoError(t, err)
	assert.Empty(t, d.Vars)
	assert.Equal(t, "plan variables are not readable on this server; add them under defaults", d.Note)
}

func TestGenerateTargetRejectsBadInput(t *testing.T) {
	s := newService(t, fakeBamboo())
	_, err := s.GenerateTarget(bg, GenerateOptions{Name: "Prov", PlanKey: "PROJ-PROV"})
	assert.Equal(t, errs.KindUsage, errs.KindOf(err))
	_, err = s.GenerateTarget(bg, GenerateOptions{Name: "prov", PlanKey: "PROJ-NOPE"})
	assert.Equal(t, errs.KindBamboo, errs.KindOf(err))
}

func TestGenerateTargetFillsValuesFromNamedBuild(t *testing.T) {
	s := newService(t, fakeBamboo())
	d, err := s.GenerateTarget(bg, GenerateOptions{Name: "prov", PlanKey: "PROJ-PROV", Branch: "develop", From: "8"})
	require.NoError(t, err)
	assert.Equal(t, []config.DraftVar{
		{Name: "cluster_type", Value: "dcos", Comment: "from PROJ-PROV12-8"},
		{Name: "compute_nodes", Value: "3", Comment: "from PROJ-PROV12-8"},
		{Name: "cluster_name", Value: "beta", Comment: "from PROJ-PROV12-8"},
		{Name: "db_password", Value: "${DB_PASSWORD}", Comment: "masked by Bamboo; set env var DB_PASSWORD"},
		{Name: "global_flag", Value: "x", Comment: "from PROJ-PROV12-8; not declared by the plan"},
	}, d.Vars)
	assert.Empty(t, d.Required)
}

func TestGenerateTargetFromBuildKeepsPlanDefaultComment(t *testing.T) {
	p := fakeBamboo()
	p.BuildVars["PROJ-PROV12-8"]["cluster_type"] = "k8s"
	s := newService(t, p)
	d, err := s.GenerateTarget(bg, GenerateOptions{Name: "prov", PlanKey: "PROJ-PROV", Branch: "develop", From: "8", Full: true})
	require.NoError(t, err)
	assert.Equal(t, config.DraftVar{Name: "cluster_type", Value: "k8s", Comment: "plan default"}, d.Vars[0])
}

func TestGenerateTargetFromBuildNeedsReadableVariables(t *testing.T) {
	p := fakeBamboo()
	p.BuildVarsErr = errs.Bamboof("no").Wrap(errs.ErrUnsupported)
	s := newService(t, p)
	_, err := s.GenerateTarget(bg, GenerateOptions{Name: "prov", PlanKey: "PROJ-PROV", Branch: "develop", From: "8"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot read the variables of PROJ-PROV12-8")
}

func TestGenerateTargetFromBuildKeyImpliesPlanAndBranch(t *testing.T) {
	p := fakeBamboo()
	b := &p.History["PROJ-PROV12"][1]
	b.Branch, b.MasterKey = "develop", "PROJ-PROV"
	s := newService(t, p)
	d, err := s.GenerateTarget(bg, GenerateOptions{Name: "prov", From: "PROJ-PROV12-8"})
	require.NoError(t, err)
	assert.Equal(t, "PROJ-PROV", d.Plan)
	assert.Equal(t, "develop", d.Branch)
	assert.Equal(t, config.DraftVar{Name: "cluster_type", Value: "dcos", Comment: "from PROJ-PROV12-8"}, d.Vars[0])

	d, err = s.GenerateTarget(bg, GenerateOptions{Name: "prov", PlanKey: "PROJ-PROV", Branch: "feat/foo", From: "PROJ-PROV12-8"})
	require.NoError(t, err)
	assert.Equal(t, "feat/foo", d.Branch, "--branch overrides the build's branch")

	_, err = s.GenerateTarget(bg, GenerateOptions{Name: "prov", PlanKey: "PROJ-BUILD", From: "PROJ-PROV12-8"})
	assert.Equal(t, errs.KindUsage, errs.KindOf(err))
	assert.Contains(t, err.Error(), "PROJ-PROV12-8 is a build of PROJ-PROV, not of PROJ-BUILD")
}

func TestGenerateTargetLeanKeepsDifferingValues(t *testing.T) {
	p := fakeBamboo()
	p.BuildVars["PROJ-PROV12-8"]["cluster_type"] = "k8s"
	s := newService(t, p)
	d, err := s.GenerateTarget(bg, GenerateOptions{Name: "prov", PlanKey: "PROJ-PROV", Branch: "develop", From: "8"})
	require.NoError(t, err)
	var names []string
	for _, v := range d.Vars {
		names = append(names, v.Name)
	}
	assert.NotContains(t, names, "cluster_type", "equal to the plan default")
	assert.Contains(t, names, "compute_nodes")
	assert.Contains(t, names, "db_password", "secrets stay as ${ENV} references")
	assert.Contains(t, names, "global_flag", "undeclared variables are always kept")
}

func TestGenerateTargetLeanByDefault(t *testing.T) {
	s := newService(t, fakeBamboo())
	d, err := s.GenerateTarget(bg, GenerateOptions{Name: "prov", PlanKey: "PROJ-PROV"})
	require.NoError(t, err)
	assert.False(t, d.Full)
	assert.Equal(t, []config.DraftVar{
		{Name: "cluster_name", Value: "", Comment: "no plan default"},
		{Name: "db_password", Value: "${DB_PASSWORD}", Comment: "masked by Bamboo; set env var DB_PASSWORD"},
	}, d.Vars, "plan defaults are left out; empty and secret variables stay")
}
