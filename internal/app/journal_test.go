package app

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Intent (BAM-11 P16): bam keeps the last 50 runs it started in its state
// file, never a variable value, and an older state file with one record
// still reads.
func TestJournalKeepsTheLast50RunsNewestFirst(t *testing.T) {
	s := &StateStore{Path: filepath.Join(t.TempDir(), "state.json")}
	base := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	for i := range JournalMax + 5 {
		run := JournalRun{BuildKey: fmt.Sprintf("PROJ-BUILD-%d", i), Origin: "https://bamboo.example.com", StartedAt: base.Add(time.Duration(i) * time.Minute)}
		require.NoError(t, s.Record("/r", LastRecord{BuildKey: run.BuildKey}, run))
	}
	runs, err := s.Runs()
	require.NoError(t, err)
	require.Len(t, runs, JournalMax)
	assert.Equal(t, "PROJ-BUILD-54", runs[0].BuildKey)
	assert.Equal(t, "PROJ-BUILD-5", runs[JournalMax-1].BuildKey)
	last, ok, _ := s.Last("/r")
	assert.True(t, ok)
	assert.Equal(t, "PROJ-BUILD-54", last.BuildKey)
}

func TestStateFileWithOneRecordStillReads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"version":1,"repos":{"/r":{"build_key":"PROJ-BUILD-45","origin":"https://bamboo.example.com","plan_key":"PROJ-BUILD","triggered_at":"2026-09-12T12:00:00Z"}}}`), 0o600))
	s := &StateStore{Path: path}
	rec, ok, err := s.Last("/r")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "PROJ-BUILD-45", rec.BuildKey)
	runs, err := s.Runs()
	require.NoError(t, err)
	assert.Empty(t, runs)
	require.NoError(t, s.Record("/r", rec, JournalRun{BuildKey: "PROJ-BUILD-46"}))
	runs, _ = s.Runs()
	assert.Len(t, runs, 1)
}

func TestTwoStoresWritingAtOnceLoseNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	var wg sync.WaitGroup
	for p := range 2 { // two bam processes, each with its own store
		s := &StateStore{Path: path}
		for i := range 20 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				key := fmt.Sprintf("PROJ-BUILD-%d%02d", p, i)
				require.NoError(t, s.Record(fmt.Sprintf("/r%d", p), LastRecord{BuildKey: key}, JournalRun{BuildKey: key}))
			}()
		}
	}
	wg.Wait()
	runs, err := (&StateStore{Path: path}).Runs()
	require.NoError(t, err)
	assert.Len(t, runs, 40)
}

func TestAStaleLockIsTakenOver(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	lock := path + ".lock"
	require.NoError(t, os.WriteFile(lock, nil, 0o600))
	old := time.Now().Add(-time.Minute)
	require.NoError(t, os.Chtimes(lock, old, old))
	require.NoError(t, (&StateStore{Path: path}).Record("/r", LastRecord{}, JournalRun{BuildKey: "PROJ-BUILD-1"}))
}

func TestMarkHerePrefersTheCheckedOutBranch(t *testing.T) {
	at := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	es := []StatusEntry{
		{Run: JournalRun{BuildKey: "A-1", RepoRoot: "/r", Branch: "", StartedAt: at.Add(3 * time.Minute)}},
		{Run: JournalRun{BuildKey: "B-1", RepoRoot: "/r", Branch: "feature", StartedAt: at}},
		{Run: JournalRun{BuildKey: "C-1", RepoRoot: "/other", Branch: "feature", StartedAt: at.Add(5 * time.Minute)}},
	}
	MarkHere(es, "/r", "feature")
	assert.Equal(t, []bool{false, true, false}, []bool{es[0].Here, es[1].Here, es[2].Here})
	for i := range es {
		es[i].Here = false
	}
	MarkHere(es, "/r", "develop") // no run on it: the default branch's build
	assert.Equal(t, []bool{true, false, false}, []bool{es[0].Here, es[1].Here, es[2].Here})
}
