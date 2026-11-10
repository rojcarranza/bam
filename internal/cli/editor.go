package cli

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"strings"

	"github.com/rojcarranza/bam/internal/errs"
)

// errEditorCancelled is an editor that exited non-zero, such as :cq in vim.
// bam run --edit treats it as the user aborting the run.
var errEditorCancelled = errors.New("the editor exited with an error")

// runEditor writes initial to a private temp file, opens it in cmd, and
// returns what the user saved. The file is removed whatever happens: it may
// hold a secret the user typed.
func runEditor(cmd string, initial []byte) ([]byte, error) {
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return nil, errs.Configf("no editor to open").WithTry("set BAM_EDITOR or EDITOR")
	}
	f, err := os.CreateTemp("", "bam-run-*.env") // CreateTemp makes it 0600
	if err != nil {
		return nil, err
	}
	path := f.Name()
	defer func() { _ = os.Remove(path) }()
	if _, err := f.Write(initial); err != nil {
		_ = f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}

	c := exec.Command(fields[0], append(fields[1:], path)...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := c.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return nil, errEditorCancelled
		}
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
			return nil, errs.Configf("editor `%s` not found", fields[0]).WithTry("set BAM_EDITOR or EDITOR")
		}
		return nil, err
	}
	return os.ReadFile(path)
}
