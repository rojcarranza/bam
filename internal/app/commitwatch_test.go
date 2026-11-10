package app

import (
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BAM-11 P17: bam watch @ follows the build of the pushed commit.

const pushedSHA = "9f2c1d7a0b3e4f5a6b7c8d9e0f1a2b3c4d5e6f70"

func atCommit(s *Service, sha string, pushed bool) {
	s.Head = func() (string, bool, error) { return sha, pushed, nil }
}

func commitBuild(key, rev string, st provider.State) provider.Build {
	b := tb(key, "Changes by jdoe", st, 0)
	b.PlanKey = key[:len(key)-2]
	b.Revision = rev
	return b
}

// clockHook runs fn on the Nth wait, so a build can appear between two looks.
type clockHook struct {
	*fakeClock
	n  int
	at int
	fn func()
}

func (c *clockHook) After(d time.Duration) <-chan time.Time {
	c.n++
	if c.n == c.at {
		c.fn()
	}
	return c.fakeClock.After(d)
}

func TestBuildOfCommitWaitsUntilBambooStartsIt(t *testing.T) {
	p := repoBuilds()
	p.History["PROJ-PARENT1"] = []provider.Build{commitBuild("PROJ-PARENT1-2", "1111111aaaa", provider.StateSuccess)}
	s := newService(t, p)
	atCommit(s, pushedSHA, true)
	s.Clock = &clockHook{fakeClock: newClock(), at: 2, fn: func() {
		p.History["PROJ-PARENT1"] = append([]provider.Build{commitBuild("PROJ-PARENT1-3", pushedSHA, provider.StateRunning)}, p.History["PROJ-PARENT1"]...)
	}}
	var notes []string
	key, err := s.BuildOfCommit(bg, "PROJ-PARENT1", 0, func(m string) { notes = append(notes, m) })
	require.NoError(t, err)
	assert.Equal(t, "PROJ-PARENT1-3", key, "the older build of another commit is not the one")
	assert.Equal(t, []string{"waiting for a build of 9f2c1d7 on PROJ-PARENT1"}, notes)
}

func TestBuildOfCommitFindsAnAlreadyStartedBuildAtOnce(t *testing.T) {
	p := repoBuilds()
	p.History["PROJ-PARENT1"] = []provider.Build{commitBuild("PROJ-PARENT1-3", pushedSHA[:12], provider.StateRunning)}
	s := newService(t, p)
	atCommit(s, pushedSHA, true)
	key, err := s.BuildOfCommit(bg, "PROJ-PARENT1", 0, func(string) {})
	require.NoError(t, err)
	assert.Equal(t, "PROJ-PARENT1-3", key, "an abbreviated revision matches")
}

func TestBuildOfCommitTimesOutNamingTheCommit(t *testing.T) {
	p := repoBuilds()
	p.History["PROJ-PARENT1"] = []provider.Build{commitBuild("PROJ-PARENT1-2", "1111111aaaa", provider.StateSuccess)}
	s := newService(t, p)
	atCommit(s, pushedSHA, true)
	_, err := s.BuildOfCommit(bg, "PROJ-PARENT1", time.Minute, func(string) {})
	require.Error(t, err)
	assert.Equal(t, errs.KindTimeout, errs.KindOf(err))
	assert.Contains(t, err.Error(), "no build of commit 9f2c1d7 appeared on PROJ-PARENT1 within 1m0s")
}

func TestBuildOfCommitNotPushedFollowsTheNewestBuild(t *testing.T) {
	p := repoBuilds()
	p.History["PROJ-PARENT1"] = []provider.Build{commitBuild("PROJ-PARENT1-2", "1111111aaaa", provider.StateSuccess)}
	s := newService(t, p)
	atCommit(s, pushedSHA, false)
	var notes []string
	key, err := s.BuildOfCommit(bg, "PROJ-PARENT1", 0, func(m string) { notes = append(notes, m) })
	require.NoError(t, err)
	assert.Equal(t, "PROJ-PARENT1-2", key)
	require.Len(t, notes, 1)
	assert.Contains(t, notes[0], "commit 9f2c1d7 is not pushed")
}

func TestCommitPlanAtUsesTheLastRunsPlanAndTheCheckedOutBranch(t *testing.T) {
	p := repoBuilds()
	b := commitBuild("PROJ-PARENT-9", "1111111aaaa", provider.StateSuccess)
	p.History["PROJ-PARENT"] = []provider.Build{b}
	s := newService(t, p)
	checkedOut(s, "tst")
	_, err := s.CommitPlan(bg, "@", "")
	assert.Equal(t, errs.KindUsage, errs.KindOf(err), "no run in this repository yet")

	require.NoError(t, s.State.SetLast(s.Cfg.RepoRoot, LastRecord{BuildKey: "PROJ-PARENT-9", Origin: s.Origin, PlanKey: "PROJ-PARENT"}))
	plan, err := s.CommitPlan(bg, "@", "")
	require.NoError(t, err)
	assert.Equal(t, "PROJ-PARENT1", plan)

	_, err = s.CommitPlan(bg, "@", "release")
	assert.Equal(t, errs.KindUsage, errs.KindOf(err), "@ and another --branch conflict")

	plan, err = s.CommitPlan(bg, "PROJ-PARENT", "@")
	require.NoError(t, err)
	assert.Equal(t, "PROJ-PARENT1", plan)
}
