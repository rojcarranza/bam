// Package atomicfile writes files so that readers never see a half-written
// one, even when several bam processes write the same file at once.
package atomicfile

import (
	"io/fs"
	"os"
	"path/filepath"
)

// Write replaces path with data, readable with perm. It creates the parent
// directory (0700) when missing, writes a uniquely named temporary file next
// to path, and renames it into place. The temporary file never outlives a
// failed write.
func Write(path string, data []byte, perm fs.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // a no-op after the rename
	_, err = tmp.Write(data)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp.Name(), perm)
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	return err
}
