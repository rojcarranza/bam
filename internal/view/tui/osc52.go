package tui

import (
	"encoding/base64"
	"io"
)

// osc52 asks the terminal emulator to put s on the system clipboard. It is an
// escape sequence rather than a call to pbcopy, xclip or wl-copy, so it works
// through ssh and tmux, where no local clipboard binary can be reached, and
// so bam needs no clipboard dependency of its own.
//
// Not every terminal honours it, so the UI says what it did and the help
// names o as the fallback.
func osc52(w io.Writer, s string) error {
	if w == nil {
		return nil
	}
	_, err := io.WriteString(w, "\x1b]52;c;"+base64.StdEncoding.EncodeToString([]byte(s))+"\x07")
	return err
}

// CopyToClipboard asks the terminal to put s on the clipboard, as the
// terminal UI's copy key does.
func CopyToClipboard(w io.Writer, s string) error { return osc52(w, s) }
