package view

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/require"
)

func setPlan() app.SetPlan {
	return app.SetPlan{Name: "release", Stages: [][]app.SetRunPlan{
		{{Target: "publish-specs", Ref: app.PlanRef{PlanKey: "PROJ-SPEC"}}},
		{{Target: "provision", Ref: app.PlanRef{PlanKey: "OPS-PROV"}}, {Target: "deploy", Ref: app.PlanRef{PlanKey: "OPS-DEP"}}},
	}}
}

var setAt = time.Date(2026, 9, 29, 10, 15, 0, 0, time.UTC)

func setEv(typ app.SetEventType, stage int, target string, b provider.Build, st provider.State) app.SetEvent {
	return app.SetEvent{Type: typ, Time: setAt, Set: "release", ID: "release-20260929T101500Z", Stage: stage, Target: target, Build: b, State: st}
}

// setEvents is a two-stage train; deploy ends in deployState and the set in
// its matching result.
func setEvents(deployState provider.State) []app.SetEvent {
	spec := provider.Build{Key: "PROJ-SPEC-12", State: provider.StateSuccess, Duration: 72 * time.Second}
	provRunning := provider.Build{Key: "OPS-PROV-40", State: provider.StateRunning, StartedAt: setAt.Add(3 * time.Minute)}
	prov := provider.Build{Key: "OPS-PROV-40", State: provider.StateSuccess, Duration: 90 * time.Second}
	dep := provider.Build{Key: "OPS-DEP-9", State: deployState, Duration: 48 * time.Second}
	inner := app.Event{Type: app.EventState, Time: setAt, Build: provRunning, State: provider.StateRunning,
		Progress: provider.Progress{Valid: true, Average: 3 * time.Minute, Elapsed: time.Minute, Remaining: 2 * time.Minute, Percent: 0.33}}
	running := setEv(app.SetRunEvent, 1, "provision", provRunning, "")
	running.Event = &inner
	done := setEv(app.SetDone, 1, "", provider.Build{}, provider.StateSuccess)
	stageDone := setEv(app.SetStageDone, 1, "", provider.Build{}, provider.StateSuccess)
	if deployState != provider.StateSuccess {
		done.State, stageDone.State = provider.StateFailed, provider.StateFailed
	}
	return []app.SetEvent{
		setEv(app.SetStageStart, 0, "", provider.Build{}, ""),
		setEv(app.SetRunTriggered, 0, "publish-specs", provider.Build{Key: "PROJ-SPEC-12", State: provider.StateQueued}, ""),
		setEv(app.SetRunDone, 0, "publish-specs", spec, provider.StateSuccess),
		setEv(app.SetStageDone, 0, "", provider.Build{}, provider.StateSuccess),
		setEv(app.SetStageStart, 1, "", provider.Build{}, ""),
		setEv(app.SetRunTriggered, 1, "provision", provider.Build{Key: "OPS-PROV-40", State: provider.StateQueued}, ""),
		setEv(app.SetRunTriggered, 1, "deploy", provider.Build{Key: "OPS-DEP-9", State: provider.StateQueued}, ""),
		running,
		setEv(app.SetRunDone, 1, "deploy", dep, deployState),
		setEv(app.SetRunDone, 1, "provision", prov, provider.StateSuccess),
		stageDone,
		done,
	}
}

func TestSetLinesOneLinePerChange(t *testing.T) {
	o, buf := testOut(false)
	l := NewSetLines(o)
	for _, e := range setEvents(provider.StateFailed) {
		l.Event(e)
	}
	out := buf.String()
	require.Contains(t, out, "2026-09-29T10:15:00Z stage 1 publish-specs PROJ-SPEC-12 triggered\n")
	require.Contains(t, out, "2026-09-29T10:15:00Z stage 2 provision OPS-PROV-40 running\n")
	require.Contains(t, out, "2026-09-29T10:15:00Z stage 2 deploy OPS-DEP-9 failed 48s\n")
	require.Contains(t, out, "set failed: stage 2 (1 of 2 runs failed)\n")
	require.Contains(t, out, "  try: bam logs OPS-DEP-9 --failed\n")
	require.Contains(t, out, "  try: bam set rerun --failed release-20260929T101500Z\n")
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.HasPrefix(line, "2026") {
			require.Regexp(t, `^\S+ stage \d \S+ `, line, "every change names its stage and preset")
		}
	}
}

func TestSetLinesTriggerFailure(t *testing.T) {
	o, buf := testOut(false)
	l := NewSetLines(o)
	e := setEv(app.SetRunDone, 0, "publish-specs", provider.Build{}, provider.StateFailed)
	e.Err = errs.Bamboof("queue full")
	l.Event(e)
	l.Event(setEv(app.SetDone, 0, "", provider.Build{}, provider.StateFailed))
	require.Contains(t, buf.String(), "stage 1 publish-specs failed: queue full\n")
	require.Contains(t, buf.String(), "  publish-specs: queue full\n")
}

var redraw = regexp.MustCompile(`(?s)^.*\x1b\[\d+A\x1b\[J`)

// lastFrame is the output after the last in-place redraw.
func lastFrame(s string) string { return redraw.ReplaceAllString(s, "") }

func TestSetLiveGoldens(t *testing.T) {
	for name, evs := range map[string][]app.SetEvent{
		"set_running": setEvents(provider.StateFailed)[:8],
		"set_failed":  setEvents(provider.StateFailed),
		"set_success": setEvents(provider.StateSuccess),
	} {
		t.Run(name, func(t *testing.T) {
			o, buf := testOut(true)
			o.Width = 80
			o.Now = func() time.Time { return setAt.Add(4 * time.Minute) }
			l := NewSetLive(o, setPlan())
			for _, e := range evs {
				l.Event(e)
			}
			golden(t, name, lastFrame(buf.String()))
		})
	}
}

