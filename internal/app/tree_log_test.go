package app

import (
	"context"
	"testing"

	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/provider/fake"
	"github.com/stretchr/testify/require"
)

// withStage gives b one stage holding one job.
func withStage(b provider.Build, stage, job string, st provider.State) provider.Build {
	b.Stages = []provider.Stage{{Name: stage, State: st, Jobs: []provider.Job{{Key: job, Name: stage, State: st}}}}
	return b
}

// logTreeFake is treeFake where PROJ-API-142's "Run integration builds" job
// starts LAB-INT-46 and a branch build LAB-K8S0-30 with a script, as a
// plan caller does, rather than through child dependencies. LAB-INT-46's
// own job starts LAB-SUB-7, and OPS-NOTIFY-3 is a Child of LAB-INT-46.
func logTreeFake() *fake.Provider {
	p := treeFake()
	root := withStage(tb("PROJ-API-142", "Manual run by jdoe", provider.StateSuccess, 0), "Run integration builds", "PROJ-API-RUN-142", provider.StateSuccess)
	p.History["PROJ-API"][0] = root
	p.Plans["OPS"] = append(p.Plans["OPS"], provider.Plan{Key: "OPS-NOTIFY"})
	p.Plans["LAB"] = []provider.Plan{{Key: "LAB-INT"}, {Key: "LAB-K8S0"}, {Key: "LAB-SUB"}}
	p.History["OPS-NOTIFY"] = []provider.Build{tb("OPS-NOTIFY-3", childOf("LAB-INT-46"), provider.StateSuccess, 40)}
	p.History["LAB-INT"] = []provider.Build{
		withStage(tb("LAB-INT-46", "Manual run by jdoe", provider.StateSuccess, 2), "Deploy", "LAB-INT-JOB1-46", provider.StateSuccess),
		tb("LAB-INT-40", "Manual run by jdoe", provider.StateSuccess, -600),
	}
	p.History["LAB-K8S0"] = []provider.Build{tb("LAB-K8S0-30", "Manual run by jdoe", provider.StateFailed, 3)}
	p.History["LAB-SUB"] = []provider.Build{tb("LAB-SUB-7", "Manual run by jdoe", provider.StateSuccess, 10)}
	p.Logs = map[string][]string{
		"PROJ-API-RUN-142": {
			"PlanCaller.py | The previous run https://bamboo.example.com/browse/LAB-INT-40 failed",
			"PlanCaller.py | The build has been started with build key 'LAB-INT-46'.",
			"PlanCaller.py | ##  Look into build https://bamboo.example.com/browse/LAB-INT-46 for the details  ##",
			"PlanCaller.py | ##  Look into build https://bamboo.example.com/browse/LAB-K8S0-30 for the details  ##",
			"PlanCaller.py | this job: https://bamboo.example.com/browse/PROJ-API-RUN-142/log",
			"PlanCaller.py | elsewhere: https://bamboo.lab.example/browse/OTHER-X-1",
			"PlanCaller.py | again https://bamboo.example.com/browse/LAB-INT-46",
		},
		"LAB-INT-JOB1-46": {"started https://bamboo.example.com/browse/LAB-SUB-7"},
	}
	return p
}

func TestBuildTreeFollowsTheBuildsAJobLogStarted(t *testing.T) {
	tr, err := treeService(t, logTreeFake()).BuildTree(context.Background(), "PROJ-API-142", TreeOptions{})
	require.NoError(t, err)
	require.Equal(t, "PROJ-API-142(LAB-INT-46(LAB-SUB-7,OPS-NOTIFY-3),LAB-K8S0-30,OPS-DEPLOY-88(OPS-SMOKE-51),PROJ-DOCS-30)", shape(tr.Root))
	require.Empty(t, tr.Warnings, "a link Bamboo does not know is not a warning")

	in := tr.Root.Children[0]
	require.True(t, in.FromLog)
	require.Equal(t, "Run integration builds", in.Stage)
	require.Equal(t, "Deploy", in.Build.Stages[0].Name, "a linked build carries its detail")
	require.False(t, in.Children[1].FromLog, "OPS-NOTIFY-3 names its parent in its reason")
	require.False(t, tr.Root.Children[2].FromLog)
}

func TestBuildTreeLogLinksRespectDepth(t *testing.T) {
	p := logTreeFake()
	tr, err := treeService(t, p).BuildTree(context.Background(), "PROJ-API-142", TreeOptions{Depth: 1})
	require.NoError(t, err)
	require.Equal(t, "PROJ-API-142(LAB-INT-46,LAB-K8S0-30,OPS-DEPLOY-88,PROJ-DOCS-30)", shape(tr.Root))
	require.Zero(t, p.LogCalls("LAB-INT-JOB1-46"), "a node at the depth limit gets no children, so its log is not read")
}

