package app

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStateStoreRoundTrip(t *testing.T) {
	s := &StateStore{Path: filepath.Join(t.TempDir(), "bam", "state.json")}
	_, ok, err := s.Last("/r")
	require.NoError(t, err)
	assert.False(t, ok)

	rec := LastRecord{BuildKey: "PROJ-BUILD-45", Origin: "https://bamboo.example.com", PlanKey: "PROJ-BUILD", Target: "build",
		TriggeredAt: time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)}
	require.NoError(t, s.SetLast("/r", rec))
	require.NoError(t, s.SetLast("/other", LastRecord{BuildKey: "OPS-X-1"}))

	got, ok, err := s.Last("/r")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, rec, got)

	require.NoError(t, s.Forget("/r"))
	_, ok, _ = s.Last("/r")
	assert.False(t, ok)
	_, ok, _ = s.Last("/other")
	assert.True(t, ok)
}

func TestClosest(t *testing.T) {
	names := []string{"develop", "feat/foo", "feat/bar", "release"}
	assert.Equal(t, []string{"develop"}, Closest("devlop", names, 3))
	assert.Equal(t, []string{"feat/foo", "feat/bar"}, Closest("feat", names, 3))
	assert.Empty(t, Closest("zzzzzz", names, 3))
	assert.Equal(t, []string{"develop", "devops"}, Closest("devlop", []string{"devops", "develop"}, 2), "nearest first")
	assert.Equal(t, []string{"plan"}, Closest("pln", []string{"open", "plan"}, 2), "one edit at most for short words")
	assert.Empty(t, Closest("del", []string{"set"}, 1), "two edits rewrite a short word")
	assert.Equal(t, []string{"run"}, Closest("rnu", []string{"run"}, 1), "a swap is one edit")
	assert.Equal(t, []string{"logs"}, Closest("log", []string{"login", "logs"}, 1), "shortest substring match first")
	assert.Empty(t, Closest("go", []string{"logout"}, 1), "two letters match only as a prefix")
	assert.Equal(t, []string{"logout"}, Closest("lo", []string{"logout"}, 1))
}
