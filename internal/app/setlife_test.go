package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func lifeStores(t *testing.T, alive func(int) bool) (mine, other *SetStore) {
	dir := t.TempDir()
	host := func() (string, error) { return "laptop", nil }
	mine = &SetStore{Dir: dir, Host: host, PID: func() int { return 4242 }, Alive: alive}
	other = &SetStore{Dir: dir, Host: host, PID: func() int { return 77 }, Alive: alive}
	return mine, other
}

func TestLockOfFourStates(t *testing.T) {
	at := time.Date(2026, 10, 6, 9, 15, 0, 0, time.UTC)
	dead := map[int]bool{}
	mine, other := lifeStores(t, func(pid int) bool { return !dead[pid] })

	st, _ := mine.LockOf("release")
	require.Equal(t, LockFree, st)

	l, err := mine.Lock([]string{"release"}, false, at)
	require.NoError(t, err)
	st, _ = mine.LockOf("release")
	require.Equal(t, LockMine, st)
	l.Release()

	_, err = other.Lock([]string{"release"}, false, at)
	require.NoError(t, err)
	st, h := mine.LockOf("release")
	require.Equal(t, LockLive, st)
	require.Equal(t, 77, h.PID)

	dead[77] = true
	st, _ = mine.LockOf("release")
	require.Equal(t, LockStale, st, "the holder's process is gone")

	// A lock written on another computer cannot be checked: it stays live.
	require.NoError(t, os.WriteFile(filepath.Join(mine.Dir, "far.lock"), []byte(`{"pid":5,"host":"desk"}`), 0o600))
	dead[5] = true
	st, _ = mine.LockOf("far")
	require.Equal(t, LockLive, st)
}

// Closing the terminal left both lock files behind; the next bam took the
// holder for a live process and refused until --force.
func TestLockTakesOverAStaleLockWithoutForce(t *testing.T) {
	at := time.Date(2026, 10, 6, 9, 15, 0, 0, time.UTC)
	alive := true
	mine, other := lifeStores(t, func(int) bool { return alive })
	_, err := other.Lock([]string{"release", "release-1"}, false, at)
	require.NoError(t, err)

	_, err = mine.Lock([]string{"release", "release-1"}, false, at)
	require.Error(t, err, "a live holder still refuses")

	alive = false
	l, err := mine.Lock([]string{"release", "release-1"}, false, at)
	require.NoError(t, err, "a gone holder is taken over")
	st, _ := mine.LockOf("release-1")
	require.Equal(t, LockMine, st)
	l.Release()
}

func TestHolderIgnoresAStaleLock(t *testing.T) {
	at := time.Date(2026, 10, 6, 9, 15, 0, 0, time.UTC)
	mine, other := lifeStores(t, func(int) bool { return false })
	_, err := other.Lock([]string{"release"}, false, at)
	require.NoError(t, err)
	_, _, _, held := mine.Holder("release")
	require.False(t, held)
}

func TestLifeAndActions(t *testing.T) {
	rec := func(s SetStatus) SetRecord { return SetRecord{Status: s} }
	cases := []struct {
		name string
		rec  SetRecord
		lock LockState
		life SetLife
		want SetActions
	}{
		{"running here", rec(SetRunning), LockMine, LifeRunningHere, SetActions{}},
		{"running elsewhere", rec(SetRunning), LockLive, LifeRunningElsewhere, SetActions{}},
		{"abandoned, lock gone", rec(SetRunning), LockFree, LifeAbandoned, SetActions{Run: true, Resume: true}},
		{"abandoned, stale lock", rec(SetRunning), LockStale, LifeAbandoned, SetActions{Run: true, Resume: true}},
		{"interrupted", rec(SetInterrupted), LockFree, LifeInterrupted, SetActions{Run: true, Resume: true}},
		{"timed out", rec(SetTimedOut), LockFree, LifeTimedOut, SetActions{Run: true, Resume: true}},
		{"failed", rec(SetFailed), LockFree, LifeFailed, SetActions{Run: true, Rerun: true}},
		{"successful", rec(SetSuccessful), LockFree, LifeSuccessful, SetActions{Run: true}},
		{"failed, another bam runs the set", rec(SetFailed), LockLive, LifeFailed, SetActions{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			life := Life(c.rec, c.lock)
			require.Equal(t, c.life, life)
			require.Equal(t, c.want, Actions(life, c.lock))
		})
	}
	require.Equal(t, SetActions{Run: true}, Actions(LifeNone, LockFree))
}
