package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/config"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/provider/fake"
	"github.com/stretchr/testify/require"
)

// BAM-23: feature-branch runs from the run form. The pipeline is the one the
// repo-branch design uses: PROJ-PARENT-41 triggered PROJ-CHILD-7, and both
// build repo-a as their default repository.

func repoBuild(key, reason string, minutes int, repo string) provider.Build {
	i := strings.LastIndex(key, "-")
	rev := repo + "-commit"
	return provider.Build{Key: key, PlanKey: key[:i], State: provider.StateSuccess, Reason: reason,
		StartedAt: time.Date(2026, 11, 2, 10, minutes, 0, 0, time.UTC),
		Revision:  rev, Revisions: []provider.Revision{{Repository: repo, Revision: rev}}}
}

// repoFormService is a service on a parent/child pipeline with a branch list.
func repoFormService() (*app.Service, *fake.Provider) {
	svc := testService()
	f := &fake.Provider{
		BaseURL:  labOrigin,
		Projects: []provider.Project{{Key: "PROJ", Name: "Example Project"}},
		Plans:    map[string][]provider.Plan{"PROJ": {{Key: "PROJ-PARENT", ProjectKey: "PROJ"}, {Key: "PROJ-CHILD", ProjectKey: "PROJ"}}},
		Branches: map[string][]provider.Branch{"PROJ-PARENT": {
			{Key: "PROJ-PARENT9", Name: "release/1.0", ShortName: "release-1.0", PlanKey: "PROJ-PARENT"},
			{Key: "PROJ-PARENT10", Name: "feature/PROJ-100", ShortName: "feature-PROJ-100", PlanKey: "PROJ-PARENT"},
		}},
		History: map[string][]provider.Build{
			"PROJ-PARENT": {repoBuild("PROJ-PARENT-41", "Manual run by jdoe", 0, "repo-a")},
			"PROJ-CHILD":  {repoBuild("PROJ-CHILD-7", "Child of PROJ-PARENT-41", 5, "repo-a")},
		},
		RepoBranches:  map[string][]string{"PROJ-PARENT": {"main", "feature/PROJ-100", "feature/PROJ-101"}},
		TriggerResult: provider.Build{Key: "PROJ-PARENT1-1", PlanKey: "PROJ-PARENT1", Number: 1, State: provider.StateQueued},
	}
	svc.P = f
	return svc, f
}

// repoPreset is a preset with repo_branches, as DescribeTargets reports it.
func repoPreset() app.TargetInfo {
	return app.TargetInfo{Name: "my-feature", Plan: "PROJ-PARENT", RepoBranches: []config.RepoBranch{
		{Repository: "repo-a", Branch: "feature/PROJ-101"},
		{Plan: "PROJ-CHILD", Repository: "repo-a", Branch: "feature/PROJ-101-extra"},
	}}
}

// openRepoForm opens the form for the preset the way R does and delivers the
// load result.
func openRepoForm(t *testing.T) (Model, *fake.Provider) {
	t.Helper()
	svc, f := repoFormService()
	m := goldenModel(100, 30)
	m.svc = svc
	p := repoPreset()
	msg := openFormCmd(t.Context(), svc, "", &p, "", 1)()
	m.screen = screenForm
	m.formGen = 1
	m.form = formState{loading: true}
	m, _ = send(m, msg)
	require.False(t, m.form.loading)
	return m, f
}

