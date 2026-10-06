// Package tui is bam's terminal UI: a second view over the same app use
// cases the commands call. It never builds a provider and never reads
// configuration; cli injects everything through Deps.
package tui

import (
	"context"
	"io"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/errs"
)

// Server is one entry of the server registry, as the UI needs it.
type Server struct {
	Alias string
	URL   string
}

// Deps is everything the UI needs. Every field is injectable, so the model
// tests need no network, no keychain and no terminal.
type Deps struct {
	Servers   []Server
	Initial   string // alias to open with
	Connect   func(ctx context.Context, alias string) (*app.Service, error)
	Targets   func() ([]app.TargetInfo, error)
	Sets      func() ([]app.SetInfo, error) // re-read on every load, as Targets is
	Open      func(url string) error
	Getenv    func(name string) string // for ${ENV} references in a preset
	Clipboard io.Writer
	Output    io.Writer // the tea.Program's output; nil means os.Stdout
}

// Run opens the UI and returns when the user quits. Ending ctx closes the UI;
// it never stops a running build.
//
// The first connect happens here, before the program loop, because a failure
// to start is an exit code: no configured server, no token, an unreachable
// server. Once the UI is up, every later failure belongs in its status bar
// instead (spec §6).
func Run(ctx context.Context, d Deps) error {
	if d.Connect == nil {
		return errs.Configf("no server to open").
			WithWhy("bam has no server configured").
			WithTry("bam server add work --url https://bamboo.example.com")
	}
	m := New(d).withContext(ctx)
	switch msg := connectCmd(ctx, d, d.Initial, 0)().(type) {
	case errMsg:
		return msg.Err
	case connectedMsg:
		m = m.connected(msg)
	}

	opts := []tea.ProgramOption{tea.WithAltScreen(), tea.WithContext(ctx)}
	if d.Output != nil {
		opts = append(opts, tea.WithOutput(d.Output))
	}
	_, err := tea.NewProgram(m, opts...).Run()
	return err
}
