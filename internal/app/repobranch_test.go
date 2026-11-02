package app

import (
	"context"
	"testing"

	"github.com/r0jjames/bam-cli/internal/config"
	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/provider/fake"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Issue #130: set a repository's branch on a run and carry it into the child
// pipelines. The run's last tree: PROJ-PARENT-41 triggered PROJ-CHILD-7 and
// PROJ-MID-3; PROJ-MID-3 triggered PROJ-GRAND-2. PROJ-PARENT, PROJ-CHILD and
// PROJ-GRAND build repo-a as their default repository; PROJ-MID builds
// repo-m; PROJ-GRAND also builds repo-b. PROJ-SOLO is no child of the
// parent but builds repo-a; PROJ-NEW has never built.

// rb is a build with its repositories, the first one the default, as
// GetBuild reports them.
func rb(b provider.Build, repos ...string) provider.Build {
	for i, r := range repos {
		rev := r + "-commit"
		if i == 0 {
			b.Revision = rev
		}
		b.Revisions = append(b.Revisions, provider.Revision{Repository: r, Revision: rev})
	}
	return b
}

func repoFake() *fake.Provider {
	return &fake.Provider{
		BaseURL: "https://bamboo.example.com",
		Plans: map[string][]provider.Plan{"PROJ": {
			{Key: "PROJ-PARENT"}, {Key: "PROJ-CHILD"}, {Key: "PROJ-MID"}, {Key: "PROJ-GRAND"}, {Key: "PROJ-SOLO"}, {Key: "PROJ-NEW"},
		}},
		History: map[string][]provider.Build{
			"PROJ-PARENT": {rb(tb("PROJ-PARENT-41", "Manual run by jdoe", provider.StateSuccess, 0), "repo-a")},
			"PROJ-CHILD":  {rb(tb("PROJ-CHILD-7", childOf("PROJ-PARENT-41"), provider.StateSuccess, 5), "repo-a")},
			"PROJ-MID":    {rb(tb("PROJ-MID-3", childOf("PROJ-PARENT-41"), provider.StateSuccess, 5), "repo-m")},
			"PROJ-GRAND":  {rb(tb("PROJ-GRAND-2", childOf("PROJ-MID-3"), provider.StateSuccess, 9), "repo-a", "repo-b")},
			"PROJ-SOLO":   {rb(tb("PROJ-SOLO-5", "Manual run by jdoe", provider.StateSuccess, -90), "repo-a")},
		},
	}
}

func repoService(t *testing.T, p *fake.Provider) *Service {
	t.Helper()
	svc := newService(t, p)
	svc.Cfg.Project.Projects = []string{"PROJ"}
	return svc
}

func overrides(t *testing.T, flags ...string) []config.RepoBranch {
	t.Helper()
	rbs, err := RepoBranchOverrides(flags, nil)
	require.NoError(t, err)
	return rbs
}

func parentRef() PlanRef { return PlanRef{PlanKey: "PROJ-PARENT", MasterKey: "PROJ-PARENT"} }

func TestRepoBranchCarriesTheBranchIntoChildPlans(t *testing.T) {
	p := repoFake()
	svc := repoService(t, p)
	rp, err := svc.PlanRepoBranches(context.Background(), parentRef(),
		overrides(t, "repo-a=feature/PROJ-101", "PROJ-CHILD:repo-a=feature/PROJ-101-extra"))
	require.NoError(t, err)

	assert.Equal(t, "feature-PROJ-101", rp.Name, "one name for every plan, without a slash")
	assert.Equal(t, []BranchPlan{
		{PlanKey: "PROJ-PARENT", Name: "feature-PROJ-101", Repository: "repo-a", VCSBranch: "feature/PROJ-101"},
		{PlanKey: "PROJ-CHILD", Parent: "PROJ-PARENT", Name: "feature-PROJ-101", Repository: "repo-a", VCSBranch: "feature/PROJ-101-extra"},
		// PROJ-MID builds repo-m but carries the name on to PROJ-GRAND.
		{PlanKey: "PROJ-MID", Parent: "PROJ-PARENT", Name: "feature-PROJ-101"},
		{PlanKey: "PROJ-GRAND", Parent: "PROJ-MID", Name: "feature-PROJ-101", Repository: "repo-a", VCSBranch: "feature/PROJ-101"},
	}, rp.Plans)
	assert.Empty(t, rp.Warnings)
	assert.Empty(t, p.Created, "planning creates nothing")
	assert.Empty(t, p.Triggered)
}

func TestRepoBranchCreatesChildBranchesBeforeTheRoot(t *testing.T) {
	p := repoFake()
	svc := repoService(t, p)
	ctx := context.Background()
	rp, err := svc.PlanRepoBranches(ctx, parentRef(), overrides(t, "repo-a=feature/PROJ-101", "PROJ-CHILD:repo-a=feature/PROJ-101-extra"))
	require.NoError(t, err)
	require.NoError(t, svc.CreateRepoBranches(ctx, &rp))

	assert.Equal(t, []fake.CreatedBranch{
		{PlanKey: "PROJ-GRAND", Name: "feature-PROJ-101", VCSBranch: "feature/PROJ-101"},
		{PlanKey: "PROJ-MID", Name: "feature-PROJ-101"},
		{PlanKey: "PROJ-CHILD", Name: "feature-PROJ-101", VCSBranch: "feature/PROJ-101-extra"},
		{PlanKey: "PROJ-PARENT", Name: "feature-PROJ-101", VCSBranch: "feature/PROJ-101"},
	}, p.Created, "the deepest first, the root last")
	assert.Equal(t, "PROJ-PARENT1", rp.Plans[0].BranchKey)
	assert.True(t, rp.Plans[0].Created)
	assert.Equal(t, []string{"PROJ-CHILD1", "PROJ-MID1", "PROJ-GRAND1"}, rp.ChildBranchKeys())

	ref := parentRef()
	ref.UseRepoBranches(rp)
	_, err = svc.Run(ctx, ref, VarSet{})
	require.NoError(t, err)
	require.Len(t, p.Triggered, 1)
	assert.Equal(t, "PROJ-PARENT1", p.Triggered[0].PlanKey, "the root's plan branch runs")
	assert.Equal(t, "feature-PROJ-101", ref.Branch)
}

func TestRepoBranchReusesAnExistingPlanBranch(t *testing.T) {
	p := repoFake()
	p.Branches = map[string][]provider.Branch{"PROJ-CHILD": {
		{Key: "PROJ-CHILD4", ShortName: "feature-PROJ-101", PlanKey: "PROJ-CHILD", URL: "https://bamboo.example.com/browse/PROJ-CHILD4"},
	}}
	svc := repoService(t, p)
	ctx := context.Background()
	rp, err := svc.PlanRepoBranches(ctx, parentRef(), overrides(t, "PROJ-CHILD:repo-a=feature/PROJ-101"))
	require.NoError(t, err)
	require.Equal(t, []BranchPlan{
		{PlanKey: "PROJ-PARENT", Name: "feature-PROJ-101"},
		{PlanKey: "PROJ-CHILD", Parent: "PROJ-PARENT", Name: "feature-PROJ-101", BranchKey: "PROJ-CHILD4",
			URL: "https://bamboo.example.com/browse/PROJ-CHILD4", Repository: "repo-a", VCSBranch: "feature/PROJ-101", Existing: true},
	}, rp.Plans, "a child-only override still runs the parent on the shared name")
	assert.Equal(t, []string{"plan branch feature-PROJ-101 already exists on PROJ-CHILD4; bam reuses it as it is and cannot check which branch it builds; to build another branch there, delete it in Bamboo first"}, rp.Warnings)

	require.NoError(t, svc.CreateRepoBranches(ctx, &rp))
	assert.Equal(t, []fake.CreatedBranch{{PlanKey: "PROJ-PARENT", Name: "feature-PROJ-101"}}, p.Created)
}

func TestRepoBranchRefusesADisabledPlanBranch(t *testing.T) {
	p := repoFake()
	p.Branches = map[string][]provider.Branch{"PROJ-PARENT": {
		{Key: "PROJ-PARENT9", ShortName: "feature-PROJ-101", Disabled: true, URL: "https://bamboo.example.com/browse/PROJ-PARENT9"},
	}}
	_, err := repoService(t, p).PlanRepoBranches(context.Background(), parentRef(), overrides(t, "repo-a=feature/PROJ-101"))
	assertUsage(t, err, "plan branch feature-PROJ-101 of PROJ-PARENT is disabled", "enable it in Bamboo: https://bamboo.example.com/browse/PROJ-PARENT9")
}

func TestRepoBranchChecksEveryOverrideBeforeAnythingChanges(t *testing.T) {
	cases := map[string]struct {
		flags    []string
		root     string
		what, in string // in: text the Why or Try holds
	}{
		"second repository":         {[]string{"repo-b=feature/x"}, "PROJ-PARENT", "repo-b is not the default repository of PROJ-GRAND", "PROJ-GRAND's is repo-a"},
		"no plan builds it":         {[]string{"repo-z=feature/x"}, "PROJ-PARENT", "no plan of this run builds repo-z", "PROJ-MID (repo-m)"},
		"named plan, other default": {[]string{"PROJ-MID:repo-a=feature/x"}, "PROJ-PARENT", "PROJ-MID does not build repo-a", "PROJ-MID-3, built repo-m"},
		"named plan, second repo":   {[]string{"PROJ-GRAND:repo-b=feature/x"}, "PROJ-PARENT", "repo-b is not the default repository of PROJ-GRAND", "Bamboo can switch only a plan's default repository"},
		"named plan never built":    {[]string{"PROJ-NEW:repo-a=feature/x"}, "PROJ-PARENT", "PROJ-NEW has no build, so bam cannot tell which repositories it builds", "run PROJ-NEW once"},
		"root never built":          {[]string{"repo-a=feature/x"}, "PROJ-NEW", "no plan of this run builds repo-a", "PROJ-NEW (no build)"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			p := repoFake()
			ref := PlanRef{PlanKey: tc.root, MasterKey: tc.root}
			_, err := repoService(t, p).PlanRepoBranches(context.Background(), ref, overrides(t, tc.flags...))
			assertUsage(t, err, tc.what, tc.in)
			assert.Empty(t, p.Created)
			assert.Empty(t, p.Triggered)
		})
	}
}

