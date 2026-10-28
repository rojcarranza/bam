package view

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const url44 = "https://bamboo.example.com/browse/PROJ-BUILD12-44"

func runningBuild() provider.Build {
	return provider.Build{Key: "PROJ-BUILD12-44", URL: url44, State: provider.StateRunning, Agent: "linux-3",
		StartedAt: fixedNow.Add(-2*time.Minute - 3*time.Second),
		Stages: []provider.Stage{
			{Name: "Checkout", State: provider.StateSuccess, Duration: 12 * time.Second, Jobs: []provider.Job{{Key: "PROJ-BUILD12-JOB1-44", Name: "Checkout", State: provider.StateSuccess, Duration: 12 * time.Second}}},
			{Name: "Test", State: provider.StateRunning, Duration: 31 * time.Second, Jobs: []provider.Job{
				{Key: "PROJ-BUILD12-UNIT-44", Name: "Unit", State: provider.StateSuccess, Duration: 28 * time.Second},
				{Key: "PROJ-BUILD12-INT-44", Name: "Integration", State: provider.StateRunning, Duration: 31 * time.Second}}},
			{Name: "Package", State: provider.StateNotBuilt},
		}}
}

func failedResult() provider.Build {
	b := runningBuild()
	b.State = provider.StateFailed
	b.PlanKey = "PROJ-BUILD12"
	b.Number = 44
	b.Duration = 220 * time.Second
	b.Stages[1].State = provider.StateFailed
	b.Stages[1].Jobs[1].State = provider.StateFailed
	return b
}

func TestRunPlanAndQueued(t *testing.T) {
	o, buf := testOut(true)
	vs := app.VarSet{Vars: []app.ResolvedVar{
		{Name: "env", Value: "staging", Source: "flag"},
		{Name: "region", Value: "eu", Source: "preset"},
		{Name: "db_password", Value: "hunter2", Source: "env", Secret: true},
		{Name: "debug", Value: "false", Source: "plan", Declared: true, PlanValue: "false"},
	}}
	RunPlan(o, app.PlanRef{PlanKey: "PROJ-BUILD12", MasterKey: "PROJ-BUILD", Branch: "develop"}, vs)
	Queued(o, provider.Build{Key: "PROJ-BUILD12-44", URL: url44}, false, "")
	assert.Equal(t, ""+
		"Plan     PROJ-BUILD  (branch develop, PROJ-BUILD12)\n"+
		"Vars     env=staging (flag)  region=eu (preset)  db_password=******** (env)  +1 plan default\n"+
		"✓ Queued   PROJ-BUILD12-44   "+url44+"\n"+
		"  watch: bam watch PROJ-BUILD12-44\n", buf.String())
}

func TestRunPlanCountsSeveralPlanDefaults(t *testing.T) {
	o, buf := testOut(true)
	RunPlan(o, app.PlanRef{PlanKey: "PROJ-BUILD", MasterKey: "PROJ-BUILD"}, app.VarSet{Vars: []app.ResolvedVar{
		{Name: "a", Value: "1", Source: "plan"},
		{Name: "b", Value: "2", Source: "plan"},
	}})
	assert.Contains(t, buf.String(), "Vars     +2 plan defaults\n")
}

func TestRunPlanNamesTheRevision(t *testing.T) {
	o, buf := testOut(true)
	RunPlan(o, app.PlanRef{PlanKey: "PROJ-BUILD", MasterKey: "PROJ-BUILD", Revision: "abc1234"}, app.VarSet{})
	assert.Equal(t, ""+
		"Plan     PROJ-BUILD\n"+
		"Revision abc1234\n"+
		"Vars     none\n", buf.String())
}

func TestQueuedWithARevisionSaysWhenItIsChecked(t *testing.T) {
	o, buf := testOut(true)
	Queued(o, provider.Build{Key: "PROJ-BUILD12-44", URL: url44}, false, "abc1234")
	assert.Equal(t, ""+
		"✓ Queued   PROJ-BUILD12-44   "+url44+"\n"+
		"  revision abc1234 is checked when the build starts: bam watch PROJ-BUILD12-44\n", buf.String())

	o, buf = testOut(true)
	Queued(o, provider.Build{Key: "PROJ-BUILD12-44", URL: url44}, true, "abc1234")
	assert.Equal(t, "✓ Queued   PROJ-BUILD12-44   "+url44+"\n", buf.String(), "a watch shows the outcome itself")
}

func TestRevisionNotBuilt(t *testing.T) {
	o, buf := testOut(true)
	RevisionNotBuilt(o, "abc1234")
	assert.Equal(t, "Bamboo could not build revision abc1234\n", buf.String())
}

func TestNDJSONRenderer(t *testing.T) {
	o, buf := testOut(false)
	r := NewNDJSON(o)
	r.Event(app.Event{Type: app.EventState, Time: fixedNow, Build: runningBuild(), State: provider.StateRunning})
	r.Event(app.Event{Type: app.EventDone, Time: fixedNow, Build: failedResult(), State: provider.StateFailed})
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	require.Len(t, lines, 2)
	var last map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[1]), &last))
	assert.Equal(t, "done", last["type"])
	assert.Equal(t, "failed", last["build"].(map[string]any)["state"])
}

func TestNDJSONSkipsProgressEvents(t *testing.T) {
	e := app.Event{Type: app.EventProgress, Time: fixedNow, Build: runningBuild(),
		Progress: provider.Progress{Valid: true, Average: 4 * time.Minute, Elapsed: 2 * time.Minute, Percent: 0.5}}

	o, buf := testOut(false)
	NewNDJSON(o).Event(e)
	assert.Empty(t, buf.String(), "NDJSON types stay state, stage, job and done (spec §7)")
}

func TestRunPlanLabelsAnEditedValue(t *testing.T) {
	o, buf := testOut(true)
	vs := app.VarSet{Vars: []app.ResolvedVar{
		{Name: "cluster_name", Value: "beta", Source: "edit"},
		{Name: "token", Value: "s3cret", Source: "env", Secret: true},
	}}
	RunPlan(o, app.PlanRef{PlanKey: "PROJ-PROV", MasterKey: "PROJ-PROV"}, vs)
	assert.Equal(t, ""+
		"Plan     PROJ-PROV\n"+
		"Vars     cluster_name=beta (edit)  token=******** (env)\n", buf.String())
}

func TestRunPlanLabelsAReusedRevision(t *testing.T) {
	o, buf := testOut(true)
	RunPlan(o, app.PlanRef{PlanKey: "PROJ-BUILD", MasterKey: "PROJ-BUILD",
		Revision: "9b30b9aba03a4f45881629b03531006b89c1e0e7", RevisionFrom: "PROJ-BUILD-41"}, app.VarSet{})
	assert.Equal(t, ""+
		"Plan     PROJ-BUILD\n"+
		"Revision 9b30b9a (from #41)\n"+
		"Vars     none\n", buf.String())
}
