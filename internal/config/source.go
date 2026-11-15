package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Source is the one answer to "is this configuration current". It holds the
// configuration as last loaded together with a stamp of every file that went
// into it, and Current reloads when any of those files changed, appeared or
// vanished, or when bam itself wrote one and called MarkStale.
//
//	loaded --file changed or MarkStale--> stale --next Current()--> loaded
//
// A reload that fails (a file mid-edit with broken YAML) returns the error and
// keeps the last good configuration, which Current hands back beside it, so a
// long-running screen keeps working until the file is fixed.
type Source struct {
	// Options gives the load options. It runs on every check, so a presets file
	// that appears at another path is noticed.
	Options func() LoadOptions

	mu    sync.Mutex
	cfg   *Config
	stamp string
	stale bool
	err   error
}

// NewSource loads once, as Load does, and returns the source that keeps the
// result current.
func NewSource(options func() LoadOptions) (*Source, error) {
	s := &Source{Options: options}
	if _, err := s.Current(); err != nil {
		return nil, err
	}
	return s, nil
}

// Fixed is a Source over cfg that never reloads, for code that builds a
// configuration in memory.
func Fixed(cfg *Config) *Source { return &Source{cfg: cfg} }

// Current returns the configuration, reloaded first when it is stale. On a
// failed reload it returns the last good configuration (nil if there never was
// one) and the error.
func (s *Source) Current() (*Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Options == nil {
		return s.cfg, nil
	}
	o := s.Options()
	stamp := stampOf(o)
	if s.cfg != nil && !s.stale && stamp == s.stamp {
		return s.cfg, s.err
	}
	cfg, err := Load(o)
	s.stamp, s.stale = stamp, false
	if err != nil {
		s.err = err
		return s.cfg, err
	}
	s.cfg, s.err = cfg, nil
	return cfg, nil
}

// MarkStale makes the next Current reload even when the file stamps look
// unchanged, as after bam's own write within the same clock tick.
func (s *Source) MarkStale() {
	s.mu.Lock()
	s.stale = true
	s.mu.Unlock()
}

// stampOf describes every file Load reads: size and modification time, or
// absent. The project file is discovered afresh so one created later counts.
func stampOf(o LoadOptions) string {
	var b strings.Builder
	project, _, _ := Discover(o.WorkDir, o.Home)
	paths := []string{project, o.MachineFile, o.PresetsFile}
	if o.PresetsFile != "" {
		dir := PresetsDir(o.PresetsFile)
		paths = append(paths, dir)
		if entries, err := os.ReadDir(dir); err == nil {
			for _, e := range entries {
				paths = append(paths, filepath.Join(dir, e.Name()))
			}
		}
	}
	sort.Strings(paths)
	for _, p := range paths {
		if p == "" {
			continue
		}
		if st, err := os.Stat(p); err == nil {
			fmt.Fprintf(&b, "%s|%d|%d\n", p, st.Size(), st.ModTime().UnixNano())
		} else {
			fmt.Fprintf(&b, "%s|absent\n", p)
		}
	}
	return b.String()
}
