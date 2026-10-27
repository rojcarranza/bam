package app

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/r0jjames/bam-cli/internal/provider"
)

// treeTTL is how long a saved tree is drawn before it is dropped (#82).
const treeTTL = 7 * 24 * time.Hour

// TreeStore keeps finished build trees on disk, one file per server origin,
// so the terminal UI draws a tree it has seen before at once, even after a
// restart. It is a cache: a missing, corrupt or expired file is rebuilt,
// never an error.
type TreeStore struct {
	Dir string

	mu    sync.Mutex
	files map[string]map[string]savedTree // origin → root key → tree
}

type savedTree struct {
	Saved time.Time `json:"saved"`
	Tree  Tree      `json:"tree"`
}

type treeFile struct {
	Version int                  `json:"version"`
	Trees   map[string]savedTree `json:"trees"`
}

// TreeFinal reports whether t can no longer change: its root is finished and
// nothing in it runs. Bamboo queues children when the parent ends.
func TreeFinal(t Tree) bool {
	return t.Root != nil && TreeFinished(t.Root.Build.State) && t.State != provider.StateRunning
}

func (s *TreeStore) path(origin string) string {
	return filepath.Join(s.Dir, url.QueryEscape(origin)+".json")
}

// trees returns origin's saved trees, read from disk on first use, without
// the expired ones. The caller holds s.mu.
func (s *TreeStore) trees(origin string, now time.Time) map[string]savedTree {
	if t, ok := s.files[origin]; ok {
		return t
	}
	var f treeFile
	if data, err := os.ReadFile(s.path(origin)); err == nil && json.Unmarshal(data, &f) == nil && f.Version == 1 {
		for k, t := range f.Trees {
			if now.Sub(t.Saved) > treeTTL {
				delete(f.Trees, k)
			}
		}
	}
	if f.Trees == nil {
		f.Trees = map[string]savedTree{}
	}
	if s.files == nil {
		s.files = map[string]map[string]savedTree{}
	}
	s.files[origin] = f.Trees
	return f.Trees
}

// Get returns the saved tree for root key on origin.
func (s *TreeStore) Get(origin, key string, now time.Time) (Tree, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.trees(origin, now)[key]
	if !ok || now.Sub(t.Saved) > treeTTL {
		return Tree{}, false
	}
	return t.Tree, true
}

// Keep saves t for key when it is final, and otherwise forgets key: a rerun
// makes a saved tree stale. A write error is dropped; the cache is only lost.
func (s *TreeStore) Keep(origin, key string, t Tree, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	trees := s.trees(origin, now)
	_, had := trees[key]
	if !TreeFinal(t) {
		if !had {
			return
		}
		delete(trees, key)
	} else {
		trees[key] = savedTree{Saved: now, Tree: t}
	}
	data, err := json.Marshal(treeFile{Version: 1, Trees: trees})
	if err != nil {
		return
	}
	p := s.path(origin)
	if os.MkdirAll(filepath.Dir(p), 0o700) != nil {
		return
	}
	if os.WriteFile(p+".tmp", data, 0o600) == nil {
		_ = os.Rename(p+".tmp", p)
	}
}

// CachedTree returns the tree saved for root key on this server, with no
// request. It may be out of date; read it again to be sure.
func (s *Service) CachedTree(key string) (Tree, bool) {
	if s.Trees == nil {
		return Tree{}, false
	}
	return s.Trees.Get(s.Origin, key, s.Clock.Now())
}

// KeepTree saves a final tree read for key, or forgets a saved one that runs
// again.
func (s *Service) KeepTree(key string, t Tree) {
	if s.Trees != nil {
		s.Trees.Keep(s.Origin, key, t, s.Clock.Now())
	}
}
