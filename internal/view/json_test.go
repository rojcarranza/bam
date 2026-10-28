package view

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildJSONGolden(t *testing.T) {
	b := provider.Build{
		Key: "PROJ-BUILD12-44", URL: "https://bamboo.example.com/browse/PROJ-BUILD12-44", PlanKey: "PROJ-BUILD12", Branch: "develop",
		Number: 44, State: provider.StateFailed, Reason: "Custom build by jdoe", CustomBuild: true,
		StartedAt: fixedNow.Add(-220 * time.Second), FinishedAt: fixedNow, Duration: 220 * time.Second,
		Revisions: []provider.Revision{{Repository: "app", Revision: "a1b2c3d4e5f6"}},
		Stages: []provider.Stage{{Name: "Test", State: provider.StateFailed, Duration: 31 * time.Second,
			Jobs: []provider.Job{{Key: "PROJ-BUILD12-INT-44", URL: "https://bamboo.example.com/browse/PROJ-BUILD12-INT-44", Name: "Integration", State: provider.StateFailed, Duration: 31 * time.Second}}}},
	}
	var buf bytes.Buffer
	require.NoError(t, WriteJSON(&buf, BuildJSON(b)))
	golden(t, "build.json", buf.String())

	var m map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &m))
	assert.Nil(t, m["queued_at"], "zero time is null")
	assert.Equal(t, []any{}, m["labels"], "empty list is [], not null")
	assert.Equal(t, []any{}, m["failed_tests"])
	assert.Equal(t, float64(220000), m["duration_ms"])
	assert.Equal(t, "2026-09-12T12:00:00Z", m["finished_at"])
	assert.Equal(t, "failed", m["state"])
}

func TestPlanJSONNeverBuilt(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, WriteNDJSON(&buf, PlanJSON(provider.Plan{Key: "PROJ-OLD", URL: "https://bamboo.example.com/browse/PROJ-OLD", Name: "Legacy", ProjectKey: "PROJ"})))
	assert.Equal(t, `{"key":"PROJ-OLD","url":"https://bamboo.example.com/browse/PROJ-OLD","name":"Legacy","project_key":"PROJ","last_build":null,"enabled":true}`+"\n", buf.String())
}

func TestRunJSONMasksSecrets(t *testing.T) {
	vs := app.VarSet{Vars: []app.ResolvedVar{
		{Name: "env", Value: "staging", Source: "flag"},
		{Name: "db_password", Value: "hunter2", Source: "env", Secret: true},
		{Name: "region", Value: "eu", PlanValue: "eu", Declared: true, Source: "plan"},
	}}
	doc := RunJSON(provider.Build{Key: "PROJ-BUILD-45", URL: "https://bamboo.example.com/browse/PROJ-BUILD-45"},
		app.PlanRef{PlanKey: "PROJ-BUILD12", MasterKey: "PROJ-BUILD", Branch: "develop"}, vs)
	var buf bytes.Buffer
	require.NoError(t, WriteNDJSON(&buf, doc))
	assert.NotContains(t, buf.String(), "hunter2")
	assert.Equal(t, `{"key":"PROJ-BUILD-45","url":"https://bamboo.example.com/browse/PROJ-BUILD-45","plan_key":"PROJ-BUILD12","branch":"develop","revision":"","revision_from":"","variables":{"db_password":"********","env":"staging"},"warnings":[]}`+"\n", buf.String())
}

func TestRunJSONCarriesTheRevision(t *testing.T) {
	d := RunJSON(provider.Build{Key: "PROJ-BUILD-46"}, app.PlanRef{PlanKey: "PROJ-BUILD", Revision: "abc1234"}, app.VarSet{})
	assert.Equal(t, "abc1234", d.Revision)
	raw, err := json.Marshal(RunJSON(provider.Build{}, app.PlanRef{PlanKey: "PROJ-BUILD"}, app.VarSet{}))
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"revision":""`, "an empty revision is present, as branch is")
}

func TestEventJSON(t *testing.T) {
	b := provider.Build{Key: "PROJ-BUILD-45", State: provider.StateSuccess}
	var buf bytes.Buffer
	require.NoError(t, WriteNDJSON(&buf, EventJSON(app.Event{Type: app.EventJob, Time: fixedNow, Build: b, Name: "Unit", Key: "PROJ-BUILD-UNIT-45", State: provider.StateRunning})))
	assert.Equal(t, `{"type":"job","time":"2026-09-12T12:00:00Z","build_key":"PROJ-BUILD-45","name":"Unit","key":"PROJ-BUILD-UNIT-45","state":"running"}`+"\n", buf.String())

	buf.Reset()
	require.NoError(t, WriteNDJSON(&buf, EventJSON(app.Event{Type: app.EventDone, Time: fixedNow, Build: b, State: provider.StateSuccess})))
	assert.True(t, strings.HasPrefix(buf.String(), `{"type":"done","time":"2026-09-12T12:00:00Z","build_key":"PROJ-BUILD-45","state":"success","build":{"key":"PROJ-BUILD-45"`), buf.String())
}

func TestTargetAndVarsJSONMask(t *testing.T) {
	doc := TargetJSON(app.TargetInfo{Name: "lab", Plan: "PROJ-LAB", Timeout: 45 * time.Minute, Defaults: []app.TargetVar{
		{Name: "db_password", Value: "literal", Secret: true},
		{Name: "api_secret", Value: "${API_SECRET}", EnvRef: "API_SECRET", Secret: true},
		{Name: "env", Value: "staging"},
	}})
	assert.Equal(t, map[string]string{"db_password": "********", "api_secret": "${API_SECRET}", "env": "staging"}, doc.Defaults)
	assert.Equal(t, int64(2700000), doc.TimeoutMS)
	assert.Equal(t, []string{}, doc.Required)

	rows := VarRowsJSON(app.PlanVarsResult{Rows: []app.VarRow{{Name: "db_password", Value: "x", LastUsed: "y", Masked: true}}})
	assert.Equal(t, []VariableDoc{{Name: "db_password", Value: "********", Masked: true, Source: "plan", LastUsed: "********"}}, rows)
}

func TestRunJSONNamesTheBuildARevisionCameFrom(t *testing.T) {
	d := RunJSON(provider.Build{Key: "PROJ-BUILD-46"}, app.PlanRef{PlanKey: "PROJ-BUILD",
		Revision: "9b30b9aba03a4f45881629b03531006b89c1e0e7", RevisionFrom: "PROJ-BUILD-41"}, app.VarSet{})
	assert.Equal(t, "9b30b9aba03a4f45881629b03531006b89c1e0e7", d.Revision, "the full commit, as sent")
	assert.Equal(t, "PROJ-BUILD-41", d.RevisionFrom)

	raw, err := json.Marshal(RunJSON(provider.Build{}, app.PlanRef{PlanKey: "PROJ-BUILD"}, app.VarSet{}))
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"revision":"","revision_from":""`)
}

func TestProgressJSONNamesItsSource(t *testing.T) {
	b := provider.Build{Key: "PROJ-BUILD-45", State: provider.StateRunning}
	data, err := json.Marshal(BuildJSONWithProgress(b, provider.Progress{Valid: true, Average: time.Minute, Elapsed: 30 * time.Second,
		Remaining: 30 * time.Second, Percent: 0.5, Source: "history"}))
	require.NoError(t, err)
	require.Contains(t, string(data), `"source":"history"`)

	data, err = json.Marshal(BuildJSONWithProgress(b, provider.Progress{}))
	require.NoError(t, err)
	require.NotContains(t, string(data), `"source"`)
}
