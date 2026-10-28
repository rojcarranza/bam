package app

import (
	"context"
	"errors"
	"testing"

	"github.com/r0jjames/bam-cli/internal/config"
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

func TestPresetTypedInWrongCaseGetsHint(t *testing.T) {
	s := newService(t, fakeBamboo())
	_, err := s.ResolvePlan(bg, "Provision-Lab", "")
	var e *errs.Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, `"Provision-Lab" is neither a preset nor a plan key`, e.What)
	assert.Equal(t, "preset names are lowercase: did you mean provision-lab?", e.Try)

	_, err = s.ResolveBuild(bg, BuildArg{Arg: "Provision-Lab"})
	require.ErrorAs(t, err, &e)
	assert.Equal(t, "preset names are lowercase: did you mean provision-lab?", e.Try)

	for arg, try := range map[string]string{
		"PROJ-BUILD-":    "did you mean PROJ-BUILD?",
		"PROJ-BUILD#482": "its plan, PROJ-BUILD",
		"PROJ-BUILD-#48": "its plan, PROJ-BUILD",
	} {
		_, err = s.ResolvePlan(bg, arg, "")
		require.ErrorAs(t, err, &e)
		assert.Equal(t, try, e.Try, arg)
	}

	_, err = s.ResolvePlan(bg, "Nope", "")
	require.ErrorAs(t, err, &e)
	assert.Equal(t, "bam preset list, or a plan key such as PROJ-PLAN", e.Try)
}

func TestResolvePlanErrors(t *testing.T) {
	s := newService(t, fakeBamboo())

	_, err := s.ResolvePlan(bg, "provison-lab", "")
	require.Error(t, err)
	assert.Equal(t, errs.KindUsage, errs.KindOf(err))
	assert.Contains(t, err.Error(), `unknown preset "provison-lab"`)
	assert.Contains(t, err.Error(), "provision-lab")
	var e *errs.Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, "did you mean provision-lab? bam preset list shows them all", e.Try)

	_, err = s.ResolvePlan(bg, "proj plan", "")
	assert.Equal(t, errs.KindUsage, errs.KindOf(err))

	_, err = s.ResolvePlan(bg, "PROJ-PROV", "devlop")
	require.Error(t, err)
	assert.Equal(t, errs.KindUsage, errs.KindOf(err))
	assert.Contains(t, err.Error(), `branch "devlop" not found on PROJ-PROV`)
	require.ErrorAs(t, err, &e)
	assert.Equal(t, "did you mean develop? bam plan branches PROJ-PROV lists them all", e.Try)

	_, err = s.ResolvePlan(bg, "PROJ-PROV", "zzzzzz")
	require.ErrorAs(t, err, &e)
	assert.Equal(t, "bam plan branches PROJ-PROV (omit --branch for the default branch)", e.Try)
}

