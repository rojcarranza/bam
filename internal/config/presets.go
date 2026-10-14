package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/r0jjames/bam-cli/internal/errs"
)

// PresetsEnv names the variable that overrides the presets file's path.
const PresetsEnv = "BAM_PRESETS"

// WSLPresetsGlob finds presets files in Windows homes from WSL, so bam in
// WSL and bam.exe in Git Bash share one file.
const WSLPresetsGlob = "/mnt/c/Users/*/bam/presets.yaml"

// WSLPresetsDirGlob finds presets directories in Windows homes from WSL, for
// a Windows home that keeps its presets only in the directory.
const WSLPresetsDirGlob = "/mnt/c/Users/*/bam/presets"

// PresetsLookup is what LocatePresets reads. Glob is filepath.Glob outside
// tests; nil means no matches.
type PresetsLookup struct {
	Home   string
	Getenv func(string) string
	Glob   func(pattern string) ([]string, error)
}

// PresetsLocation is the presets file bam reads and writes. The presets
// directory is always PresetsDir(Path).
type PresetsLocation struct {
	Path string
	// Exists reports whether the file or the directory beside it exists.
	Exists bool
	// Note explains why a candidate was passed over, for preset list.
	Note string
}

// DefaultPresetsPath is ~/bam/presets.yaml, the same relative path on Linux,
// macOS and Windows.
func DefaultPresetsPath(home string) string { return filepath.Join(home, "bam", "presets.yaml") }

// PresetsDir is the presets directory beside the presets file: every *.yaml
// and *.yml in it holds presets too, one file per Bamboo project by convention.
func PresetsDir(presetsFile string) string {
	return filepath.Join(filepath.Dir(presetsFile), "presets")
}

// presetsExist reports whether the presets file or its directory exists.
func presetsExist(presetsFile string) bool {
	if _, err := os.Stat(presetsFile); err == nil {
		return true
	}
	st, err := os.Stat(PresetsDir(presetsFile))
	return err == nil && st.IsDir()
}

// IsWSL reports whether bam runs inside Windows Subsystem for Linux.
func IsWSL(getenv func(string) string) bool { return getenv("WSL_DISTRO_NAME") != "" }

// LocatePresets picks the presets file: BAM_PRESETS, else ~/bam/presets.yaml
// when it or ~/bam/presets/ exists, else (in WSL only) the one presets file
// or directory under a Windows home, else ~/bam/presets.yaml to be created.
func LocatePresets(l PresetsLookup) PresetsLocation {
	if p := l.Getenv(PresetsEnv); p != "" {
		return PresetsLocation{Path: p, Exists: presetsExist(p)}
	}
	home := DefaultPresetsPath(l.Home)
	if presetsExist(home) {
		return PresetsLocation{Path: home, Exists: true}
	}
	if IsWSL(l.Getenv) && l.Glob != nil {
		files, _ := l.Glob(WSLPresetsGlob)
		dirs, _ := l.Glob(WSLPresetsDirGlob)
		var matches []string
		seen := map[string]bool{}
		for _, m := range files {
			if !seen[m] {
				seen[m], matches = true, append(matches, m)
			}
		}
		for _, d := range dirs {
			if m := filepath.Join(filepath.Dir(d), "presets.yaml"); !seen[m] {
				seen[m], matches = true, append(matches, m)
			}
		}
		switch len(matches) {
		case 0:
		case 1:
			return PresetsLocation{Path: matches[0], Exists: presetsExist(matches[0])}
		default:
			return PresetsLocation{Path: home, Note: "several Windows presets files (" + strings.Join(matches, ", ") +
				"); set " + PresetsEnv + " to the one to use"}
		}
	}
	return PresetsLocation{Path: home}
}

var presetsFileNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// PresetsDirFile is the file of the presets directory that preset add writes
// for name: --file's value, or the lowercased project key of the plan.
// A trailing .yaml or .yml is optional. Names with path separators, "..", a
// leading dot or other punctuation are usage errors.
func PresetsDirFile(presetsFile, name string) (string, error) {
	base := name
	if ext := strings.ToLower(filepath.Ext(base)); ext == ".yaml" || ext == ".yml" {
		base = strings.TrimSuffix(base, filepath.Ext(base))
	}
	if !presetsFileNameRe.MatchString(base) || strings.Contains(base, "..") {
		return "", errs.Usagef("invalid presets file name %q", name).
			WithWhy("it names a file in " + PresetsDir(presetsFile) + ": letters, digits, -, _ and ., with no path").
			WithTry("--file custom-presets")
	}
	if filepath.Ext(name) != "" && base != name {
		return filepath.Join(PresetsDir(presetsFile), name), nil
	}
	return filepath.Join(PresetsDir(presetsFile), base+".yaml"), nil
}

// ProjectPresetsName is the presets directory file name for a plan key: its
// project key, lowercased, so PROJ-DEPLOY goes to proj.yaml.
func ProjectPresetsName(planKey string) string {
	if i := strings.Index(planKey, "-"); i > 0 {
		planKey = planKey[:i]
	}
	return strings.ToLower(planKey)
}
