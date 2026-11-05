package app

import (
	"testing"

	"github.com/r0jjames/bam-cli/internal/config"
	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/provider/fake"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BAW-7 (direction B): bam reads which branch each repository built in the
// newest finished job of the preset's plan branch. PROJ-PARENT1 is the plan
// branch "tst" of PROJ-PARENT: build 3 is still queued, build 2 finished and
// built scripts on feature/cli-tools, build 1 built it on develop.

// jb is a build with one job; branches is what the job recorded, nil while it
// has not finished.
func jb(key string, st provider.State, branches map[string]string) provider.Build {
	b := tb(key, "Manual run by jdoe", st, 0)
	b.Stages = []provider.Stage{{Name: "Build", Jobs: []provider.Job{{Key: key[:len(key)-2] + "-JOB1" + key[len(key)-2:], Branches: branches}}}}
	return b
}

func repoBuilds() *fake.Provider {
	built := map[string]string{"app": "develop", "scripts": "feature/cli-tools"}
	return &fake.Provider{
		BaseURL: "https://bamboo.example.com",
		Plans:   map[string][]provider.Plan{"PROJ": {{Key: "PROJ-PARENT"}}},
		Branches: map[string][]provider.Branch{"PROJ-PARENT": {
			{Key: "PROJ-PARENT1", Name: "tst", ShortName: "tst", PlanKey: "PROJ-PARENT"}}},
		History: map[string][]provider.Build{
			"PROJ-PARENT1": {
				jb("PROJ-PARENT1-3", provider.StateRunning, nil),
				jb("PROJ-PARENT1-2", provider.StateSuccess, built),
				jb("PROJ-PARENT1-1", provider.StateSuccess, map[string]string{"app": "develop", "scripts": "develop"}),
			},
			"PROJ-PARENT": {jb("PROJ-PARENT-9", provider.StateSuccess, map[string]string{"app": "main"})},
		},
	}
}

func branchRef(t *testing.T, s *Service) PlanRef {
	t.Helper()
	ref, err := s.ResolvePlan(bg, "PROJ-PARENT", "tst")
	require.NoError(t, err)
	require.Equal(t, "PROJ-PARENT1", ref.PlanKey)
	return ref
}

func TestCheckRepositoriesMatchingBranchesAsksNothing(t *testing.T) {
	s := newService(t, repoBuilds())
	c, err := s.CheckRepositories(bg, branchRef(t, s), map[string]string{"scripts": "feature/cli-tools"})
	require.NoError(t, err)
	assert.False(t, c.Needed())
	assert.True(t, c.Known())
	assert.Equal(t, "PROJ-PARENT1-2", c.BuildKey, "the newest build with a finished job, not the queued one")
}

func TestCheckRepositoriesDifferingBranchNamesBothAndTheTab(t *testing.T) {
	s := newService(t, repoBuilds())
	c, err := s.CheckRepositories(bg, branchRef(t, s), map[string]string{"scripts": "feature/x", "app": "develop"})
	require.NoError(t, err)
	assert.Equal(t, []RepoChange{{Name: "scripts", Built: "feature/cli-tools", Want: "feature/x"}}, c.Changes)
	assert.Equal(t, "https://bamboo.example.com/branch/admin/config/configureChainBranchRepositories.action?planKey=PROJ-PARENT1", c.URL)
}

func TestCheckRepositoriesWithoutAFinishedJobCannotTell(t *testing.T) {
	p := repoBuilds()
	p.History["PROJ-PARENT1"] = []provider.Build{jb("PROJ-PARENT1-1", provider.StateSuccess, nil)}
	s := newService(t, p)
	c, err := s.CheckRepositories(bg, branchRef(t, s), map[string]string{"scripts": "feature/x"})
	require.NoError(t, err)
	assert.False(t, c.Known())
	assert.True(t, c.Needed(), "bam asks, as when the branches differ")
	assert.Equal(t, []RepoChange{{Name: "scripts", Want: "feature/x"}}, c.Changes)
}

func TestCheckRepositoriesUnknownNameFailsBeforeTriggering(t *testing.T) {
	s := newService(t, repoBuilds())
	_, err := s.CheckRepositories(bg, branchRef(t, s), map[string]string{"scrips": "feature/x"})
	var e *errs.Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, errs.KindUsage, e.Kind)
	assert.Contains(t, e.Why, "app, scripts", "the message lists the names the last build has")
}

func TestCheckRepositoriesNeedsAPlanBranch(t *testing.T) {
	s := newService(t, repoBuilds())
	ref, err := s.ResolvePlan(bg, "PROJ-PARENT", "")
	require.NoError(t, err)
	_, err = s.CheckRepositories(bg, ref, map[string]string{"app": "main"})
	var e *errs.Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, errs.KindUsage, e.Kind)
}

func TestCheckRepositoriesWithNothingWantedReadsNothing(t *testing.T) {
	s := newService(t, &fake.Provider{BaseURL: "https://bamboo.example.com"})
	c, err := s.CheckRepositories(bg, PlanRef{PlanKey: "PROJ-PARENT", MasterKey: "PROJ-PARENT"}, nil)
	require.NoError(t, err)
	assert.False(t, c.Needed())
}

// BAW-7 answer 5: --repository beats the preset's entry for the same name.
func TestRepositoryWantsFlagBeatsThePreset(t *testing.T) {
	rt := &config.ResolvedTarget{Target: config.Target{Repositories: config.StringMap{"scripts": "feature/cli-tools", "app": "develop"}}}
	got, err := RepositoryWants([]string{"scripts=feature/x"}, rt)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"scripts": "feature/x", "app": "develop"}, got)
	assert.Equal(t, "feature/cli-tools", rt.Repositories["scripts"], "the preset itself is not changed")
}

func TestRepositoryWantsRejectsBadFlags(t *testing.T) {
	for _, flags := range [][]string{{"scripts"}, {"=x"}, {"scripts="}, {"a=b", "a=c"}} {
		_, err := RepositoryWants(flags, nil)
		var e *errs.Error
		require.ErrorAs(t, err, &e, flags)
		assert.Equal(t, errs.KindUsage, e.Kind, flags)
	}
}

// After the first job finished bam reads the branch it really built.
func TestConfirmRepositoriesReportsWhatBuilt(t *testing.T) {
	s := newService(t, repoBuilds())
	changes, ok, err := s.ConfirmRepositories(bg, "PROJ-PARENT1-2", map[string]string{"scripts": "feature/cli-tools", "app": "main"})
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, []RepoChange{{Name: "app", Built: "develop", Want: "main"}}, changes)

	_, ok, err = s.ConfirmRepositories(bg, "PROJ-PARENT1-3", map[string]string{"app": "main"})
	require.NoError(t, err)
	assert.False(t, ok, "no job has finished yet")
}
