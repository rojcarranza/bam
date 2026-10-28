package view

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/require"
)

func setInfo() app.SetInfo {
	return app.SetInfo{Name: "release", Timeout: 2 * time.Hour, DefinedIn: []string{"/src/app/.bam.yaml"},
		Stages: [][]app.SetRunInfo{
			{{Target: "publish-specs", Plan: "PROJ-SPEC"}},
			{{Target: "provision", Plan: "OPS-PROV", Branch: "release-1"}, {Target: "deploy", Plan: "OPS-DEP", Vars: []app.TargetVar{
				{Name: "deploy_token", Value: "${DEPLOY_TOKEN}", EnvRef: "DEPLOY_TOKEN", Secret: true},
				{Name: "env", Value: "qa"},
				{Name: "ssh_password", Value: "hunter2", Secret: true}}}},
		}}
}

func TestSetsList(t *testing.T) {
	o, buf := testOut(false)
	require.NoError(t, Sets(o, []app.SetInfo{setInfo()}))
	require.Equal(t, "SET      STAGES  RUNS  DEFINED IN\nrelease  2       3     /src/app/.bam.yaml\n", buf.String())
}

func TestSetDetailMasksSecrets(t *testing.T) {
	o, buf := testOut(false)
	require.NoError(t, SetDetail(o, setInfo()))
	out := buf.String()
	require.Contains(t, out, "stage 1  publish-specs\n")
	require.Contains(t, out, "stage 2  provision, deploy\n  provision  OPS-PROV  branch release-1\n  deploy  OPS-DEP  branch default\n")
	require.Contains(t, out, "deploy  deploy_token  ${DEPLOY_TOKEN} (unset)")
	require.Contains(t, out, "deploy  env           qa")
	require.Contains(t, out, "********")
	require.NotContains(t, out, "hunter2")
	require.Contains(t, out, "2h0m0s")
}

func TestSetJSONFieldNames(t *testing.T) {
	data, err := json.Marshal(SetJSON(setInfo()))
	require.NoError(t, err)
	require.JSONEq(t, `{"name":"release","timeout_ms":7200000,"defined_in":["/src/app/.bam.yaml"],
	  "stages":[[{"target":"publish-specs","plan_key":"PROJ-SPEC","branch":"","vars":{},"runs":["publish-specs"]}],
	            [{"target":"provision","plan_key":"OPS-PROV","branch":"release-1","vars":{},"runs":["provision"]},
	             {"target":"deploy","plan_key":"OPS-DEP","branch":"","vars":{"deploy_token":"${DEPLOY_TOKEN}","env":"qa","ssh_password":"********"},"runs":["deploy"]}]]}`, string(data))
}

func TestSetEventJSONFieldNames(t *testing.T) {
	e := app.SetEvent{Type: app.SetRunDone, Time: time.Date(2026, 9, 29, 10, 16, 0, 0, time.UTC),
		Set: "release", ID: "release-20260929T101500Z", Stage: 1, Target: "deploy",
		Build: provider.Build{Key: "OPS-DEP-9", State: provider.StateFailed}, State: provider.StateFailed}
	data, err := json.Marshal(SetEventJSON(e))
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(data, &m))
	for _, k := range []string{"type", "time", "set", "id", "stage", "target", "build", "state"} {
		require.Contains(t, m, k)
	}
	require.Equal(t, "2026-09-29T10:16:00Z", m["time"])
	require.NotContains(t, m, "event", "only run_event carries the inner event")
	require.NotContains(t, m, "error")

	e.Err = errs.Bamboof("queue full").WithTry("bam doctor")
	e.Build = provider.Build{}
	data, _ = json.Marshal(SetEventJSON(e))
	require.JSONEq(t, `{"type":"run_done","time":"2026-09-29T10:16:00Z","set":"release","id":"release-20260929T101500Z",
	  "stage":1,"target":"deploy","state":"failed","rerun":0,"error":{"kind":"bamboo","what":"queue full","try":"bam doctor"}}`, string(data))

	inner := app.Event{Type: app.EventState, Time: e.Time, Build: provider.Build{Key: "OPS-DEP-9"}, State: provider.StateRunning}
	data, _ = json.Marshal(SetEventJSON(app.SetEvent{Type: app.SetRunEvent, Time: e.Time, Target: "deploy", Event: &inner, Build: inner.Build}))
	require.Contains(t, string(data), `"event":{"type":"state"`)
}

