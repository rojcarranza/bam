package view

import (
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/view/style"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func progressOut(width int, color bool) Out {
	return Out{Width: width, Style: style.Mode{Color: color}, Now: func() time.Time { return time.Time{} }}
}

func TestBarIsEmptyWithoutAnEstimate(t *testing.T) {
	assert.Equal(t, "", Bar(progressOut(80, false), provider.Progress{}))
	assert.Equal(t, "", Bar(progressOut(80, false), provider.Progress{Valid: true, Percent: 0.5}))
	assert.Equal(t, "", Bar(progressOut(80, false), provider.Progress{Average: time.Minute, Percent: 0.5}))
}

func TestBarShowsPercentAndRemaining(t *testing.T) {
	p := provider.Progress{Valid: true, Average: 3 * time.Minute, Elapsed: 99 * time.Second, Remaining: 81 * time.Second, Percent: 0.55}
	assert.Equal(t, "[###########---------]  55%  ~1m21s left", Bar(progressOut(80, false), p))
}

func TestBarUsesBlocksWithColor(t *testing.T) {
	p := provider.Progress{Valid: true, Average: 2 * time.Minute, Elapsed: 30 * time.Second, Remaining: 90 * time.Second, Percent: 0.25}
	assert.Equal(t, "[█████░░░░░░░░░░░░░░░]  25%  ~1m30s left", Bar(progressOut(80, true), p))
}

func TestBarScalesWithWidth(t *testing.T) {
	p := provider.Progress{Valid: true, Average: time.Minute, Elapsed: 30 * time.Second, Remaining: 30 * time.Second, Percent: 0.5}
	assert.Equal(t, "[#####-----]  50%  ~30s left", Bar(progressOut(20, false), p), "narrow terminals get the floor of ten cells")
	assert.Equal(t, "[############------------]  50%  ~30s left", Bar(progressOut(200, false), p), "wide terminals cap at twenty-four")
	assert.Equal(t, "[##########----------]  50%  ~30s left", Bar(progressOut(0, false), p), "an unknown width means twenty")
}

func TestBarReportsOverrunInsteadOfTimeLeft(t *testing.T) {
	p := provider.Progress{Valid: true, Average: time.Minute, Elapsed: 90 * time.Second, Percent: 1}
	assert.Equal(t, "[####################] 100%  over by 30s", Bar(progressOut(80, false), p))
}

func TestBarAtZeroPercent(t *testing.T) {
	p := provider.Progress{Valid: true, Average: time.Minute, Remaining: time.Minute}
	assert.Equal(t, "[--------------------]   0%  ~1m00s left", Bar(progressOut(80, false), p))
}

func TestBuildDetailShowsTheEstimateOnlyWhileRunning(t *testing.T) {
	o, buf := testOut(false)
	p := provider.Progress{Valid: true, Average: 3 * time.Minute, Elapsed: 99 * time.Second, Remaining: 81 * time.Second, Percent: 0.55}

	require.NoError(t, BuildDetailWithProgress(o, runningBuild(), p))

	assert.Contains(t, buf.String(), "Estimate    3m00s  [#############-----------]  55%  ~1m21s left")

	o, buf = testOut(false)
	require.NoError(t, BuildDetailWithProgress(o, failedResult(), provider.Progress{}))
	assert.NotContains(t, buf.String(), "Estimate")
}

func TestBuildListShowsAnETAColumnOnlyWhenSomethingRuns(t *testing.T) {
	o, buf := testOut(false)
	running := runningBuild()
	p := map[string]provider.Progress{running.Key: {Valid: true, Average: 3 * time.Minute, Elapsed: 99 * time.Second, Remaining: 81 * time.Second, Percent: 0.55}}

	require.NoError(t, BuildListWithProgress(o, []provider.Build{running, failedResult()}, p))

	assert.Contains(t, buf.String(), "ETA")
	assert.Contains(t, buf.String(), "1m21s")

	o, buf = testOut(false)
	require.NoError(t, BuildListWithProgress(o, []provider.Build{failedResult()}, nil))
	assert.NotContains(t, buf.String(), "ETA")
}

func TestBuildJSONCarriesProgressOnlyWhenEstimated(t *testing.T) {
	p := provider.Progress{Valid: true, Average: 3 * time.Minute, Elapsed: 99 * time.Second, Remaining: 81 * time.Second, Percent: 0.55, Stage: "Deploy"}

	doc := BuildJSONWithProgress(runningBuild(), p)

	require.NotNil(t, doc.Progress)
	assert.Equal(t, ProgressDoc{Percent: 0.55, AverageMS: 180000, ElapsedMS: 99000, RemainingMS: 81000, Stage: "Deploy"}, *doc.Progress)
	assert.Nil(t, BuildJSONWithProgress(runningBuild(), provider.Progress{}).Progress)
	assert.Nil(t, BuildJSON(failedResult()).Progress)
}

func TestEventJSONCarriesProgressOnRunningEventsOnly(t *testing.T) {
	p := provider.Progress{Valid: true, Average: 3 * time.Minute, Elapsed: 99 * time.Second, Remaining: 81 * time.Second, Percent: 0.55}

	running := EventJSON(app.Event{Type: app.EventState, Time: fixedNow, Build: runningBuild(), Progress: p, State: provider.StateRunning})
	done := EventJSON(app.Event{Type: app.EventDone, Time: fixedNow, Build: failedResult(), State: provider.StateFailed})

	require.NotNil(t, running.Progress)
	assert.Equal(t, 0.55, running.Progress.Percent)
	assert.Nil(t, done.Progress)
	assert.Nil(t, done.Build.Progress)
}

func TestBarWithinFitsItsBudget(t *testing.T) {
	p := provider.Progress{Valid: true, Average: 4 * time.Minute, Elapsed: 2 * time.Minute, Remaining: 2 * time.Minute, Percent: 0.5}

	for _, budget := range []int{26, 32, 40, 60} {
		got := BarWithin(style.Mode{}, p, budget)
		assert.LessOrEqual(t, len(got), budget, "budget %d: %q", budget, got)
		assert.Contains(t, got, "~2m00s left", "budget %d", budget)
	}
}

func TestBarWithinDropsTheCellsBeforeTheNumbers(t *testing.T) {
	p := provider.Progress{Valid: true, Average: 4 * time.Minute, Elapsed: 2 * time.Minute, Remaining: 2 * time.Minute, Percent: 0.5}

	assert.Equal(t, " 50%  ~2m00s left", BarWithin(style.Mode{}, p, 25), "six cells plus the text need 26 columns")
}

func TestBarWithinIsEmptyWhenThereIsNoRoom(t *testing.T) {
	p := provider.Progress{Valid: true, Average: 4 * time.Minute, Elapsed: 2 * time.Minute, Remaining: 2 * time.Minute, Percent: 0.5}

	assert.Equal(t, "", BarWithin(style.Mode{}, p, 16), "not even the numbers fit")
	assert.Equal(t, "", BarWithin(style.Mode{}, provider.Progress{}, 80))
}
