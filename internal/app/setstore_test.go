package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/stretchr/testify/require"
)

func newSetStore(t *testing.T) *SetStore {
	return &SetStore{Dir: filepath.Join(t.TempDir(), "sets"),
		Host: func() (string, error) { return "laptop", nil }, PID: func() int { return 4242 }}
}

func TestSetRecordRoundTripAndMode(t *testing.T) {
	s := newSetStore(t)
	now := time.Date(2026, 9, 29, 10, 15, 0, 0, time.UTC)
	id := NewSetID("release", now)
	require.Equal(t, "release-20260929T101500Z", id)
	rec := SetRecord{Version: 1, ID: id, Set: "release", Origin: "https://bamboo.example.com", Repo: "/src/app",
		StartedAt: now, Status: SetRunning, VarNames: []string{"env"},
		Stages: []StageRecord{{Runs: []RunRecord{{Target: "publish-specs", PlanKey: "PROJ-SPEC"}}}}}
	require.NoError(t, s.Save(rec))
	fi, err := os.Stat(filepath.Join(s.Dir, id+".json"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
	di, err := os.Stat(s.Dir)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), di.Mode().Perm())
	got, err := s.Load(id)
	require.NoError(t, err)
	require.Equal(t, rec, got)
}

func TestLoadMissingRecordIsUsage(t *testing.T) {
	_, err := newSetStore(t).Load("release-20260101T000000Z")
	require.Equal(t, errs.KindUsage, errs.KindOf(err))
	require.ErrorIs(t, err, errs.ErrNotFound)
}

func TestLatestPicksNewestForRepoAndSet(t *testing.T) {
	s := newSetStore(t)
	at := func(m int) time.Time { return time.Date(2026, 9, 29, 10, m, 0, 0, time.UTC) }
	for _, r := range []SetRecord{
		{ID: NewSetID("release", at(1)), Set: "release", Repo: "/a", StartedAt: at(1), Status: SetFailed},
		{ID: NewSetID("release", at(2)), Set: "release", Repo: "/a", StartedAt: at(2), Status: SetInterrupted},
		{ID: NewSetID("release", at(3)), Set: "release", Repo: "/b", StartedAt: at(3), Status: SetInterrupted},
		{ID: NewSetID("nightly", at(4)), Set: "nightly", Repo: "/a", StartedAt: at(4), Status: SetSuccessful},
	} {
		r.Version = 1
		require.NoError(t, s.Save(r))
	}
	r, ok, err := s.Latest("/a", "", nil)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "nightly", r.Set)
	r, _, _ = s.Latest("/a", "release", nil)
	require.Equal(t, at(2), r.StartedAt)
	r, _, _ = s.Latest("/a", "", func(r SetRecord) bool { return r.Status == SetFailed })
	require.Equal(t, at(1), r.StartedAt)
	_, ok, err = s.Latest("/c", "", nil)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestPruneKeepsNewestOfOneSet(t *testing.T) {
	s := newSetStore(t)
	for m := 0; m < 25; m++ {
		at := time.Date(2026, 9, 29, 10, m, 0, 0, time.UTC)
		require.NoError(t, s.Save(SetRecord{Version: 1, ID: NewSetID("release", at), Set: "release", StartedAt: at}))
	}
	other := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, s.Save(SetRecord{Version: 1, ID: NewSetID("nightly", other), Set: "nightly", StartedAt: other}))
	require.NoError(t, s.Prune("release", 20))
	ids, err := s.IDs()
	require.NoError(t, err)
	require.Len(t, ids, 21)
	require.Equal(t, NewSetID("release", time.Date(2026, 9, 29, 10, 24, 0, 0, time.UTC)), ids[0])
	require.Contains(t, ids, NewSetID("nightly", other))
	require.NotContains(t, ids, NewSetID("release", time.Date(2026, 9, 29, 10, 4, 0, 0, time.UTC)))
}

func TestLockRefusesASecondHolderAndForceTakesOver(t *testing.T) {
	s := newSetStore(t)
	now := time.Date(2026, 9, 29, 10, 15, 0, 0, time.UTC)
	l, err := s.Lock([]string{"release", "release-20260929T101500Z"}, false, now)
	require.NoError(t, err)
	_, err = s.Lock([]string{"release"}, false, now)
	require.Equal(t, errs.KindUsage, errs.KindOf(err))
	require.Contains(t, err.Error(), "pid 4242 on laptop")
	// A crashed holder never releases: --force replaces its files.
	l2, err := s.Lock([]string{"release"}, true, now)
	require.NoError(t, err)
	l2.Release()
	l.Release() // releasing twice, or after a takeover, is harmless
	l.Release()
	entries, _ := os.ReadDir(s.Dir)
	for _, e := range entries {
		require.NotContains(t, e.Name(), ".lock")
	}
}

func TestAFailedLockLeavesNoPartialLock(t *testing.T) {
	s := newSetStore(t)
	now := time.Date(2026, 9, 29, 10, 15, 0, 0, time.UTC)
	held, err := s.Lock([]string{"b"}, false, now)
	require.NoError(t, err)
	defer held.Release()
	_, err = s.Lock([]string{"a", "b"}, false, now)
	require.Error(t, err)
	_, err = os.Stat(filepath.Join(s.Dir, "a.lock"))
	require.True(t, os.IsNotExist(err), "a is released when b is held")
}
