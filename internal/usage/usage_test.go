package usage

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRecordCountsByDayAndCommand(t *testing.T) {
	s := &Store{Path: filepath.Join(t.TempDir(), "sub", "usage.json")}
	d1 := time.Date(2026, 11, 14, 23, 59, 0, 0, time.UTC)
	d2 := d1.Add(2 * time.Minute)
	require.NoError(t, s.Record("bam watch", d1))
	require.NoError(t, s.Record("bam watch", d1))
	require.NoError(t, s.Record("bam status", d1))
	require.NoError(t, s.Record("bam watch", d2))

	tl, err := s.Load()
	require.NoError(t, err)
	require.Equal(t, map[string]map[string]int{
		"2026-11-14": {"bam watch": 2, "bam status": 1},
		"2026-11-15": {"bam watch": 1},
	}, tl.Days)
	require.Equal(t, []Total{{"bam watch", 3}, {"bam status", 1}}, tl.Totals())
	require.Equal(t, []DayTotal{{"2026-11-14", 3}, {"2026-11-15", 1}}, tl.DayTotals())
}

func TestOffStopsCountingAndKeepsCounts(t *testing.T) {
	s := &Store{Path: filepath.Join(t.TempDir(), "usage.json")}
	now := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	require.NoError(t, s.Record("bam watch", now))
	require.NoError(t, s.SetOff(true))
	require.NoError(t, s.Record("bam watch", now))
	tl, err := s.Load()
	require.NoError(t, err)
	require.True(t, tl.Off)
	require.Equal(t, 1, tl.Totals()[0].Count)

	require.NoError(t, s.SetOff(false))
	require.NoError(t, s.Record("bam watch", now))
	tl, _ = s.Load()
	require.Equal(t, 2, tl.Totals()[0].Count)
}

func TestCorruptFileIsReplaced(t *testing.T) {
	p := filepath.Join(t.TempDir(), "usage.json")
	require.NoError(t, os.WriteFile(p, []byte("{not json"), 0o600))
	s := &Store{Path: p}
	_, err := s.Load()
	require.Error(t, err)
	require.NoError(t, s.Record("bam status", time.Now()))
	tl, err := s.Load()
	require.NoError(t, err)
	require.Equal(t, 1, tl.Totals()[0].Count)
}

func TestFileIsPrivate(t *testing.T) {
	p := filepath.Join(t.TempDir(), "usage.json")
	require.NoError(t, (&Store{Path: p}).Record("bam status", time.Now()))
	fi, err := os.Stat(p)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
}
