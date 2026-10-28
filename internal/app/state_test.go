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
