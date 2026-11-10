package app

import (
	"testing"

	"github.com/r0jjames/bam-cli/internal/errs"
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
	assert.Contains(t, e.Try, "https://bamboo.example.com/chain/admin/createPlanBranch!doDefault.action?buildKey=PROJ-PARENT")
	assert.Len(t, p.Branches["PROJ-PARENT"], 1, "nothing was created")
}

func TestAtWithoutACheckoutIsAUsageError(t *testing.T) {
	s := newService(t, repoBuilds())
	_, err := s.ResolvePlan(bg, "PROJ-PARENT", "@")
	assert.Equal(t, errs.KindUsage, errs.KindOf(err))
}
