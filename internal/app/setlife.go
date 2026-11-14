package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// LockState is what a set's lock file says about the process that holds it.
type LockState int

const (
	// LockFree: no lock file.
	LockFree LockState = iota
	// LockMine: this process holds it.
	LockMine
	// LockLive: another process holds it, or one on another computer, whose
	// liveness bam cannot check.
	LockLive
	// LockStale: a process on this computer wrote it and is gone.
	LockStale
)

// LockHolder is who a lock file names.
type LockHolder struct {
	PID   int
	Host  string
	Since time.Time
}

// LockOf reads a lock without taking it. An unreadable or corrupt file counts
// as free, as bam cannot tell whose it is.
func (s *SetStore) LockOf(name string) (LockState, LockHolder) {
	return s.lockAt(filepath.Join(s.Dir, name+".lock"))
}

func (s *SetStore) lockAt(p string) (LockState, LockHolder) {
	data, err := os.ReadFile(p)
	if err != nil {
		return LockFree, LockHolder{}
	}
	var info lockInfo
	if json.Unmarshal(data, &info) != nil {
		return LockFree, LockHolder{}
	}
	h := LockHolder(info)
	me, _ := s.Host()
	switch {
	case info.Host != me:
		return LockLive, h
	case info.PID == s.PID():
		return LockMine, h
	case s.Alive != nil && !s.Alive(info.PID):
		return LockStale, h
	}
	return LockLive, h
}

// dropStale removes the lock file p when its process on this computer is
// gone. It renames the file aside before judging it, so of two bams taking
// over one stale lock only one moves it; a live lock moved aside by the
// other is put back.
func (s *SetStore) dropStale(p string) {
	if st, _ := s.lockAt(p); st != LockStale {
		return
	}
	aside := fmt.Sprintf("%s.%d.stale", p, s.PID())
	if os.Rename(p, aside) != nil {
		return
	}
	if st, _ := s.lockAt(aside); st != LockStale {
		_ = os.Link(aside, p)
	}
	_ = os.Remove(aside)
}

// SetLife is where a set run stands, from its record and its lock together.
type SetLife string

const (
	LifeNone             SetLife = "never_run"         // the set has no run yet
	LifeRunningHere      SetLife = "running_here"      // recorded running, this process holds the lock
	LifeRunningElsewhere SetLife = "running_elsewhere" // recorded running, another bam holds the lock
	LifeAbandoned        SetLife = "abandoned"         // recorded running, nobody holds the lock: its terminal closed or it crashed
	LifeInterrupted      SetLife = "interrupted"
	LifeTimedOut         SetLife = "timed_out"
	LifeFailed           SetLife = "failed"
	LifeSuccessful       SetLife = "successful"
)

// Life is the one answer to "is this set run alive": rec is the set's newest
// run and lock the state of the set's lock.
func Life(rec SetRecord, lock LockState) SetLife {
	switch rec.Status {
	case SetRunning:
		switch lock {
		case LockMine:
			return LifeRunningHere
		case LockLive:
			return LifeRunningElsewhere
		}
		return LifeAbandoned
	case SetInterrupted:
		return LifeInterrupted
	case SetTimedOut:
		return LifeTimedOut
	case SetFailed:
		return LifeFailed
	}
	return LifeSuccessful
}

// SetActions is what a person may start on a set.
type SetActions struct {
	Run    bool // start a new run
	Resume bool // continue the run
	Rerun  bool // restart the run's failed builds
}

// Actions is the one table of what the set offers: nothing while any bam
// holds its lock, else resume for a run that stopped short (an abandoned one
// included) and rerun for a failed one. lock is the set's lock state.
func Actions(life SetLife, lock LockState) SetActions {
	if lock == LockMine || lock == LockLive {
		return SetActions{}
	}
	a := SetActions{Run: true}
	switch life {
	case LifeAbandoned, LifeInterrupted, LifeTimedOut:
		a.Resume = true
	case LifeFailed:
		a.Rerun = true
	}
	return a
}

// Resumable is whether the run stopped short: its status says resume may
// apply, the lock then decides (Actions). A live lock still refuses it.
func (r SetRecord) Resumable() bool {
	return r.Status == SetRunning || r.Status == SetInterrupted || r.Status == SetTimedOut
}
