package app

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/rojcarranza/bam/internal/provider"
	"github.com/rojcarranza/bam/internal/provider/fake"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Intent (BAM-42 P4, issue 193): a pin written as a plan key without a
// branch lists the plan's branches that built in the last 7 days, newest
// first, at most 10, and Needs you raises a failed or slow branch like a pin.

// branchPlan gives PROJ-PROV a branch per name, each with one build that
// finished the given time ago; a negative age is a branch that never built.
func branchPlan(t *testing.T, svc *Service, ages map[string]time.Duration) *fake.Provider {
	t.Helper()
	p := boardBamboo(svc.Clock.Now())
	p.Branches["PROJ-PROV"] = nil
	i := 0
	for name, age := range ages {
		i++
		key := fmt.Sprintf("PROJ-PROV%d", 100+i)
		p.Branches["PROJ-PROV"] = append(p.Branches["PROJ-PROV"], provider.Branch{Key: key, Name: "Provision lab - " + name, ShortName: name, PlanKey: "PROJ-PROV"})
		if age >= 0 {
			p.History[key] = []provider.Build{{Key: key + "-1", Number: 1, State: provider.StateSuccess, Duration: time.Minute, FinishedAt: svc.Clock.Now().Add(-age)}}
		}
	}
	svc.P = p
	return p
}

func labels(rows []PinView) []string {
	var out []string
	for _, r := range rows {
		out = append(out, r.Label())
	}
	return out
}

func TestBoardPinListsBranchesBuiltInTheLastWeekNewestFirst(t *testing.T) {
	svc := newService(t, nil)
	branchPlan(t, svc, map[string]time.Duration{
		"old":       8 * 24 * time.Hour,
		"older":     30 * 24 * time.Hour,
		"edge":      6*24*time.Hour + 23*time.Hour,
		"fresh":     time.Hour,
		"never":     -1,
		"yesterday": 24 * time.Hour,
	})
	b, err := svc.Board(context.Background(), pins("lab", "PROJ-PROV"))
	require.NoError(t, err)
	pin := b.Groups[0].Pins[0]

	assert.Equal(t, "Provision lab", pin.Label(), "the main plan keeps its own line")
	assert.Equal(t, []string{"Provision lab@fresh", "Provision lab@yesterday", "Provision lab@edge"}, labels(pin.Branches))
	assert.Zero(t, pin.BranchesMore)
	assert.Equal(t, provider.StateSuccess, pin.Branches[0].Results[0].State, "a branch line carries the branch's results")
	assert.Equal(t, pin.Branches[0].PlanKey+"-1", pin.Branches[0].Results[0].Key)
}

func TestBoardPinListsAtMostTenBranchesAndCountsTheRest(t *testing.T) {
	svc := newService(t, nil)
	ages := map[string]time.Duration{}
	for i := range 13 {
		ages[fmt.Sprintf("b%02d", i)] = time.Duration(i+1) * time.Hour
	}
	branchPlan(t, svc, ages)
	b, err := svc.Board(context.Background(), pins("lab", "PROJ-PROV"))
	require.NoError(t, err)
	pin := b.Groups[0].Pins[0]
	require.Len(t, pin.Branches, PinBranches)
	assert.Equal(t, 3, pin.BranchesMore)
	assert.Equal(t, "Provision lab@b00", pin.Branches[0].Label())
	assert.Equal(t, "Provision lab@b09", pin.Branches[9].Label())
}

func TestBoardPinWithABranchOrAPresetListsNoBranches(t *testing.T) {
	svc := newService(t, nil)
	branchPlan(t, svc, map[string]time.Duration{"fresh": time.Hour})
	b, err := svc.Board(context.Background(), pins("lab", "PROJ-PROV@fresh", "provision-lab", "PROJ-BUILD"))
	require.NoError(t, err)
	for _, p := range b.Groups[0].Pins {
		assert.Empty(t, p.Branches, p.Pin)
	}
}

func TestBoardNeedsYouRaisesAFailedOrSlowBranch(t *testing.T) {
	svc := newService(t, nil)
	now := svc.Clock.Now()
	fp := branchPlan(t, svc, map[string]time.Duration{"broken": time.Hour, "slow": time.Hour, "fine": time.Hour})
	for _, br := range fp.Branches["PROJ-PROV"] {
		switch br.ShortName {
		case "broken":
			fp.History[br.Key][0].State = provider.StateFailed
		case "slow":
			fp.History[br.Key] = []provider.Build{
				{Key: br.Key + "-2", Number: 2, State: provider.StateRunning, StartedAt: now.Add(-30 * time.Minute)},
				{Key: br.Key + "-1", Number: 1, State: provider.StateSuccess, Duration: 10 * time.Minute, FinishedAt: now.Add(-time.Hour)},
				{Key: br.Key + "-0", Number: 0, State: provider.StateSuccess, Duration: 10 * time.Minute, FinishedAt: now.Add(-2 * time.Hour)},
				{Key: br.Key + "--1", Number: -1, State: provider.StateSuccess, Duration: 10 * time.Minute, FinishedAt: now.Add(-3 * time.Hour)},
			}
		}
	}
	b, err := svc.Board(context.Background(), pins("lab", "PROJ-PROV"))
	require.NoError(t, err)
	var texts []string
	for _, n := range b.Needs {
		texts = append(texts, n.Kind+": "+n.Text)
	}
	joined := fmt.Sprint(texts)
	assert.Contains(t, joined, "pin_failed: Provision lab@broken: the last build,")
	assert.Contains(t, joined, "pin_slow: Provision lab@slow: ")
	assert.NotContains(t, joined, "@fine")
}

func TestBoardQuickRefreshReadsOnlyTheBranchesAlreadyListed(t *testing.T) {
	svc := newService(t, nil)
	fp := branchPlan(t, svc, map[string]time.Duration{"one": time.Hour, "two": 2 * time.Hour})
	b, err := svc.Board(context.Background(), pins("lab", "PROJ-PROV"))
	require.NoError(t, err)
	require.Len(t, b.Groups[0].Pins[0].Branches, 2)

	// A branch that builds after the full read waits for the next full read.
	fp.Branches["PROJ-PROV"] = append(fp.Branches["PROJ-PROV"], provider.Branch{Key: "PROJ-PROV999", ShortName: "late", PlanKey: "PROJ-PROV"})
	fp.History["PROJ-PROV999"] = []provider.Build{{Key: "PROJ-PROV999-1", Number: 1, State: provider.StateSuccess, FinishedAt: svc.Clock.Now()}}
	again, err := svc.RefreshBoard(context.Background(), pins("lab", "PROJ-PROV"), &b)
	require.NoError(t, err)
	assert.Equal(t, labels(b.Groups[0].Pins[0].Branches), labels(again.Groups[0].Pins[0].Branches))
	assert.Zero(t, fp.ListCalls("PROJ-PROV999"), "the quick refresh does not list branches again")

	full, err := svc.Board(context.Background(), pins("lab", "PROJ-PROV"))
	require.NoError(t, err)
	assert.Contains(t, labels(full.Groups[0].Pins[0].Branches), "Provision lab@late")
}
