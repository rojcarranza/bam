package config

import (
	"os"
	"path/filepath"
)

// Dirs are bam's directories outside any repository.
type Dirs struct {
	Config string // config.yaml, credentials.yaml, fixture-denylist.txt
	Cache  string // capabilities.json, saved build trees
	State  string // state.json, set run records
	// OldConfig is the config directory older releases used on macOS and
	// Windows, which bam moves config files out of (MoveDir). It is empty
	// on other systems and when XDG_CONFIG_HOME is an absolute path.
	OldConfig string
}

// LocateDirs returns bam's directories in the XDG layout on every OS:
// $XDG_CONFIG_HOME/bam (default ~/.config/bam), $XDG_CACHE_HOME/bam
// (~/.cache/bam) and $XDG_STATE_HOME/bam (~/.local/state/bam). Relative XDG
// values are ignored, as the XDG spec requires.
//
// Older bam releases used the OS-native directories on macOS
// (~/Library/Application Support, ~/Library/Caches) and Windows
// (%LOCALAPPDATA%). Config files are moved out of the old config directory,
// OldConfig. For cache and state, while the new directory does not exist and
// the old one does, the old one is kept, so an upgrade loses no record.
func LocateDirs(home, goos string, getenv func(string) string) Dirs {
	base := func(name, def string) string {
		if v := getenv(name); filepath.IsAbs(v) {
			return v
		}
		return filepath.Join(home, def)
	}
	d := Dirs{
		Config: filepath.Join(base("XDG_CONFIG_HOME", ".config"), "bam"),
		Cache:  filepath.Join(base("XDG_CACHE_HOME", ".cache"), "bam"),
		State:  filepath.Join(base("XDG_STATE_HOME", filepath.Join(".local", "state")), "bam"),
	}
	var config, cache, state string
	switch goos {
	case "darwin":
		config = filepath.Join(home, "Library", "Application Support")
		cache = filepath.Join(home, "Library", "Caches")
		state = config
	case "windows":
		local := getenv("LOCALAPPDATA")
		if local == "" {
			local = filepath.Join(home, "AppData", "Local")
		}
		config, cache, state = local, filepath.Join(local, "cache"), local
	default:
		return d
	}
	keep := func(cur *string, legacyBase, xdgVar string) {
		if filepath.IsAbs(getenv(xdgVar)) {
			return
		}
		legacy := filepath.Join(legacyBase, "bam")
		if !isDir(*cur) && isDir(legacy) {
			*cur = legacy
		}
	}
	if !filepath.IsAbs(getenv("XDG_CONFIG_HOME")) {
		d.OldConfig = filepath.Join(config, "bam")
	}
	keep(&d.Cache, cache, "XDG_CACHE_HOME")
	keep(&d.State, state, "XDG_STATE_HOME")
	return d
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}
