package view

import (
	"fmt"
	"io"

	"github.com/rojcarranza/bam/internal/app"
	"github.com/rojcarranza/bam/internal/view/style"
)

// PrintLogs writes job logs; with several jobs each gets a tail-style header.
func PrintLogs(w io.Writer, logs []app.JobLog) error {
	for i, l := range logs {
		if len(logs) > 1 {
			if i > 0 {
				fmt.Fprintln(w)
			}
			fmt.Fprintf(w, "==> %s  %s (%s) <==\n", l.Job.Key, l.Job.Name, style.Label(l.Job.State))
		}
		for _, line := range l.Lines {
			if _, err := fmt.Fprintln(w, line); err != nil {
				return err
			}
		}
	}
	return nil
}

// PagerCommand picks the pager: BAM_PAGER, the machine file, PAGER, then
// less -FRX. Windows gets no default pager.
func PagerCommand(getenv func(string) string, machinePager, goos string) string {
	for _, v := range []string{getenv("BAM_PAGER"), machinePager, getenv("PAGER")} {
		if v != "" {
			return v
		}
	}
	if goos == "windows" {
		return ""
	}
	return "less -FRX"
}