func typeText(m Model, s string) Model {
	for _, r := range s {
		m, _ = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return m
}

func TestAPresetWithRepoBranchesRunsFromTheForm(t *testing.T) {
	m, f := openRepoForm(t)
	require.Equal(t, []repoRow{
		{Repo: "repo-a", Branch: "feature/PROJ-101"},
		{Plan: "PROJ-CHILD", Repo: "repo-a", Branch: "feature/PROJ-101-extra"},
	}, m.form.repos, "the preset's repo_branches are the form's rows")
	v := m.View()
	require.Contains(t, v, "repo-a")
	require.Contains(t, v, "feature/PROJ-101")
	require.Contains(t, v, "PROJ-CHILD:repo-a")
	require.Contains(t, v, "2 repository branches")

	m, cmd := send(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	require.NotNil(t, cmd)
	msg, ok := cmd().(triggeredMsg)
	require.True(t, ok, "ctrl-R runs the preset instead of refusing it: got %T", msg)
	require.Equal(t, []fake.CreatedBranch{
		{PlanKey: "PROJ-CHILD", Name: "feature-PROJ-101", VCSBranch: "feature/PROJ-101-extra"},
		{PlanKey: "PROJ-PARENT", Name: "feature-PROJ-101", VCSBranch: "feature/PROJ-101"},
	}, f.Created, "ctrl-R creates the plan branches without asking, the root last")
	require.Len(t, f.Triggered, 1)
	require.Equal(t, "PROJ-PARENT3", f.Triggered[0].PlanKey, "the root's plan branch runs")

	m, _ = send(m, msg)
	require.Equal(t, []string{"PROJ-CHILD1"}, m.runChildren["PROJ-PARENT1-1"], "the child plans are followed")
	require.Contains(t, m.status, "created 2, reused 0")
	m.stopWatch()
}

// BAM-26: on the forge-lab Bamboo the status bar still read "queued
// BAMCI-BUILD8-1 on plan branch fix-ignore-undotted-bam-yaml (created 3,
// reused 0)" minutes after the build had finished. The notice of a run with
// repository branches ends when the build leaves the queue, like the plain
// "queued KEY" notice.
func TestThePlanBranchQueuedNoticeEndsWhenTheBuildLeavesTheQueue(t *testing.T) {
	m, _ := openRepoForm(t)
	m, cmd := send(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	msg := cmd().(triggeredMsg)
	m, _ = send(m, msg)
	defer m.stopWatch()
	b := msg.Build
	require.Equal(t, "queued "+b.Key+" on plan branch feature-PROJ-101 (created 2, reused 0)", m.status)

	m, _ = send(m, watchEventMsg{Gen: m.watchGen, Event: app.Event{Type: app.EventState, Build: b, State: b.State}})
	require.Contains(t, m.status, "created 2, reused 0", "still queued")

	b.State = provider.StateRunning
	m, _ = send(m, watchEventMsg{Gen: m.watchGen, Event: app.Event{Type: app.EventState, Build: b, State: b.State}})
	require.Empty(t, m.status)
	require.NotContains(t, m.statusBar(100), "queued")
}

func TestRunningAgainReusesThePlanBranchesAndDeletesNothing(t *testing.T) {
	m, f := openRepoForm(t)
	_, cmd := send(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	cmd()
	made := len(f.Created)

	m2, _ := openRepoForm(t)
	m2.svc.P = f
	_, cmd = send(m2, tea.KeyMsg{Type: tea.KeyCtrlR})
	msg := cmd().(triggeredMsg)
	require.Len(t, f.Created, made, "the second run creates nothing")
	require.Len(t, f.Triggered, 2)
	for _, bp := range msg.Run.Plans {
		require.True(t, bp.Existing, bp.PlanKey)
	}
}

func TestDListsEachPlanBranchItCreatesOrReusesAndTouchesNothing(t *testing.T) {
	m, f := openRepoForm(t)
	m, cmd := send(m, mkKey("d"))
	require.Equal(t, overlayDryRun, m.overlay)
	require.NotNil(t, cmd)
	require.Contains(t, m.View(), "finding the plans")
	m, _ = send(m, cmd())

	v := m.View()
	require.Contains(t, v, "plan branch feature-PROJ-101 on 2 plans")
	require.Contains(t, v, "create  PROJ-PARENT  repo-a=feature/PROJ-101")
	require.Contains(t, v, "create  PROJ-CHILD  repo-a=feature/PROJ-101-extra")
	require.Contains(t, v, "never deletes plan branches")
	require.Contains(t, v, "DELETE /rest/api/latest/plan/<branch key>")
	require.Empty(t, f.Created)
	require.Empty(t, f.Triggered)

	// After a run the branches exist, so d says reuse.
	_, run := send(m, mkKey("esc"))
	_ = run
	m3, _ := openRepoForm(t)
	m3.svc.P = f
	_, cmd = send(m3, tea.KeyMsg{Type: tea.KeyCtrlR})
	cmd()
	m3, cmd = send(m3, mkKey("d"))
	m3, _ = send(m3, cmd())
	require.Contains(t, m3.View(), "reuse PROJ-PARENT3")
	require.Contains(t, m3.View(), "reuse PROJ-CHILD1")
}

func TestDShowsTheReasonABranchCannotBeSet(t *testing.T) {
	m, _ := openRepoForm(t)
	m.form.repos[0].Repo = "repo-z"
	m, cmd := send(m, mkKey("d"))
	m, _ = send(m, cmd())
	require.Contains(t, m.View(), "no plan of this run builds repo-z")
}

func TestBAddsARepositoryBranchRowTypedOrPicked(t *testing.T) {
	svc, f := repoFormService()
	m := formModel()
	m.svc = svc
	m.form.ref = app.PlanRef{PlanKey: "PROJ-PARENT", MasterKey: "PROJ-PARENT"}
	m.form.target, m.form.repos = "", nil
	m.form.choices = map[string][]string{"": {"release/1.0", "feature/PROJ-100"}}

	// Typed: REPO=BRANCH in one go.
	m, _ = send(m, mkKey("b"))
	require.True(t, m.form.namingRepo)
	m = typeText(m, "repo-a=feature/PROJ-101")
	m, _ = send(m, mkKey("enter"))
	require.False(t, m.form.editing)
	require.Equal(t, []repoRow{{Repo: "repo-a", Branch: "feature/PROJ-101", Added: true}}, m.form.repos)

	// Picked: REPO, then up/down through Bamboo's branches.
	m, _ = send(m, mkKey("b"))
	m = typeText(m, "repo-b")
	m, _ = send(m, mkKey("enter"))
	require.True(t, m.form.editing, "the branch is typed or picked next")
	require.Contains(t, m.View(), "pick from 2 branches")
	m, _ = send(m, mkKey("down"))
	require.Equal(t, "release/1.0", m.form.input.Value())
	m, _ = send(m, mkKey("down"))
	m, _ = send(m, mkKey("up"))
	require.Equal(t, "release/1.0", m.form.input.Value())
	m, _ = send(m, mkKey("enter"))
	require.Equal(t, "release/1.0", m.form.repos[1].Branch)

	// A plan's row names the plan, and its branches are fetched.
	m, _ = send(m, mkKey("b"))
	m = typeText(m, "PROJ-CHILD:repo-a")
	m, cmd := send(m, mkKey("enter"))
	require.NotNil(t, cmd)
	require.Equal(t, "PROJ-CHILD", m.form.repos[2].Plan)
	_ = f

	// A name that is not a repository is refused, not added.
	m, _ = send(m, mkKey("esc"))
	m, _ = send(m, mkKey("b"))
	m = typeText(m, "PROJ-CHILD:")
	m, _ = send(m, mkKey("enter"))
	require.Len(t, m.form.repos, 3)
	require.NotNil(t, m.form.err)
}

func TestOnlyAddedRepositoryBranchRowsCanBeRemoved(t *testing.T) {
	m, _ := openRepoForm(t)
	m.form.cursor = len(m.form.fields)
	m, _ = send(m, mkKey("x"))
	require.Len(t, m.form.repos, 2, "a preset's row stays")

	m, _ = send(m, mkKey("b"))
	m = typeText(m, "repo-b=x")
	m, _ = send(m, mkKey("enter"))
	require.Len(t, m.form.repos, 3)
	m, _ = send(m, mkKey("x"))
	require.Len(t, m.form.repos, 2)
}

func TestARepositoryBranchRowWithNoBranchBlocksRun(t *testing.T) {
	m, _ := openRepoForm(t)
	m, _ = send(m, mkKey("b"))
	m = typeText(m, "repo-b")
	m, _ = send(m, mkKey("enter"))
	m, _ = send(m, mkKey("esc"))
	m.revalidate()
	require.False(t, m.canRun())
	_, cmd := send(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	require.Nil(t, cmd)
	require.Contains(t, m.View(), "branch required")
}

func TestARunFailureNamesThePlanBranchesAlreadyCreated(t *testing.T) {
	m, f := openRepoForm(t)
	f.CreateErrs = map[string]error{"PROJ-PARENT": errBoom}
	m, cmd := send(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	msg, ok := cmd().(errMsg)
	require.True(t, ok)
	require.Equal(t, streamRun, msg.Stream)
	m, _ = send(m, msg)
	require.Equal(t, screenForm, m.screen, "the form stays open")
	require.Empty(t, f.Triggered)
}

func TestTheTreeOfARunSearchesTheChildPlanBranches(t *testing.T) {
	m, f := openRepoForm(t)
	f.History["PROJ-CHILD1"] = []provider.Build{repoBuild("PROJ-CHILD1-1", "Child of PROJ-PARENT3-1", 9, "repo-a")}
	f.TriggerResult = provider.Build{Key: "PROJ-PARENT3-1", PlanKey: "PROJ-PARENT3", Number: 1, State: provider.StateSuccess,
		StartedAt: time.Date(2026, 11, 2, 10, 8, 0, 0, time.UTC)}
	f.History["PROJ-PARENT3"] = []provider.Build{f.TriggerResult}

	_, cmd := send(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	msg := cmd().(triggeredMsg)
	kids := msg.Run.ChildBranchKeys()
	require.Equal(t, []string{"PROJ-CHILD1"}, kids)

	var found bool
	for _, r := range runBatch(treePreviewCmd(t.Context(), m.svc, "PROJ-PARENT3-1", 1, true, kids)) {
		if tp, ok := r.(treePreviewMsg); ok {
			require.NoError(t, tp.Err)
			for _, c := range tp.Tree.Root.Children {
				found = found || c.Build.Key == "PROJ-CHILD1-1"
			}
		}
	}
	require.True(t, found, "the child's plan branch build is in the tree")
}

func TestThePresetsTableShowsTheRepositoryBranch(t *testing.T) {
	cols := presetColumns(100, []app.TargetInfo{repoPreset()})
	cells := presetCells(cols, repoPreset(), nil, nil, time.Now())
	require.Contains(t, cells, "repo-a=feature/PROJ-101 +1")

	plain := app.TargetInfo{Name: "smoke", Plan: "PROJ-BUILD", Branch: "develop"}
	require.Equal(t, "develop", presetBranch(plain))
	require.Equal(t, "default", presetBranch(app.TargetInfo{Name: "x", Plan: "PROJ-BUILD"}))
}

// BAM-23: b picks from the repository's branches in version control, with
// the plan's own plan branches as extra suggestions, and falls back to
// typing on a server without the list.
func TestTheBranchPickerListsTheRepositorysBranches(t *testing.T) {
	m, _ := openRepoForm(t)
	require.Equal(t, []string{"main", "feature/PROJ-100", "feature/PROJ-101", "release/1.0"}, m.form.choices[""],
		"Bamboo's repository branches first, then plan branches that are not among them")
}

func TestTheBranchPickerFallsBackToPlanBranchesAndThenTyping(t *testing.T) {
	svc, f := repoFormService()
	delete(f.RepoBranches, "PROJ-PARENT")
	require.Equal(t, []string{"release/1.0", "feature/PROJ-100"}, branchNames(t.Context(), svc, "PROJ-PARENT"),
		"no vcsBranches endpoint: the plan branches stay as suggestions")
	delete(f.Branches, "PROJ-PARENT")
	require.Empty(t, branchNames(t.Context(), svc, "PROJ-PARENT"), "nothing to pick: the branch is typed")

	m := formModel()
	m.svc = svc
	m, _ = send(m, mkKey("b"))
	m = typeText(m, "repo-a")
	m, _ = send(m, mkKey("enter"))
	require.True(t, m.form.editing)
	m = typeText(m, "my/branch")
	m, _ = send(m, mkKey("enter"))
	require.Equal(t, "my/branch", m.form.repos[0].Branch)
}
