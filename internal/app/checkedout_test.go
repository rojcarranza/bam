package app

import (
	"testing"

	"github.com/rojcarranza/bam/internal/errs"
	"github.com/rojcarranza/bam/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BAM-11 P5: "@" is the branch checked out in the working directory, in
// --branch, --repository NAME=@ and preset repositories.

func checkedOut(s *Service, b string) {
	s.Checkout = func() (string, error) { return b, nil }
}

func TestAtInBranchPicksTheCheckedOutPlanBranch(t *testing.T) {
	s := newService(t, repoBuilds())
	checkedOut(s, "tst")
	ref, err := s.ResolvePlan(bg, "PROJ-PARENT", "@")
	require.NoError(t, err)
	assert.Equal(t, "PROJ-PARENT1", ref.PlanKey)
	assert.Equal(t, "tst", ref.Branch, "the branch name, never the marker")
}

func TestAtInRepositoriesWantsTheCheckedOutBranch(t *testing.T) {
	s := newService(t, repoBuilds())
	checkedOut(s, "feature/cli-tools")
	c, err := s.CheckRepositories(bg, branchRef(t, s), map[string]string{"scripts": "@", "app": "develop"})
	require.NoError(t, err)
	assert.False(t, c.Needed(), "scripts already builds feature/cli-tools")

	checkedOut(s, "feature/x")
	c, err = s.CheckRepositories(bg, branchRef(t, s), map[string]string{"scripts": "@"})
	require.NoError(t, err)
	assert.Equal(t, []RepoChange{{Name: "scripts", Built: "feature/cli-tools", Want: "feature/x"}}, c.Changes)
}

func TestAtWithNoPlanBranchSaysSoNamesTheClosestAndLinksWithoutCreating(t *testing.T) {
	p := repoBuilds()
	s := newService(t, p)
	checkedOut(s, "tts")
	_, err := s.ResolvePlan(bg, "PROJ-PARENT", "@")
	var e *errs.Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, errs.KindUsage, errs.KindOf(err))
	assert.Contains(t, err.Error(), `PROJ-PARENT has no plan branch for "tts"`)
	assert.Equal(t, "the closest plan branch is tst", e.Why)
	assert.Contains(t, e.Try, p.CreateBranchURL("PROJ-PARENT"))
	assert.Len(t, p.Branches["PROJ-PARENT"], 1, "nothing was created")
}

// Issue #239: Bamboo names the plan branch it makes for Git branch
// feature/PROJ-101 feature-PROJ-101 and does not say which Git branch a plan
// branch builds, so the name asked for finds it by the server's naming rule.
func TestBranchFindsThePlanBranchBambooNamedAfterIt(t *testing.T) {
	p := fakeBamboo()
	p.Branches["PROJ-PROV"] = append(p.Branches["PROJ-PROV"], provider.Branch{
		Key: "PROJ-PROV0", Name: "Provision lab - feature-PROJ-101", ShortName: "feature-PROJ-101", PlanKey: "PROJ-PROV"})
	s := newService(t, p)

	ref, err := s.ResolvePlan(bg, "PROJ-PROV", "feature/PROJ-101")
	require.NoError(t, err)
	assert.Equal(t, "PROJ-PROV0", ref.PlanKey)
	assert.Equal(t, "feature/PROJ-101", ref.Branch, "the name asked for, which bam status matches the checkout against")

	checkedOut(s, "feature/PROJ-101")
	ref, err = s.ResolvePlan(bg, "PROJ-PROV", "@")
	require.NoError(t, err)
	assert.Equal(t, "PROJ-PROV0", ref.PlanKey)
}

func TestAtWithoutACheckoutIsAUsageError(t *testing.T) {
	s := newService(t, repoBuilds())
	_, err := s.ResolvePlan(bg, "PROJ-PARENT", "@")
	assert.Equal(t, errs.KindUsage, errs.KindOf(err))
}
