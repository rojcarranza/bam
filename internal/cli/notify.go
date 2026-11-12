package cli

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/rojcarranza/bam/internal/provider"
	"github.com/spf13/cobra"
)

// addNotifyFlag adds --notify to a command that watches a build.
func addNotifyFlag(cmd *cobra.Command, r *runtime) {
	cmd.Flags().BoolVar(&r.notify, "notify", false, "when the watched build finishes, ring the terminal bell and raise a desktop notification where the system has one")
}

// notifyDone tells the user a watched build finished. The bell always rings
// (on stderr, so --json output stays clean); a desktop notification is a
// bonus that fails silently where none exists.
func (r *runtime) notifyDone(key string, state provider.State) {
	if !r.notify {
		return
	}
	_, _ = io.WriteString(r.env.Stderr, "\a")
	if r.env.Notify != nil {
		_ = r.env.Notify("bam: "+key, fmt.Sprintf("%s finished: %s", key, state))
	}
}

// desktopNotify uses notify-send on Linux and osascript on macOS.
func desktopNotify(title, body string) error {
	if path, err := exec.LookPath("osascript"); err == nil {
		script := fmt.Sprintf("display notification %s with title %s", appleQuote(body), appleQuote(title))
		return exec.Command(path, "-e", script).Run()
	}
	if path, err := exec.LookPath("notify-send"); err == nil {
		return exec.Command(path, "--", title, body).Run()
	}
	return errors.New("no desktop notifier")
}

func appleQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}
