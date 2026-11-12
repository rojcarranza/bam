package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/rojcarranza/bam/internal/atomicfile"
)

// LastRecord is the last build bam triggered from one repository. It holds no
// variable values.
type LastRecord struct {
	BuildKey    string    `json:"build_key"`
	Origin      string    `json:"origin"`
	PlanKey     string    `json:"plan_key"`
	Target      string    `json:"target,omitempty"`
	TriggeredAt time.Time `json:"triggered_at"`
}

// JournalRun is one build bam started, kept in the journal that bam status
// reads. It holds no variable values.
type JournalRun struct {
	BuildKey  string    `json:"build_key"`
	Origin    string    `json:"origin"`
	Server    string    `json:"server,omitempty"` // the alias it was started on
	PlanKey   string    `json:"plan_key"`
	Branch    string    `json:"branch,omitempty"` // plan branch short name; empty for the default branch
	Target    string    `json:"target,omitempty"` // the preset
	RepoRoot  string    `json:"repo_root,omitempty"`
	StartedAt time.Time `json:"started_at"`
}

// JournalMax is how many runs the journal keeps, newest first.
const JournalMax = 50

type stateFile struct {
	Version int                   `json:"version"`
	Repos   map[string]LastRecord `json:"repos"`
	// Runs is the journal; absent from state files written before it.
	Runs []JournalRun `json:"runs,omitempty"`
}

// StateStore reads and writes $XDG_STATE_HOME/bam/state.json.
type StateStore struct {
	Path string
}

// update loads the state file, applies change and saves it, all under a lock
// file so that two bam processes writing at once lose neither change.
func (s *StateStore) update(change func(*stateFile)) error {
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
	f, err := s.load()
	if err != nil {
		return err
	}
	change(&f)
	return s.save(f)
}

const (
	lockWait  = 5 * time.Second
	lockStale = 15 * time.Second
)

// lock takes an exclusive lock file beside the state file. A lock older
// than lockStale was left by a process that died, and is taken over.
func (s *StateStore) lock() (func(), error) {
	path := s.Path + ".lock"
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(lockWait)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_ = f.Close()
			return func() { _ = os.Remove(path) }, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
		if st, serr := os.Stat(path); serr == nil && time.Since(st.ModTime()) > lockStale && takeOverStale(path) {
			continue
		}
		if time.Now().After(deadline) {
			return nil, errors.New("the bam state file is locked by another bam process")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// takeOverStale moves a lock that looked stale aside, atomically, so that of
// two processes taking it over only one succeeds. It looks again at what it
// moved: a lock that was in fact fresh, because another process replaced the
// stale one first, is put back.
func takeOverStale(path string) bool {
	aside := fmt.Sprintf("%s.stale.%d.%d", path, os.Getpid(), time.Now().UnixNano())
	if os.Rename(path, aside) != nil {
		return false // another process moved it first
	}
	defer func() { _ = os.Remove(aside) }()
	if st, err := os.Stat(aside); err == nil && time.Since(st.ModTime()) <= lockStale {
		if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
			_ = os.Link(aside, path)
		}
		return false
	}
	return true
}

func (s *StateStore) load() (stateFile, error) {
	f := stateFile{Version: 1, Repos: map[string]LastRecord{}}
	data, err := os.ReadFile(s.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return f, err
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return stateFile{Version: 1, Repos: map[string]LastRecord{}}, nil // a corrupt state file is rebuilt
	}
	if f.Repos == nil {
		f.Repos = map[string]LastRecord{}
	}
	return f, nil
}

func (s *StateStore) save(f stateFile) error {
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(s.Path, data, 0o600)
}

// Last returns the record for a repository root.
func (s *StateStore) Last(root string) (LastRecord, bool, error) {
	f, err := s.load()
	if err != nil {
		return LastRecord{}, false, err
	}
	r, ok := f.Repos[root]
	return r, ok, nil
}

// SetLast replaces the record for a repository root.
func (s *StateStore) SetLast(root string, r LastRecord) error {
	return s.update(func(f *stateFile) { f.Repos[root] = r })
}

// Record replaces the last build of a repository and adds the run to the
// journal, newest first, keeping JournalMax runs.
func (s *StateStore) Record(root string, last LastRecord, run JournalRun) error {
	return s.update(func(f *stateFile) {
		f.Repos[root] = last
		f.Runs = append([]JournalRun{run}, f.Runs...)
		if len(f.Runs) > JournalMax {
			f.Runs = f.Runs[:JournalMax]
		}
	})
}

// Runs returns the journal, newest first.
func (s *StateStore) Runs() ([]JournalRun, error) {
	f, err := s.load()
	return f.Runs, err
}

// Forget removes the record for a repository root.
func (s *StateStore) Forget(root string) error {
	return s.update(func(f *stateFile) { delete(f.Repos, root) })
}