func TestSetStatusView(t *testing.T) {
	rec := app.SetRecord{ID: "release-20260929T101500Z", Set: "release", Status: app.SetInterrupted,
		Stages: []app.StageRecord{
			{Runs: []app.RunRecord{{Target: "publish-specs", PlanKey: "PROJ-SPEC", BuildKey: "PROJ-SPEC-12", State: provider.StateSuccess}}},
			{Runs: []app.RunRecord{{Target: "provision", PlanKey: "OPS-PROV", BuildKey: "OPS-PROV-40"},
				{Target: "deploy", PlanKey: "OPS-DEP", Error: "queue full"}}},
		}}
	builds := map[string]provider.Build{"OPS-PROV-40": {Key: "OPS-PROV-40", State: provider.StateRunning}}
	o, buf := testOut(false)
	require.NoError(t, SetStatusView(o, rec, builds))
	out := buf.String()
	require.Contains(t, out, "release  release-20260929T101500Z  interrupted\n")
	require.Contains(t, out, "stage 2/2")
	require.Regexp(t, `provision\s+OPS-PROV-40\s+Running`, out)
	require.Regexp(t, `deploy\s+–\s+pending\s+queue full`, out)
	require.Contains(t, out, "try: bam set resume release-20260929T101500Z")

	data, err := json.Marshal(SetRecordJSON(rec, builds))
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(data, &doc))
	run := doc["stages"].([]any)[1].(map[string]any)["runs"].([]any)[0].(map[string]any)
	require.Equal(t, "running", run["state"], "the live state")
	require.Contains(t, run, "build")
}

func TestSetDryRunShowsEveryStageMasked(t *testing.T) {
	vs := app.VarSet{Vars: []app.ResolvedVar{
		{Name: "cluster_name", Value: "alpha", Declared: true},
		{Name: "db_password", Value: "hunter2", Secret: true, Declared: true},
	}}
	p := app.SetPlan{Name: "release", Stages: [][]app.SetRunPlan{{{Target: "provision",
		Ref: app.PlanRef{PlanKey: "OPS-PROV12", MasterKey: "OPS-PROV", Branch: "develop"}, Vars: vs}}}}
	o, buf := testOut(false)
	require.NoError(t, SetDryRun(o, p))
	out := buf.String()
	require.Contains(t, out, "stage 1/1")
	require.Contains(t, out, "OPS-PROV12")
	require.Contains(t, out, "develop")
	require.Contains(t, out, "cluster_name = alpha")
	require.Contains(t, out, "db_password = ********")
	require.NotContains(t, out, "hunter2")
	require.Contains(t, out, "dry run: nothing triggered")

	data, _ := json.Marshal(SetPlanJSON(p))
	require.NotContains(t, string(data), "hunter2")
	require.Contains(t, string(data), `"plan_key":"OPS-PROV12"`)
	require.True(t, strings.Contains(string(data), `"warnings":[]`))
}

func TestSetEventJSONCarriesTheRerun(t *testing.T) {
	e := app.SetEvent{Type: app.SetRunTriggered, Time: time.Date(2026, 9, 29, 10, 16, 0, 0, time.UTC),
		Stage: 1, Target: "deploy", Build: provider.Build{Key: "OPS-DEP-9"}, Rerun: 2}
	data, err := json.Marshal(SetEventJSON(e))
	require.NoError(t, err)
	require.Contains(t, string(data), `"rerun":2`)
}

func TestSetStatusViewShowsReruns(t *testing.T) {
	rec := app.SetRecord{ID: "release-20260929T101500Z", Set: "release", Status: app.SetFailed,
		Stages: []app.StageRecord{{Runs: []app.RunRecord{
			{Target: "deploy", PlanKey: "OPS-DEP", BuildKey: "OPS-DEP-9", State: provider.StateFailed, Reruns: 2}}}}}
	o, buf := testOut(false)
	require.NoError(t, SetStatusView(o, rec, nil))
	require.Regexp(t, `deploy\s+OPS-DEP-9\s+Failed \(rerun 2\)`, buf.String())
	require.Contains(t, buf.String(), "try: bam set rerun --failed release-20260929T101500Z\n")
	data, err := json.Marshal(SetRecordJSON(rec, nil))
	require.NoError(t, err)
	require.Contains(t, string(data), `"reruns":2`)
}

