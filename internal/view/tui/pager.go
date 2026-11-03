package tui

import (
	"io"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/r0jjames/bam-cli/internal/errs"
)

// pagerDoneMsg ends the pager. A pager that exits non-zero is reported, since
// it may mean the command is not installed.
type pagerDoneMsg struct{ Err error }

// pagerExec runs the pager on the real terminal while bubbletea steps aside.
type pagerExec struct {
	run  func(cmd string, r io.Reader) error
	cmd  string
	text string
}

func (p *pagerExec) SetStdin(io.Reader)  {}
func (p *pagerExec) SetStdout(io.Writer) {}
func (p *pagerExec) SetStderr(io.Writer) {}

func (p *pagerExec) Run() error { return p.run(p.cmd, strings.NewReader(p.text)) }

// openPager is E: the whole log, not the viewport's window, through the pager.
func (m Model) openPager() (tea.Model, tea.Cmd) {
	if len(m.logs.lines) == 0 {
		return m, nil
	}
	if m.deps.Pager == "" || m.deps.RunPager == nil {
		m.err = errs.Configf("no pager to open").
			WithTry("set BAM_PAGER or PAGER")
		return m, nil
	}
	m.err = nil
	return m, tea.Exec(&pagerExec{run: m.deps.RunPager, cmd: m.deps.Pager, text: m.logs.fullText()},
		func(err error) tea.Msg { return pagerDoneMsg{Err: err} })
}
