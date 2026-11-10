package view

import (
	"errors"
	"fmt"
	"io"

	"github.com/rojcarranza/bam/internal/errs"
)

// PrintError writes what failed, why, and what to try. The cause appears only with debug.
func PrintError(w io.Writer, err error, debug bool) {
	var e *errs.Error
	if !errors.As(err, &e) {
		fmt.Fprintf(w, "error: %s\n", err)
		return
	}
	fmt.Fprintf(w, "error: %s\n", e.What)
	if e.Why != "" {
		fmt.Fprintf(w, "  %s\n", e.Why)
	}
	if e.Try != "" {
		fmt.Fprintf(w, "  try: %s\n", e.Try)
	}
	if debug && e.Err != nil {
		fmt.Fprintf(w, "  cause: %v\n", e.Err)
	}
}

// PrintWarnings writes one "warning:" line each.
func PrintWarnings(w io.Writer, warnings []string) {
	for _, m := range warnings {
		fmt.Fprintf(w, "warning: %s\n", m)
	}
}
