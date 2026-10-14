package config

import (
	"os"
	"path/filepath"
	"strings"
)

// PresetsEnv names the variable that overrides the presets file's path.
const PresetsEnv = "BAM_PRESETS"

// WSLPresetsGlob finds presets files in Windows homes from WSL, so bam in
// WSL and bam.exe in Git Bash share one file.
const WSLPresetsGlob = "/mnt/c/Users/*/bam/presets.yaml"

// PresetsLookup is what LocatePresets reads. Glob is filepath.Glob outside
// tests; nil means no matches.
type PresetsLookup struct {
	Home   string
	Getenv func(string) string
	Glob   func(pattern string) ([]string, error)
}

// PresetsLocation is the presets file bam reads and writes.
type PresetsLocation struct {
	Path   string
	Exists bool
	// Note explains why a candidate was passed over, for preset list.
	Note string
}

// DefaultPresetsPath is ~/bam/presets.yaml, the same relative path on Linux,
// macOS and Windows.
func DefaultPresetsPath(home string) string { return filepath.Join(home, "bam", "presets.yaml") }

// IsWSL reports whether bam runs inside Windows Subsystem for Linux.
func IsWSL(getenv func(string) string) bool { return getenv("WSL_DISTRO_NAME") != "" }

// LocatePresets picks the presets file: BAM_PRESETS, else ~/bam/presets.yaml
// when it exists, else (in WSL only) the one presets file under a Windows
// home, else ~/bam/presets.yaml to be created.
func LocatePresets(l PresetsLookup) PresetsLocation {
	exists := func(p string) bool { _, err := os.Stat(p); return err == nil }
	if p := l.Getenv(PresetsEnv); p != "" {
		return PresetsLocation{Path: p, Exists: exists(p)}
	}
	home := DefaultPresetsPath(l.Home)
	if exists(home) {
		return PresetsLocation{Path: home, Exists: true}
	}
	if IsWSL(l.Getenv) && l.Glob != nil {
		matches, _ := l.Glob(WSLPresetsGlob)
		switch len(matches) {
		case 0:
		case 1:
			return PresetsLocation{Path: matches[0], Exists: exists(matches[0])}
		default:
			return PresetsLocation{Path: home, Note: "several Windows presets files (" + strings.Join(matches, ", ") +
				"); set " + PresetsEnv + " to the one to use"}
		}
	}
	return PresetsLocation{Path: home}
}