func TestSetLiveShowsPendingStagesBeforeTheyStart(t *testing.T) {
	o, buf := testOut(true)
	l := NewSetLive(o, setPlan())
	l.Event(setEv(app.SetStageStart, 0, "", provider.Build{}, ""))
	out := lastFrame(buf.String())
	require.Contains(t, out, "stage 2/2")
	require.Regexp(t, `deploy\s+–\s+pending`, out)
}

func TestSetNDJSONEndsWithSetDoneAndSkipsProgress(t *testing.T) {
	var b strings.Builder
	n := NewSetNDJSON(Out{W: &b})
	evs := setEvents(provider.StateSuccess)
	prog := app.Event{Type: app.EventProgress, Time: setAt}
	extra := setEv(app.SetRunEvent, 1, "provision", provider.Build{Key: "OPS-PROV-40"}, "")
	extra.Event = &prog
	evs = append(evs[:len(evs)-1], extra, evs[len(evs)-1])
	for _, e := range evs {
		n.Event(e)
	}
	lines := strings.Split(strings.TrimSpace(b.String()), "\n")
	require.Len(t, lines, len(evs)-1, "the progress-only event is not in the contract")
	var last map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[len(lines)-1]), &last))
	require.Equal(t, "set_done", last["type"])
}

func TestSetLinesNamesARerun(t *testing.T) {
	o, buf := testOut(false)
	l := NewSetLines(o)
	e := setEv(app.SetRunTriggered, 1, "deploy", provider.Build{Key: "OPS-DEP-9", State: provider.StateQueued}, "")
	e.Rerun = 1
	l.Event(e)
	require.Equal(t, "2026-09-29T10:15:00Z stage 2 deploy OPS-DEP-9 rerun 1\n", buf.String())
}

func TestSetLiveStartsFromTheRecordAndMarksARerun(t *testing.T) {
	o, buf := testOut(true)
	o.Width = 80
	l := NewSetLive(o, setPlan())
	l.Seed(app.SetRecord{ID: "release-20260929T101500Z", Stages: []app.StageRecord{
		{Runs: []app.RunRecord{{Target: "publish-specs", BuildKey: "PROJ-SPEC-12", State: provider.StateSuccess}}},
		{Runs: []app.RunRecord{{Target: "provision", BuildKey: "OPS-PROV-40", State: provider.StateSuccess},
			{Target: "deploy", BuildKey: "OPS-DEP-9", State: provider.StateFailed}}},
	}})
	l.Event(setEv(app.SetStageStart, 1, "", provider.Build{}, ""))
	out := lastFrame(buf.String())
	require.Regexp(t, `publish-specs\s+PROJ-SPEC-12\s+Success`, out, "not pending: the record knows it")
	require.Regexp(t, `deploy\s+OPS-DEP-9\s+Failed`, out)

	e := setEv(app.SetRunTriggered, 1, "deploy", provider.Build{Key: "OPS-DEP-9", State: provider.StateQueued}, "")
	e.Rerun = 1
	l.Event(e)
	require.Regexp(t, `deploy\s+OPS-DEP-9\s+\S+ \(rerun 1\)`, lastFrame(buf.String()))
}

func matrixPlan() app.SetPlan {
	return app.SetPlan{Name: "regions", Stages: [][]app.SetRunPlan{{
		{Name: "deploy[env=qa]", Target: "deploy", Ref: app.PlanRef{PlanKey: "OPS-DEP"}},
		{Name: "deploy[env=prod]", Target: "deploy", Ref: app.PlanRef{PlanKey: "OPS-DEP"}},
	}}}
}

func TestSetLiveKeysMatrixRunsByName(t *testing.T) {
	o, buf := testOut(true)
	o.Width = 100
	l := NewSetLive(o, matrixPlan())
	for _, e := range []app.SetEvent{
		{Type: app.SetRunTriggered, Time: setAt, Target: "deploy", Run: "deploy[env=qa]", Build: provider.Build{Key: "OPS-DEP-1", State: provider.StateQueued}},
		{Type: app.SetRunTriggered, Time: setAt, Target: "deploy", Run: "deploy[env=prod]", Build: provider.Build{Key: "OPS-DEP-2", State: provider.StateQueued}},
		{Type: app.SetRunDone, Time: setAt, Target: "deploy", Run: "deploy[env=prod]", Build: provider.Build{Key: "OPS-DEP-2"}, State: provider.StateFailed},
	} {
		l.Event(e)
	}
	out := lastFrame(buf.String())
	require.Regexp(t, `deploy\[env=qa\]\s+OPS-DEP-1`, out)
	require.Regexp(t, `deploy\[env=prod\]\s+OPS-DEP-2\s+Failed`, out)
	require.Equal(t, 2, strings.Count(out, "deploy["), "two rows, not one per preset")
}

func TestSetLinesNameTheMatrixRun(t *testing.T) {
	o, buf := testOut(false)
	l := NewSetLines(o)
	l.Event(app.SetEvent{Type: app.SetRunTriggered, Time: setAt, Target: "deploy", Run: "deploy[env=qa]", Build: provider.Build{Key: "OPS-DEP-1"}})
	require.Equal(t, "2026-09-29T10:15:00Z stage 1 deploy[env=qa] OPS-DEP-1 triggered\n", buf.String())
}