func matrixInfo() app.SetInfo {
	return app.SetInfo{Name: "regions", DefinedIn: []string{"/repo/.bam.yaml"}, Stages: [][]app.SetRunInfo{{
		{Target: "deploy", Matrix: map[string][]string{"env": {"qa", "prod"}, "region": {"eu"}}, MatrixLine: "env=[qa prod] region=[eu]",
			Runs: []string{"deploy[env=prod,region=eu]", "deploy[env=qa,region=eu]"}},
		{Target: "smoke", Matrix: []map[string]string{{"env": "qa"}}, MatrixLine: "1 variable set", Runs: []string{"smoke[env=qa]"}},
	}}}
}

func TestSetsListCountsExpandedRuns(t *testing.T) {
	o, buf := testOut(false)
	require.NoError(t, Sets(o, []app.SetInfo{matrixInfo()}))
	require.Regexp(t, `regions\s+1\s+3\s`, buf.String())
}

func TestSetDetailShowsMatricesAndRuns(t *testing.T) {
	o, buf := testOut(false)
	require.NoError(t, SetDetail(o, matrixInfo()))
	out := buf.String()
	require.Contains(t, out, "deploy  matrix env=[qa prod] region=[eu]")
	require.Contains(t, out, "smoke  matrix 1 variable set")
	require.Contains(t, out, "    deploy[env=qa,region=eu]\n")
}

func TestSetJSONCarriesMatrixAndRuns(t *testing.T) {
	data, err := json.Marshal(SetJSON(matrixInfo()))
	require.NoError(t, err)
	s := string(data)
	require.Contains(t, s, `"matrix":{"env":["qa","prod"],"region":["eu"]}`)
	require.Contains(t, s, `"matrix":[{"env":"qa"}]`)
	require.Contains(t, s, `"runs":["smoke[env=qa]"]`)
}

func TestSetRecordAndEventJSONCarryNames(t *testing.T) {
	rec := app.SetRecord{ID: "r", Set: "regions", Status: app.SetFailed, Stages: []app.StageRecord{{Runs: []app.RunRecord{
		{Target: "deploy", Name: "deploy[env=qa]", BuildKey: "OPS-DEP-1", State: provider.StateFailed},
		{Target: "smoke", BuildKey: "OPS-SMK-1", State: provider.StateSuccess},
	}}}}
	data, _ := json.Marshal(SetRecordJSON(rec, nil))
	require.Contains(t, string(data), `"name":"deploy[env=qa]"`)
	require.Contains(t, string(data), `"name":"smoke"`, "an old record's run is named after its preset")
	o, buf := testOut(false)
	require.NoError(t, SetStatusView(o, rec, nil))
	require.Regexp(t, `deploy\[env=qa\]\s+OPS-DEP-1`, buf.String())

	ev, _ := json.Marshal(SetEventJSON(app.SetEvent{Type: app.SetRunDone, Time: setAt, Target: "deploy", Run: "deploy[env=qa]"}))
	require.Contains(t, string(ev), `"run":"deploy[env=qa]"`)
}

func TestSetHistoryShowsEachRecordedRun(t *testing.T) {
	start := time.Date(2026, 9, 29, 10, 15, 0, 0, time.UTC)
	end := start.Add(12*time.Minute + 3*time.Second)
	recs := []app.SetRecord{
		{ID: "release-20260929T101500Z", Set: "release", Status: app.SetFailed, StartedAt: start, FinishedAt: &end,
			Stages: []app.StageRecord{
				{Runs: []app.RunRecord{{Target: "publish-specs", State: provider.StateSuccess}}},
				{Runs: []app.RunRecord{{Target: "provision", State: provider.StateFailed}, {Target: "deploy"}}},
			}},
		{ID: "release-20260928T090000Z", Set: "release", Status: app.SetRunning, StartedAt: start.Add(-25 * time.Hour)},
	}
	o, buf := testOut(false)
	require.NoError(t, SetHistory(o, recs))
	require.Equal(t, "RUN                       STATUS   STARTED               DURATION  BUILDS\n"+
		"release-20260929T101500Z  failed   2026-09-29T10:15:00Z  12m03s    1/3 ✓\n"+
		"release-20260928T090000Z  running  2026-09-28T09:15:00Z  –         0/0 ✓\n", buf.String())
}