func TestResolveBuild(t *testing.T) {
	s := newService(t, fakeBamboo())

	key, err := s.ResolveBuild(bg, BuildArg{Arg: "PROJ-BUILD-7"})
	require.NoError(t, err)
	assert.Equal(t, "PROJ-BUILD-7", key)

	key, err = s.ResolveBuild(bg, BuildArg{Arg: "PROJ-BUILD-007"})
	require.NoError(t, err)
	assert.Equal(t, "PROJ-BUILD-7", key, "leading zeros are dropped")

	for _, arg := range []string{"PROJ-BUILD-0", "PROJ-BUILD-99999999999999999999"} {
		_, err = s.ResolveBuild(bg, BuildArg{Arg: arg})
		var ze *errs.Error
		require.ErrorAs(t, err, &ze, arg)
		assert.Equal(t, errs.KindUsage, ze.Kind)
		assert.Equal(t, "its latest build: pass the plan key, PROJ-BUILD", ze.Try)
	}

	key, err = s.ResolveBuild(bg, BuildArg{Arg: "PROJ-BUILD"})
	require.NoError(t, err)
	assert.Equal(t, "PROJ-BUILD-482", key, "a plan means its latest build")

	key, err = s.ResolveBuild(bg, BuildArg{Arg: "provision-lab"})
	require.NoError(t, err)
	assert.Equal(t, "PROJ-PROV12-9", key, "a target means the latest build on its branch")

	_, err = s.ResolveBuild(bg, BuildArg{})
	assert.Equal(t, errs.KindUsage, errs.KindOf(err))

	_, err = s.ResolveBuild(bg, BuildArg{Arg: "PROJ-BUILD-abc"})
	require.Error(t, err)
	assert.Equal(t, errs.KindUsage, errs.KindOf(err))
	var e *errs.Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, `"PROJ-BUILD-abc" is not a build key, a plan key or a preset`, e.What)
	assert.Equal(t, "a build key such as PROJ-PLAN-12, a plan key such as PROJ-PLAN, or bam preset list", e.Try)

	_, err = s.ResolveBuild(bg, BuildArg{Arg: "482"})
	require.ErrorAs(t, err, &e)
	assert.Equal(t, "a build number needs its plan, as in PROJ-PLAN-482", e.Try)
	require.NoError(t, s.State.SetLast(s.Cfg.RepoRoot, LastRecord{BuildKey: "PROJ-BUILD-480", Origin: s.Origin}))
	_, err = s.ResolveBuild(bg, BuildArg{Arg: "482"})
	require.ErrorAs(t, err, &e)
	assert.Equal(t, "did you mean PROJ-BUILD-482?", e.Try, "a bare number takes the plan of the last build run here")
	_, err = s.ResolveBuild(bg, BuildArg{Arg: "#482"})
	require.ErrorAs(t, err, &e)
	assert.Equal(t, "did you mean PROJ-BUILD-482?", e.Try, "Bamboo's '#' before a bare number")

	_, err = s.ResolveBuild(bg, BuildArg{Arg: "PROJ-BUILD-INT-482"})
	require.ErrorAs(t, err, &e)
	assert.Equal(t, "that is job INT of PROJ-BUILD-482: pass PROJ-BUILD-482, and --job INT for bam logs", e.Try)

	for _, arg := range []string{"PROJ-BUILD-#482", "PROJ-BUILD#482", "PROJ-BUILD-482-"} {
		_, err = s.ResolveBuild(bg, BuildArg{Arg: arg})
		require.ErrorAs(t, err, &e)
		assert.Equal(t, "did you mean PROJ-BUILD-482?", e.Try, arg)
	}

	_, err = s.ResolveBuild(bg, BuildArg{Arg: "PROJ"})
	require.ErrorAs(t, err, &e)
	assert.Equal(t, errs.KindUsage, e.Kind)
	assert.Equal(t, `"PROJ" is a project key, not a plan key`, e.What)
	assert.Equal(t, "bam plan list PROJ to see its plans", e.Try)
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
	key, _ = s.ResolveFrom(bg, ref, "007")
	assert.Equal(t, "PROJ-PROV12-7", key, "leading zeros are dropped")
	_, err = s.ResolveFrom(bg, ref, "0")
	assert.EqualError(t, err, "--from takes a build number from 1 up, not 0")
	assert.Equal(t, errs.KindUsage, errs.KindOf(err))
	key, _ = s.ResolveFrom(bg, ref, "PROJ-PROV-3")
	assert.Equal(t, "PROJ-PROV-3", key)
	key, _ = s.ResolveFrom(bg, ref, "PROJ-PROV-03")
	assert.Equal(t, "PROJ-PROV-3", key)
	_, err = s.ResolveFrom(bg, ref, "PROJ-PROV-0")
	assert.Equal(t, errs.KindUsage, errs.KindOf(err))
	_, err = s.ResolveFrom(bg, ref, "yesterday")
	assert.Equal(t, errs.KindUsage, errs.KindOf(err))

	key, err = s.ResolveFrom(bg, ref, "LAST")
	require.NoError(t, err)
	assert.Equal(t, "PROJ-PROV12-8", key, "last is matched in any case")
	_, err = s.ResolveFrom(bg, ref, "proj-prov-3")
	var e *errs.Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, "keys are uppercase: did you mean PROJ-PROV-3?", e.Try)
	_, err = s.ResolveFrom(bg, ref, "#5")
	require.ErrorAs(t, err, &e)
	assert.Equal(t, "drop Bamboo's '#': --from 5", e.Try)
	for _, from := range []string{"PROJ-PROV-#5", "PROJ-PROV#5", "PROJ-PROV-5-"} {
		_, err = s.ResolveFrom(bg, ref, from)
		require.ErrorAs(t, err, &e)
		assert.Equal(t, "did you mean PROJ-PROV-5?", e.Try, from)
	}
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

func TestUnknownTargetWithNoPresetsSaysHowToAddOne(t *testing.T) {
	err := UnknownTarget(&config.Config{Machine: &config.MachineFile{Version: 1}}, "deploy-qa")
	var e *errs.Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, errs.KindUsage, e.Kind)
	assert.Equal(t, "no presets are configured", e.Why)
	assert.Equal(t, "keys are uppercase: did you mean DEPLOY-QA? or, to make a preset by that name: bam preset add deploy-qa --plan KEY", e.Try)
}

func TestUnknownTargetThatIsALowercaseKeySuggestsTheKey(t *testing.T) {
	cfg := &config.Config{Machine: &config.MachineFile{Version: 1}}
	var e *errs.Error
	require.ErrorAs(t, UnknownTarget(cfg, "proj-build"), &e)
	assert.Equal(t, errs.KindUsage, e.Kind)
	assert.Equal(t, "keys are uppercase: did you mean PROJ-BUILD? or, to make a preset by that name: bam preset add proj-build --plan KEY", e.Try)
	require.ErrorAs(t, UnknownTarget(cfg, "proj-build-482"), &e)
	assert.Equal(t, "keys are uppercase: did you mean PROJ-BUILD-482?", e.Try, "a build key is no preset name to add")
	require.ErrorAs(t, UnknownTarget(cfg, "deploy"), &e)
	assert.Contains(t, e.Try, "like PROJ-PLAN", "a name that is no key keeps the example")
}

func TestUnknownTargetAmongPresetsSkipsTheKeyExample(t *testing.T) {
	cfg := &config.Config{Machine: &config.MachineFile{Version: 1, Targets: map[string]config.Target{"web": {Plan: "PROJ-BUILD"}}}}
	var e *errs.Error
	require.ErrorAs(t, UnknownTarget(cfg, "nope"), &e)
	assert.Equal(t, "presets: web", e.Why)
	assert.Equal(t, "bam preset list", e.Try, "a plain name says nothing about plan keys")
	require.ErrorAs(t, UnknownTarget(cfg, "proj-build"), &e)
	assert.Equal(t, "bam preset list; keys are uppercase: did you mean PROJ-BUILD?", e.Try)
}

func TestResolvePlanWithBuildKeyPointsAtItsPlan(t *testing.T) {
	s := newService(t, fakeBamboo())
	_, err := s.ResolvePlan(bg, "PROJ-PLAN-12", "")
	var e *errs.Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, errs.KindUsage, e.Kind)
	assert.Equal(t, `"PROJ-PLAN-12" is a build key, not a plan key`, e.What)
	assert.Equal(t, "its plan, PROJ-PLAN", e.Try)
}