func TestBuildTreeReadsAFinishedJobsLogOnce(t *testing.T) {
	p := logTreeFake()
	svc := treeService(t, p)
	_, scan, err := svc.buildTree(context.Background(), "PROJ-API-142", TreeOptions{}, nil, nil)
	require.NoError(t, err)
	tr, _, err := svc.buildTree(context.Background(), "PROJ-API-142", TreeOptions{}, nil, scan)
	require.NoError(t, err)
	require.Contains(t, shape(tr.Root), "LAB-INT-46(LAB-SUB-7")
	require.Equal(t, 1, p.LogCalls("PROJ-API-RUN-142"))
	require.Equal(t, 1, p.LogCalls("LAB-INT-JOB1-46"))
}

func TestBuildTreeRereadsARunningJobsLog(t *testing.T) {
	p := logTreeFake()
	running := withStage(tb("PROJ-API-142", "Manual run by jdoe", provider.StateRunning, 0), "Run integration builds", "PROJ-API-RUN-142", provider.StateRunning)
	p.History["PROJ-API"][0] = running
	p.LogGrowth = map[string][][]string{"PROJ-API-RUN-142": {
		{"starting"},
		{"starting", "Look into build https://bamboo.example.com/browse/LAB-K8S0-30 for the details"},
	}}
	svc := treeService(t, p)
	tr, scan, err := svc.buildTree(context.Background(), "PROJ-API-142", TreeOptions{}, nil, nil)
	require.NoError(t, err)
	require.NotContains(t, shape(tr.Root), "LAB-K8S0-30")
	tr, _, err = svc.buildTree(context.Background(), "PROJ-API-142", TreeOptions{}, nil, scan)
	require.NoError(t, err)
	require.Contains(t, shape(tr.Root), "LAB-K8S0-30")
}

func TestBuildLinks(t *testing.T) {
	require.Equal(t, []string{"LAB-INT-46", "LAB-K8S0-30"}, BuildLinks([]string{
		"see https://bamboo.example.com/browse/LAB-INT-46 for details",
		"job https://bamboo.example.com/browse/LAB-INT-JOB1-46/log",
		"plan https://bamboo.example.com/browse/LAB-INT",
		"branch <https://bamboo.example.com/browse/LAB-K8S0-30>, again /browse/LAB-INT-46.",
		"lowercase /browse/lab-int-46",
	}))
}

// TestBuildTreeSearchesALinkedBuildsProject: LAB is not a project the tree
// searches, but a script started LAB-INT-46, and LAB-NOTE-3 is a Child of
// it. The linked build's project is searched for its children too.
func TestBuildTreeSearchesALinkedBuildsProject(t *testing.T) {
	p := logTreeFake()
	p.Plans["LAB"] = append(p.Plans["LAB"], provider.Plan{Key: "LAB-NOTE"})
	p.History["LAB-NOTE"] = []provider.Build{tb("LAB-NOTE-3", childOf("LAB-INT-46"), provider.StateSuccess, 50)}
	svc := treeService(t, p)
	tr, scan, err := svc.buildTree(context.Background(), "PROJ-API-142", TreeOptions{}, nil, nil)
	require.NoError(t, err)
	require.Equal(t, "PROJ-API-142(LAB-INT-46(LAB-NOTE-3,LAB-SUB-7,OPS-NOTIFY-3),LAB-K8S0-30,OPS-DEPLOY-88(OPS-SMOKE-51),PROJ-DOCS-30)", shape(tr.Root))
	require.Contains(t, scan.plans, "LAB-NOTE", "a watch keeps reading the plans it added")

	tr, _, err = svc.buildTree(context.Background(), "PROJ-API-142", TreeOptions{}, nil, scan)
	require.NoError(t, err)
	require.Contains(t, shape(tr.Root), "LAB-INT-46(LAB-NOTE-3,")
}

// TestBuildTreeALinkedBuildsChildThatIsAnAncestor: the root names LAB-INT-46
// as its parent, so growing LAB-INT-46 by reason reaches the root again,
// which is a loop, not a second copy of the tree.
func TestBuildTreeALinkedBuildsChildThatIsAnAncestor(t *testing.T) {
	p := logTreeFake()
	p.History["PROJ-API"][0].Reason = childOf("LAB-INT-46")
	tr, err := treeService(t, p).BuildTree(context.Background(), "PROJ-API-142", TreeOptions{})
	require.NoError(t, err)
	require.Equal(t, "PROJ-API-142(LAB-INT-46(LAB-SUB-7,OPS-NOTIFY-3,PROJ-API-142^),LAB-K8S0-30,OPS-DEPLOY-88(OPS-SMOKE-51),PROJ-DOCS-30)", shape(tr.Root))
}
