package config

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// configFiles are the files bam keeps in its config directory. MoveDir moves
// only these: on macOS the old directory also holds state.json.
var configFiles = []string{"config.yaml", "credentials.yaml", "fixture-denylist.txt"}

// Dir is bam's config directory on every platform: $XDG_CONFIG_HOME/bam, or
// ~/.config/bam when the variable is unset or not absolute.
func Dir(getenv func(string) string, home string) string {
	if x := getenv("XDG_CONFIG_HOME"); filepath.IsAbs(x) {
		return filepath.Join(x, "bam")
	}
	return filepath.Join(home, ".config", "bam")
}

// MoveDir moves bam's config files from an older config directory (macOS
// ~/Library/Application Support/bam, Windows %LOCALAPPDATA%\bam) into to, and
// returns the names it moved. A file already in to is never replaced, and the
// old copy stays where it is. So does a file that one of keep names, because
// BAM_CONFIG or BAM_FIXTURE_DENYLIST points there.
func MoveDir(from, to string, keep ...string) ([]string, error) {
	if filepath.Clean(from) == filepath.Clean(to) {
		return nil, nil
	}
	var moved []string
	for _, name := range configFiles {
		src, dst := filepath.Join(from, name), filepath.Join(to, name)
		if _, err := os.Lstat(src); errors.Is(err, fs.ErrNotExist) {
			continue
		} else if err != nil {
			return moved, err
		}
		if namedBy(keep, src) {
			continue
		}
		if _, err := os.Lstat(dst); err == nil {
			continue
		} else if !errors.Is(err, fs.ErrNotExist) {
			return moved, err
		}
		if err := os.MkdirAll(to, 0o700); err != nil {
			return moved, err
		}
		if err := moveFile(src, dst); err != nil {
			return moved, err
		}
		moved = append(moved, name)
	}
	return moved, nil
}

// namedBy reports whether one of paths is the file at path, however it is
// spelled.
func namedBy(paths []string, path string) bool {
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	for _, p := range paths {
		if other, err := os.Stat(p); err == nil && os.SameFile(fi, other) {
			return true
		}
	}
	return false
}

// moveFile renames src to dst, copying with src's mode when they are on
// different file systems.
func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	fi, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fi.Mode().Perm())
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if err = errors.Join(err, out.Close()); err != nil {
		return errors.Join(err, os.Remove(dst))
	}
	return os.Remove(src)
}
