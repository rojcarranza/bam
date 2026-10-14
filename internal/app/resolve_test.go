package app

import (
	"context"
	"errors"
	"testing"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var bg = context.Background()

func TestKeyShapes(t *testing.T) {
	assert.True(t, IsPlanKey("PROJ-BUILD"))
	assert.True(t, IsPlanKey("PROJ-BUILD12"))
	assert.False(t, IsPlanKey("PROJ-BUILD-12"))
	assert.False(t, IsPlanKey("build"))
	assert.True(t, IsBuildKey("PROJ-BUILD-12"))
	assert.True(t, IsBuildKey("PROJ-BUILD12-5"))
	assert.False(t, IsBuildKey("PROJ-BUILD"))
}

func TestResolvePlanFromTargetUsesTargetBranch(t *testing.T) {
	s := newService(t, fakeBamboo())
	ref, err := s.ResolvePlan(bg, "provision-lab", "")
	require.NoError(t, err)
	assert.Equal(t, "PROJ-PROV", ref.MasterKey)
	assert.Equal(t, "PROJ-PROV12", ref.PlanKey)
	assert.Equal(t, "develop", ref.Branch)
	require.NotNil(t, ref.Target)
	assert.Equal(t, "provision-lab", ref.Target.Name)
}

func TestResolvePlanBranchFlagOverridesTarget(t *testing.T) {
	s := newService(t, fakeBamboo())
	ref, err := s.ResolvePlan(bg, "provision-lab", "feat/foo")
	require.NoError(t, err)
	assert.Equal(t, "PROJ-PROV7", ref.PlanKey)
}

func TestResolvePlanFromKey(t *testing.T) {
	s := newService(t, fakeBamboo())
	ref, err := s.ResolvePlan(bg, "PROJ-BUILD", "")
	require.NoError(t, err)
	assert.Equal(t, PlanRef{PlanKey: "PROJ-BUILD", MasterKey: "PROJ-BUILD"}, ref)
}

func TestResolvePlanErrors(t *testing.T) {
	s := newService(t, fakeBamboo())

	_, err := s.ResolvePlan(bg, "provison-lab", "")
	require.Error(t, err)
	assert.Equal(t, errs.KindUsage, errs.KindOf(err))
	assert.Contains(t, err.Error(), `unknown preset "provison-lab"`)
	assert.Contains(t, err.Error(), "provision-lab")

	_, err = s.ResolvePlan(bg, "proj plan", "")
	assert.Equal(t, errs.KindUsage, errs.KindOf(err))

	_, err = s.ResolvePlan(bg, "PROJ-PROV", "devlop")
	require.Error(t, err)
	assert.Equal(t, errs.KindUsage, errs.KindOf(err))
	assert.Contains(t, err.Error(), `branch "devlop" not found on PROJ-PROV`)
	assert.Contains(t, err.Error(), "develop")
}

func TestResolveBuild(t *testing.T) {
	s := newService(t, fakeBamboo())

	key, err := s.ResolveBuild(bg, BuildArg{Arg: "PROJ-BUILD-7"})
	require.NoError(t, err)
	assert.Equal(t, "PROJ-BUILD-7", key)

	key, err = s.ResolveBuild(bg, BuildArg{Arg: "PROJ-BUILD"})
	require.NoError(t, err)
	assert.Equal(t, "PROJ-BUILD-482", key, "a plan means its latest build")

	key, err = s.ResolveBuild(bg, BuildArg{Arg: "provision-lab"})
	require.NoError(t, err)
	assert.Equal(t, "PROJ-PROV12-9", key, "a target means the latest build on its branch")

	_, err = s.ResolveBuild(bg, BuildArg{})
	assert.Equal(t, errs.KindUsage, errs.KindOf(err))
}

func TestResolveBuildLast(t *testing.T) {
	s := newService(t, fakeBamboo())
	_, err := s.ResolveBuild(bg, BuildArg{Last: true})
	require.Error(t, err)
	assert.Equal(t, errs.KindUsage, errs.KindOf(err))
	assert.Contains(t, err.Error(), "no build triggered from this repository")

	require.NoError(t, s.State.SetLast(s.Cfg.RepoRoot, LastRecord{BuildKey: "PROJ-BUILD-482", Origin: s.Origin}))
	key, err := s.ResolveBuild(bg, BuildArg{Last: true})
	require.NoError(t, err)
	assert.Equal(t, "PROJ-BUILD-482", key)

	require.NoError(t, s.State.SetLast(s.Cfg.RepoRoot, LastRecord{BuildKey: "LAB-X-1", Origin: "http://bamboo.lab.example:8085"}))
	_, err = s.ResolveBuild(bg, BuildArg{Last: true})
	assert.Equal(t, errs.KindConfig, errs.KindOf(err))
}

func TestResolveFrom(t *testing.T) {
	s := newService(t, fakeBamboo())
	ref, err := s.ResolvePlan(bg, "provision-lab", "")
	require.NoError(t, err)

	key, err := s.ResolveFrom(bg, ref, "last")
	require.NoError(t, err)
	assert.Equal(t, "PROJ-PROV12-8", key, "last prefers the most recent manual build")

	key, _ = s.ResolveFrom(bg, ref, "5")
	assert.Equal(t, "PROJ-PROV12-5", key)
	key, _ = s.ResolveFrom(bg, ref, "PROJ-PROV-3")
	assert.Equal(t, "PROJ-PROV-3", key)
	_, err = s.ResolveFrom(bg, ref, "yesterday")
	assert.Equal(t, errs.KindUsage, errs.KindOf(err))
}

func TestBuildForgetsExpiredLastBuild(t *testing.T) {
	s := newService(t, fakeBamboo())
	require.NoError(t, s.State.SetLast(s.Cfg.RepoRoot, LastRecord{BuildKey: "PROJ-BUILD-1", Origin: s.Origin}))
	_, err := s.Build(bg, "PROJ-BUILD-1")
	require.Error(t, err)
	assert.True(t, errors.Is(err, errs.ErrNotFound))
	assert.Contains(t, err.Error(), "no longer exists")
	_, ok, _ := s.State.Last(s.Cfg.RepoRoot)
	assert.False(t, ok)
}

func TestBuildKeepsLastBuildRecordFromAnotherOrigin(t *testing.T) {
	s := newService(t, fakeBamboo())
	require.NoError(t, s.State.SetLast(s.Cfg.RepoRoot, LastRecord{BuildKey: "PROJ-BUILD-1", Origin: "https://other.example.com"}))
	_, err := s.Build(bg, "PROJ-BUILD-1")
	require.Error(t, err)
	assert.True(t, errors.Is(err, errs.ErrNotFound))
	assert.NotContains(t, err.Error(), "no longer exists", "the record belongs to a different server, so bam must not claim to have forgotten it")
	rec, ok, _ := s.State.Last(s.Cfg.RepoRoot)
	require.True(t, ok, "the record from the other origin must survive")
	assert.Equal(t, "https://other.example.com", rec.Origin)
}

func TestRevisionLabels(t *testing.T) {
	typed := PlanRef{Revision: "abc1234ffff"}
	assert.Equal(t, "abc1234ffff", typed.RevisionShort(), "a typed revision prints as given")
	assert.Equal(t, "abc1234ffff", typed.RevisionLabel())

	reused := PlanRef{Revision: "9b30b9aba03a4f45881629b03531006b89c1e0e7", RevisionFrom: "PROJ-BUILD-41"}
	assert.Equal(t, "9b30b9a", reused.RevisionShort())
	assert.Equal(t, "9b30b9a (from #41)", reused.RevisionLabel())

	assert.Equal(t, "", PlanRef{}.RevisionLabel())
	assert.Equal(t, "#41", BuildLabel("PROJ-BUILD12-41"))
}
