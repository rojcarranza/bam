package view

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rojcarranza/bam/internal/app"
	"github.com/rojcarranza/bam/internal/provider"
)

// Intent (BAM-101 piece 1): a group's name becomes its heading, a capital
// first letter with - and _ as spaces.
func TestGroupTitle(t *testing.T) {
	for in, want := range map[string]string{
		"integration":   "Integration",
		"release-train": "Release train",
		"nightly_lab-2": "Nightly lab 2",
		"x":             "X",
		"":              "",
	} {
		assert.Equal(t, want, GroupTitle(in), in)
	}
}

func fin(n int, s provider.State, at time.Time) provider.Build {
	return provider.Build{Number: n, State: s, FinishedAt: at}
}

// Intent: the right end of the heading gives the pipelines, then the failing
// and running counts, leaving out a zero.
func TestGroupState(t *testing.T) {
	failed := []provider.Build{{State: provider.StateFailed}}
	ok := []provider.Build{{State: provider.StateSuccess}}
	run := &provider.Build{State: provider.StateRunning}
	queued := &provider.Build{State: provider.StateQueued}
	g := app.PinGroupView{Pins: []app.PinView{{Results: failed, Current: run}, {Results: ok}, {Results: ok, Current: queued}}}
	assert.Equal(t, "3 pipelines · 1 failing · 1 running", GroupState(g))
	assert.Equal(t, "2 pipelines", GroupState(app.PinGroupView{Pins: g.Pins[1:]}))
	assert.Equal(t, "1 pipeline · 1 failing · 1 running", GroupState(app.PinGroupView{Pins: g.Pins[:1]}))
	assert.Equal(t, "1 pipeline · 1 failing", GroupState(app.PinGroupView{Pins: []app.PinView{{Results: failed}}}))
}

func TestRuledLine(t *testing.T) {
	id := func(s string) string { return s }
	got := RuledLine("Integration", "3 pipelines", 40, id, id, id)
	assert.Equal(t, "Integration ──────────────── 3 pipelines", got)
	assert.Equal(t, 40, len([]rune(got)))
	assert.Equal(t, "Integration ──────", RuledLine("Integration", "3 pipelines", 18, id, id, id), "state left out when it does not fit")
	assert.Equal(t, "Integra…", RuledLine("Integration", "", 8, id, id, id), "title cut to the width")
}

// Intent: the pass record reads in each case: no results, all passed, a
// failing run with a last pass, and no pass at all.
func TestPassRecord(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.Local)
	day := func(d, h int) time.Time { return time.Date(2026, 11, d, h, 0, 0, 0, time.Local) }
	cases := []struct {
		name string
		in   []provider.Build
		want string
	}{
		{"none", nil, ""},
		{"all passed", []provider.Build{fin(3, provider.StateSuccess, day(15, 9)), fin(2, provider.StateSuccess, day(14, 9)), fin(1, provider.StateSuccess, day(12, 9))},
			"3 of 3 passed · passing for 3 builds since 12 Nov"},
		{"failing run", []provider.Build{fin(5, provider.StateFailed, day(16, 10)), fin(4, provider.StateFailed, day(15, 9)), fin(3, provider.StateSuccess, day(14, 9)), fin(2, provider.StateSuccess, day(13, 9))},
			"2 of 4 passed · failing for 2 builds since 15 Nov · last pass #3 2d ago"},
		{"no pass", []provider.Build{fin(2, provider.StateFailed, day(16, 10)), fin(1, provider.StateFailed, day(15, 9))},
			"0 of 2 passed · failing for 2 builds since 15 Nov · no pass in these 2"},
		{"passing again", []provider.Build{fin(3, provider.StateSuccess, day(16, 8)), fin(2, provider.StateFailed, day(15, 9)), fin(1, provider.StateSuccess, day(14, 9))},
			"2 of 3 passed · passing for 1 build since 16 Nov"},
		{"stopped newest", []provider.Build{fin(2, provider.StateStopped, day(16, 8)), fin(1, provider.StateSuccess, day(15, 9))},
			"1 of 2 passed · last pass #1 1d ago"},
		{"no finish time", []provider.Build{{Number: 2, State: provider.StateFailed}, {Number: 1, State: provider.StateSuccess}},
			"1 of 2 passed · failing for 1 build · last pass #1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := PassRecord(c.in, now)
			assert.Equal(t, c.want, got)
			assert.False(t, strings.Contains(got, "\n"))
		})
	}
	require.Equal(t, "", PassRecord([]provider.Build{}, now))
}