func TestRepoBranchNamedPlanOutsideTheTreeJoinsTheRun(t *testing.T) {
	p := repoFake()
	rp, err := repoService(t, p).PlanRepoBranches(context.Background(), parentRef(), overrides(t, "PROJ-SOLO:repo-a=feature/PROJ-7"))
	require.NoError(t, err)
	require.Len(t, rp.Plans, 2)
	assert.Equal(t, BranchPlan{PlanKey: "PROJ-SOLO", Parent: "PROJ-PARENT", Name: "feature-PROJ-7", Repository: "repo-a", VCSBranch: "feature/PROJ-7"}, rp.Plans[1])
}

func TestRepoBranchFollowsTheChildrenConfig(t *testing.T) {
	p := repoFake()
	svc := repoService(t, p)
	// The config names PROJ-SOLO as a child of PROJ-CHILD; it never ran as one.
	svc.Cfg.Project.Children = map[string][]string{"PROJ-CHILD": {"PROJ-SOLO"}}
	rp, err := svc.PlanRepoBranches(context.Background(), parentRef(), overrides(t, "PROJ-SOLO:repo-a=feature/PROJ-7"))
	require.NoError(t, err)
	assert.Equal(t, []BranchPlan{
		{PlanKey: "PROJ-PARENT", Name: "feature-PROJ-7"},
		{PlanKey: "PROJ-CHILD", Parent: "PROJ-PARENT", Name: "feature-PROJ-7"},
		{PlanKey: "PROJ-SOLO", Parent: "PROJ-CHILD", Name: "feature-PROJ-7", Repository: "repo-a", VCSBranch: "feature/PROJ-7"},
	}, rp.Plans, "PROJ-CHILD carries the name on to its configured child")
}

