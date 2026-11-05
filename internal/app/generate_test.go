package app

import (
	"testing"

	"github.com/r0jjames/bam-cli/internal/config"
	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateTargetFromDeclaredVariables(t *testing.T) {
	s := newService(t, fakeBamboo())
	d, err := s.GenerateTarget(bg, GenerateOptions{Name: "provision-lab", PlanKey: "PROJ-PROV", Branch: "develop"})
	require.NoError(t, err)
	assert.Equal(t, config.TargetDraft{
		Name: "provision-lab", Plan: "PROJ-PROV", Branch: "develop",
		Vars: []config.DraftVar{
			{Name: "cluster_type", Value: "k8s", Comment: "plan default"},
			{Name: "compute_nodes", Value: "1", Comment: "plan default"},
			{Name: "cluster_name", Value: "", Comment: `no plan default; last used "beta" in PROJ-PROV12-8`},
			{Name: "db_password", Value: "${DB_PASSWORD}", Comment: "masked by Bamboo; set env var DB_PASSWORD", Commented: true},
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
		{Name: "db_password", Value: "${DB_PASSWORD}", Comment: "masked by Bamboo; set env var DB_PASSWORD", Commented: true},
		{Name: "global_flag", Value: "x", Comment: "from PROJ-PROV12-8; not declared by the plan"},
	}, d.Vars)
	assert.Empty(t, d.Required)
}

func TestGenerateTargetFromBuildKeepsPlanDefaultComment(t *testing.T) {
	p := fakeBamboo()
	p.BuildVars["PROJ-PROV12-8"]["cluster_type"] = "k8s"
	s := newService(t, p)
	d, err := s.GenerateTarget(bg, GenerateOptions{Name: "prov", PlanKey: "PROJ-PROV", Branch: "develop", From: "8"})
	require.NoError(t, err)
	assert.Equal(t, config.DraftVar{Name: "cluster_type", Value: "k8s", Comment: "plan default"}, d.Vars[0])
}

func TestGenerateTargetFromBuildEmptyValueOverPlanDefaultIsNotRequired(t *testing.T) {
	p := fakeBamboo()
	p.BuildVars["PROJ-PROV12-8"]["compute_nodes"] = ""
	s := newService(t, p)
	d, err := s.GenerateTarget(bg, GenerateOptions{Name: "prov", PlanKey: "PROJ-PROV", Branch: "develop", From: "8"})
	require.NoError(t, err)
	assert.Contains(t, d.Vars, config.DraftVar{Name: "compute_nodes", Value: "", Comment: "from PROJ-PROV12-8"})
	assert.NotContains(t, d.Required, "compute_nodes", "the build ran with it empty on purpose")
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

func TestGenerateTargetKeepsEveryDeclaredVariable(t *testing.T) {
	p := fakeBamboo()
	p.BuildVars["PROJ-PROV12-8"]["cluster_type"] = "k8s"
	s := newService(t, p)
	d, err := s.GenerateTarget(bg, GenerateOptions{Name: "prov", PlanKey: "PROJ-PROV", Branch: "develop", From: "8"})
	require.NoError(t, err)
	var names []string
	for _, v := range d.Vars {
		names = append(names, v.Name)
	}
	assert.Contains(t, names, "cluster_type", "equal to the plan default, still written")
	assert.Contains(t, names, "compute_nodes")
	assert.Contains(t, names, "db_password")
	assert.Contains(t, names, "global_flag", "undeclared variables are kept")
}

// Reported: bam preset add left the plan's declared variables out of the
// preset, so the user had nothing to fill in (BAM-31).
func TestGenerateTargetWritesEveryDeclaredVariableByDefault(t *testing.T) {
	p := fakeBamboo()
	p.Variables["PROJ-PROV"] = append(p.Variables["PROJ-PROV"], provider.Variable{Name: "api_secret", Masked: true})
	s := newService(t, p)
	d, err := s.GenerateTarget(bg, GenerateOptions{Name: "prov", PlanKey: "PROJ-PROV"})
	require.NoError(t, err)
	assert.Equal(t, []config.DraftVar{
		{Name: "cluster_type", Value: "k8s", Comment: "plan default"},
		{Name: "compute_nodes", Value: "1", Comment: "plan default"},
		{Name: "cluster_name", Value: "", Comment: "no plan default"},
		{Name: "db_password", Value: "${DB_PASSWORD}", Comment: "masked by Bamboo; set env var DB_PASSWORD", Commented: true},
		{Name: "api_secret", Value: "${API_SECRET}", Comment: "masked by Bamboo; set env var API_SECRET"},
	}, d.Vars, "every declared variable; a stored secret is commented, an empty one is active")
}

// BAM-37: preset add wrote only name and plan, silently, for a plan that lists
// no variables (they are global or deployment variables, which bam does not read).
func TestGenerateTargetNotesPlanWithoutVariables(t *testing.T) {
	p := fakeBamboo()
	p.Plans["PROJ"] = append(p.Plans["PROJ"], provider.Plan{Key: "PROJ-GLOB", Name: "Globals only", ProjectKey: "PROJ"})
	s := newService(t, p)
	d, err := s.GenerateTarget(bg, GenerateOptions{Name: "glob", PlanKey: "PROJ-GLOB"})
	require.NoError(t, err)
	assert.Empty(t, d.Vars)
	assert.Contains(t, d.Note, "PROJ-GLOB lists no variables of its own")
	assert.Contains(t, d.Note, "global and deployment variables are not read")
}

func TestGenerateTargetFromBranchKeyUsesMainPlanVariables(t *testing.T) {
	p := fakeBamboo()
	p.Plans["PROJ"] = append(p.Plans["PROJ"], provider.Plan{Key: "PROJ-PROV12", Name: "Provision lab - develop", ProjectKey: "PROJ", MasterKey: "PROJ-PROV"})
	p.Branches = map[string][]provider.Branch{"PROJ-PROV": {{Key: "PROJ-PROV12", Name: "feature/custom-plan", PlanKey: "PROJ-PROV"}}}
	s := newService(t, p)
	d, err := s.GenerateTarget(bg, GenerateOptions{Name: "dev", PlanKey: "PROJ-PROV12"})
	require.NoError(t, err)
	// The captain asked (BAM-40 thread) that a custom plan branch land in the preset.
	assert.Equal(t, "PROJ-PROV", d.Plan)
	assert.Equal(t, "feature/custom-plan", d.Branch)
	require.Len(t, d.Vars, 4)
	assert.Equal(t, "cluster_type", d.Vars[0].Name)
	assert.Empty(t, d.Note, "the main plan lists the variables itself")
}

func TestGenerateTargetWithVariablesHasNoNote(t *testing.T) {
	s := newService(t, fakeBamboo())
	d, err := s.GenerateTarget(bg, GenerateOptions{Name: "prov", PlanKey: "PROJ-PROV"})
	require.NoError(t, err)
	assert.Empty(t, d.Note)
}

// BAW-7 answer 8: preset add writes every repository of the plan branch with
// the branch it builds now, read from the newest finished job of --branch.
func TestGenerateTargetWritesTheBranchEachRepositoryBuildsNow(t *testing.T) {
	s := newService(t, repoBuilds())
	d, err := s.GenerateTarget(bg, GenerateOptions{Name: "p", PlanKey: "PROJ-PARENT", Branch: "tst", Server: "work"})
	require.NoError(t, err)
	assert.Equal(t, "work", d.Server)
	assert.Equal(t, []config.DraftRepo{{Name: "app", Branch: "develop"}, {Name: "scripts", Branch: "feature/cli-tools"}}, d.Repos)
}

// Without --branch the main plan's newest finished job is read; the draft
// then comments the lines because repositories: needs branch:.
func TestGenerateTargetReadsTheMainPlanWithoutABranch(t *testing.T) {
	s := newService(t, repoBuilds())
	d, err := s.GenerateTarget(bg, GenerateOptions{Name: "p", PlanKey: "PROJ-PARENT"})
	require.NoError(t, err)
	assert.Empty(t, d.Branch)
	assert.Equal(t, []config.DraftRepo{{Name: "app", Branch: "main"}}, d.Repos)
}

// With no finished job on the plan branch bam falls back to the names in the
// newest build's revisions and writes no branch.
func TestGenerateTargetFallsBackToTheNamesOfTheNewestBuild(t *testing.T) {
	p := repoBuilds()
	p.History["PROJ-PARENT1"] = []provider.Build{{Key: "PROJ-PARENT1-2", PlanKey: "PROJ-PARENT1", Number: 2, State: provider.StateSuccess,
		Revisions: []provider.Revision{{Repository: "app", Revision: "a1"}, {Repository: "scripts", Revision: "b2"}},
		Stages:    []provider.Stage{{Jobs: []provider.Job{{Key: "PROJ-PARENT1-JOB1-2"}}}}}}
	s := newService(t, p)
	d, err := s.GenerateTarget(bg, GenerateOptions{Name: "p", PlanKey: "PROJ-PARENT", Branch: "tst"})
	require.NoError(t, err)
	assert.Equal(t, []config.DraftRepo{{Name: "app"}, {Name: "scripts"}}, d.Repos)
}
