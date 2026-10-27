package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/r0jjames/bam-cli/internal/provider"
)

func TestTreeStoreKeepsFinalTreesAcrossRestarts(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	origin := "https://bamboo.example.com"
	done := Tree{Root: &TreeNode{PlanKey: "PROJ-BUILD", Build: provider.Build{Key: "PROJ-BUILD-44", State: provider.StateFailed}}, State: provider.StateFailed}

	(&TreeStore{Dir: dir}).Keep(origin, "PROJ-BUILD-44", done, now)
	got, ok := (&TreeStore{Dir: dir}).Get(origin, "PROJ-BUILD-44", now.Add(time.Hour))
	require.True(t, ok)
	require.Equal(t, "PROJ-BUILD-44", got.Root.Build.Key)

	_, ok = (&TreeStore{Dir: dir}).Get("https://bamboo.lab.example", "PROJ-BUILD-44", now)
	require.False(t, ok, "trees are kept per server origin")
	_, ok = (&TreeStore{Dir: dir}).Get(origin, "PROJ-BUILD-44", now.Add(8*24*time.Hour))
	require.False(t, ok, "a tree older than 7 days is dropped")

	running := done
	running.State = provider.StateRunning
	s := &TreeStore{Dir: dir}
	s.Keep(origin, "PROJ-BUILD-44", running, now)
	_, ok = (&TreeStore{Dir: dir}).Get(origin, "PROJ-BUILD-44", now)
	require.False(t, ok, "a tree that runs again is forgotten on disk too")
}

func TestTreeStoreRebuildsACorruptFile(t *testing.T) {
	dir := t.TempDir()
	s := &TreeStore{Dir: dir}
	require.NoError(t, os.WriteFile(s.path("https://bamboo.example.com"), []byte("{not json"), 0o600))
	_, ok := s.Get("https://bamboo.example.com", "PROJ-BUILD-44", time.Now())
	require.False(t, ok)

	done := Tree{Root: &TreeNode{Build: provider.Build{State: provider.StateSuccess}}, State: provider.StateSuccess}
	s.Keep("https://bamboo.example.com", "PROJ-BUILD-44", done, time.Now())
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	require.Len(t, files, 1)
	_, ok = (&TreeStore{Dir: dir}).Get("https://bamboo.example.com", "PROJ-BUILD-44", time.Now())
	require.True(t, ok)
}
