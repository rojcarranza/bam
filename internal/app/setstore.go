package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
)

// SetStatus is where a set run stands.
type SetStatus string

const (
	SetRunning     SetStatus = "running"
	SetSuccessful  SetStatus = "successful"
	SetFailed      SetStatus = "failed"
	SetInterrupted SetStatus = "interrupted"
	SetTimedOut    SetStatus = "timed_out"
)

// SetRecord is one run of a set as bam remembers it. It holds no variable
// values: VarNames keeps only the names given with --var, so resume can ask
// for them again.
type SetRecord struct {
	Version    int           `json:"version"`
	ID         string        `json:"id"`
	Set        string        `json:"set"`
	Origin     string        `json:"origin"`
	Repo       string        `json:"repo"`
	StartedAt  time.Time     `json:"started_at"`
	FinishedAt *time.Time    `json:"finished_at,omitempty"`
	Status     SetStatus     `json:"status"`
	VarNames   []string      `json:"var_names"`
	Stages     []StageRecord `json:"stages"`
}

// StageRecord is one stage of a recorded set run.
type StageRecord struct {
	Runs []RunRecord `json:"runs"`
}

// RunRecord is one run of a stage. BuildKey is empty until it triggered.
type RunRecord struct {
	Target   string         `json:"target"`
	PlanKey  string         `json:"plan_key"`
	BuildKey string         `json:"build_key"`
	State    provider.State `json:"state"`
	Reruns   int            `json:"reruns"` // how many times bam restarted this run's build
	Error    string         `json:"error,omitempty"`
}

const setIDTime = "20060102T150405Z"

// NewSetID names one run of a set by its start time in UTC.
func NewSetID(set string, now time.Time) string { return set + "-" + now.UTC().Format(setIDTime) }

// SetStore keeps one record per set run in Dir ($XDG_STATE_HOME/bam/sets),
// plus the lock files of the runs in progress.
type SetStore struct {
	Dir  string
	Host func() (string, error)
	PID  func() int
}

func (s *SetStore) path(id string) string { return filepath.Join(s.Dir, id+".json") }

// Save writes a record atomically.
func (s *SetStore) Save(r SetRecord) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	tmp := s.path(r.ID) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path(r.ID))
}

// Load reads one record.
func (s *SetStore) Load(id string) (SetRecord, error) {
	var r SetRecord
	data, err := os.ReadFile(s.path(id))
	if errors.Is(err, fs.ErrNotExist) {
		return r, errs.Usagef("no set run %q", id).WithTry("bam set status").Wrap(errs.ErrNotFound)
	}
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return r, errs.Configf("set record %s is corrupt", s.path(id)).Wrap(err)
	}
	return r, nil
}

// all returns every readable record, newest first. Corrupt files are skipped.
func (s *SetStore) all() ([]SetRecord, error) {
	entries, err := os.ReadDir(s.Dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []SetRecord
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok {
			continue
		}
		if r, err := s.Load(id); err == nil {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out, nil
}

// Latest is the newest record of repo, of set when set is not empty, that
// match accepts (nil accepts every record).
func (s *SetStore) Latest(repo, set string, match func(SetRecord) bool) (SetRecord, bool, error) {
	all, err := s.all()
	if err != nil {
		return SetRecord{}, false, err
	}
	for _, r := range all {
		if r.Repo == repo && (set == "" || r.Set == set) && (match == nil || match(r)) {
			return r, true, nil
		}
	}
	return SetRecord{}, false, nil
}

// IDs lists every record id, newest first.
func (s *SetStore) IDs() ([]string, error) {
	all, err := s.all()
	ids := make([]string, 0, len(all))
	for _, r := range all {
		ids = append(ids, r.ID)
	}
	return ids, err
}

// Prune deletes set's records beyond the newest keep. Other sets' records
// are left alone.
func (s *SetStore) Prune(set string, keep int) error {
	all, err := s.all()
	if err != nil {
		return err
	}
	n := 0
	for _, r := range all {
		if r.Set != set {
			continue
		}
		if n++; n > keep {
			if err := os.Remove(s.path(r.ID)); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}

// SetLock is held while a set runs, so two terminals never trigger the same
// train.
type SetLock struct{ paths []string }

type lockInfo struct {
	PID   int       `json:"pid"`
	Host  string    `json:"host"`
	Since time.Time `json:"since"`
}

// Lock creates <name>.lock for every name, or fails naming the holder. With
// force it replaces existing lock files: the way out after a crash.
func (s *SetStore) Lock(names []string, force bool, now time.Time) (*SetLock, error) {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return nil, err
	}
	host, _ := s.Host()
	data, err := json.Marshal(lockInfo{PID: s.PID(), Host: host, Since: now})
	if err != nil {
		return nil, err
	}
	l := &SetLock{}
	for _, name := range names {
		p := filepath.Join(s.Dir, name+".lock")
		if force {
			_ = os.Remove(p)
		}
		f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if errors.Is(err, fs.ErrExist) {
			l.Release()
			var held lockInfo
			if b, rerr := os.ReadFile(p); rerr == nil {
				_ = json.Unmarshal(b, &held)
			}
			return nil, errs.Usagef("set run in progress (pid %d on %s, since %s)", held.PID, held.Host, held.Since.Local().Format("15:04")).
				WithWhy(p + " exists").WithTry("pass --force if that process is gone")
		}
		if err != nil {
			l.Release()
			return nil, err
		}
		_, werr := f.Write(data)
		cerr := f.Close()
		l.paths = append(l.paths, p)
		if werr != nil || cerr != nil {
			l.Release()
			return nil, fmt.Errorf("write %s: %w", p, errors.Join(werr, cerr))
		}
	}
	return l, nil
}

// Release removes the lock files. It is safe to call more than once. After a
// --force takeover it also removes the new holder's files; a takeover means
// this holder was gone, so that cannot happen while both run.
func (l *SetLock) Release() {
	if l == nil {
		return
	}
	for _, p := range l.paths {
		_ = os.Remove(p)
	}
	l.paths = nil
}