func TestRepoBranchWithBranchIsAUsageError(t *testing.T) {
	p := repoFake()
	ref := parentRef()
	ref.Branch = "develop"
	_, err := repoService(t, p).PlanRepoBranches(context.Background(), ref, overrides(t, "repo-a=feature/x"))
	assertUsage(t, err, "--repo-branch cannot be combined with --branch", "drop --branch")
	assert.Zero(t, p.ListCalls("PROJ-PARENT"), "refused before any request")
}

func TestRepoBranchCreateFailureNamesWhatWasCreated(t *testing.T) {
	p := repoFake()
	p.CreateErrs = map[string]error{"PROJ-PARENT": errs.Bamboof("could not create plan branch feature-PROJ-101 of PROJ-PARENT").WithWhy("Bamboo said no")}
	svc := repoService(t, p)
	ctx := context.Background()
	rp, err := svc.PlanRepoBranches(ctx, parentRef(), overrides(t, "PROJ-CHILD:repo-a=feature/PROJ-101"))
	require.NoError(t, err)
	err = svc.CreateRepoBranches(ctx, &rp)
	var e *errs.Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, "Bamboo said no; bam already created PROJ-CHILD1", e.Why)
}

func TestRepoBranchOverridesFlagBeatsPreset(t *testing.T) {
	target := &config.ResolvedTarget{Name: "feature-101", Target: config.Target{Plan: "PROJ-PARENT",
		RepoBranches: config.StringMap{"repo-a": "feature/old", "PROJ-CHILD:repo-a": "feature/PROJ-101-extra"}}}
	got, err := RepoBranchOverrides([]string{"repo-a=feature/PROJ-101"}, target)
	require.NoError(t, err)
	assert.Equal(t, []config.RepoBranch{
		{Repository: "repo-a", Branch: "feature/PROJ-101"},
		{Plan: "PROJ-CHILD", Repository: "repo-a", Branch: "feature/PROJ-101-extra"},
	}, got)
	assert.Equal(t, "feature-PROJ-101", RepoBranchName(got), "the flag names the plan branch")
}

