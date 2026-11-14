// Package usage keeps bam's private tally of the commands its owner runs. The
// tally is one small file on the local computer; nothing reads it but
// "bam usage" and nothing sends it anywhere. It holds command names and counts
// by day, and nothing else: callers pass a command's name, never its arguments.
package usage

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"sort"
	"time"

	"github.com/rojcarranza/bam/internal/atomicfile"
)

const dayFormat = "2006-01-02"

// Tally is the file's content: how often each command ran on each day.
type Tally struct {
	// Off is true once counting was turned off; Record then writes nothing.
	Off  bool                      `json:"off,omitempty"`
	Days map[string]map[string]int `json:"days"`
}

// Store is the tally file.
type Store struct {
	Path string
}

// Load reads the tally. A missing file is an empty tally.
func (s *Store) Load() (Tally, error) {
	var t Tally
	data, err := os.ReadFile(s.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return t, nil
	}
	if err != nil {
		return t, err
	}
	if err := json.Unmarshal(data, &t); err != nil {
		return Tally{}, err
	}
	return t, nil
}

// Record adds one to command's count for the local day of now. It does nothing
// while counting is off. A corrupt file is replaced by a fresh tally, because
// a lost count costs less than a command that fails over its own bookkeeping.
func (s *Store) Record(command string, now time.Time) error {
	t, err := s.Load()
	if err != nil {
		t = Tally{}
	}
	if t.Off {
		return nil
	}
	if t.Days == nil {
		t.Days = map[string]map[string]int{}
	}
	day := now.Format(dayFormat)
	if t.Days[day] == nil {
		t.Days[day] = map[string]int{}
	}
	t.Days[day][command]++
	return s.save(t)
}

// SetOff turns counting off or back on and keeps the counts so far.
func (s *Store) SetOff(off bool) error {
	t, err := s.Load()
	if err != nil {
		t = Tally{}
	}
	t.Off = off
	return s.save(t)
}

func (s *Store) save(t Tally) error {
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(s.Path, append(data, '\n'), 0o600)
}

// Total is how often command ran over the whole tally.
type Total struct {
	Command string
	Count   int
}

// Totals sums each command over all days, most used first, ties by name.
func (t Tally) Totals() []Total {
	sum := map[string]int{}
	for _, cmds := range t.Days {
		for c, n := range cmds {
			sum[c] += n
		}
	}
	out := make([]Total, 0, len(sum))
	for c, n := range sum {
		out = append(out, Total{c, n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Command < out[j].Command
	})
	return out
}

// DayTotal is how many commands ran on one day.
type DayTotal struct {
	Day   string
	Count int
}

// DayTotals sums each day, oldest first.
func (t Tally) DayTotals() []DayTotal {
	out := make([]DayTotal, 0, len(t.Days))
	for d, cmds := range t.Days {
		n := 0
		for _, c := range cmds {
			n += c
		}
		out = append(out, DayTotal{d, n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Day < out[j].Day })
	return out
}