// Found on the forge-lab Bamboo after the captain asked for preset examples:
// a flag for one plan on top of a preset named every plan branch after that
// one plan's branch, so the run created new plan branches called "main".
func TestRepoBranchNameComesFromAnOverrideForEveryPlan(t *testing.T) {
	target := &config.ResolvedTarget{Name: "feature-101", Target: config.Target{Plan: "PROJ-PARENT",
		RepoBranches: config.StringMap{"repo-a": "feature/PROJ-101"}}}
	got, err := RepoBranchOverrides([]string{"PROJ-CHILD:repo-a=main"}, target)
	require.NoError(t, err)
	assert.Equal(t, "feature-PROJ-101", RepoBranchName(got), "the preset's entry for every plan names the plan branch")

	only, err := RepoBranchOverrides([]string{"PROJ-CHILD:repo-a=feature/PROJ-101-extra"}, nil)
	require.NoError(t, err)
	assert.Equal(t, "feature-PROJ-101-extra", RepoBranchName(only), "with only one-plan entries the first names it")
}

func TestParseRepoBranchFlag(t *testing.T) {
	got, err := ParseRepoBranchFlag("PROJ-CHILD:repo-a=feature/a=b")
	require.NoError(t, err)
	assert.Equal(t, config.RepoBranch{Plan: "PROJ-CHILD", Repository: "repo-a", Branch: "feature/a=b"}, got)
	for _, bad := range []string{"repo-a", "repo-a=", "=feature/x", "PROJ-CHILD:=feature/x"} {
		_, err := ParseRepoBranchFlag(bad)
		assertUsage(t, err, "--repo-branch \""+bad+"\" is not REPO=BRANCH or PLAN:REPO=BRANCH", "--repo-branch repo-a=feature/PROJ-101")
	}
	_, err = RepoBranchOverrides([]string{"repo-a=x", "repo-a=y"}, nil)
	assertUsage(t, err, "--repo-branch repo-a is given twice", "")
}

func TestRunRefusesAPresetsRepoBranchesItDidNotApply(t *testing.T) {
	p := repoFake()
	ref := parentRef()
	ref.Target = &config.ResolvedTarget{Name: "feature-101", Target: config.Target{Plan: "PROJ-PARENT", RepoBranches: config.StringMap{"repo-a": "feature/PROJ-101"}}}
	_, err := repoService(t, p).Run(context.Background(), ref, VarSet{})
	assertUsage(t, err, "preset feature-101 sets repo_branches, which only bam run applies", "bam run feature-101")
	assert.Empty(t, p.Triggered)
}

// A release train runs presets without --repo-branch, so one whose preset
// sets repo_branches is a problem found while planning, before any stage
// triggers.
func TestPlanSetRefusesAPresetWithRepoBranches(t *testing.T) {
	svc := setService(t, fakeBamboo())
	svc.Cfg.Project.Targets["publish-specs"] = config.Target{Plan: "PROJ-BUILD", RepoBranches: config.StringMap{"repo-a": "feature/PROJ-101"}}
	_, err := svc.PlanSet(context.Background(), "release", SetOptions{})
	require.Error(t, err)
	assert.Equal(t, errs.KindUsage, errs.KindOf(err))
	assert.Contains(t, errTry(err), "stage 1 publish-specs: preset publish-specs sets repo_branches, which only bam run applies")
	assert.Empty(t, svc.P.(*fake.Provider).Triggered)
}

func TestTreeSearchesExtraPlans(t *testing.T) {
	p := repoFake()
	// The child's plan branch build names the parent's branch build.
	p.History["PROJ-PARENT1"] = []provider.Build{tb("PROJ-PARENT1-1", "Manual run by jdoe", provider.StateSuccess, 20)}
	p.History["PROJ-CHILD1"] = []provider.Build{tb("PROJ-CHILD1-1", childOf("PROJ-PARENT1-1"), provider.StateRunning, 25)}
	svc := repoService(t, p)
	tr, err := svc.BuildTree(context.Background(), "PROJ-PARENT1-1", TreeOptions{})
	require.NoError(t, err)
	assert.Equal(t, "PROJ-PARENT1-1", shape(tr.Root), "branch plans are not searched by default")

	tr, err = svc.BuildTree(context.Background(), "PROJ-PARENT1-1", TreeOptions{Plans: []string{"PROJ-CHILD1"}})
	require.NoError(t, err)
	assert.Equal(t, "PROJ-PARENT1-1(PROJ-CHILD1-1)", shape(tr.Root))
}

func assertUsage(t *testing.T, err error, what, in string) {
	t.Helper()
	var e *errs.Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, errs.KindUsage, e.Kind, err.Error())
	assert.Equal(t, what, e.What)
	if in != "" {
		assert.Contains(t, e.Why+" | "+e.Try, in)
	}
}
