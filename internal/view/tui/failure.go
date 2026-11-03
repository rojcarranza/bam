package tui

import (
	"bytes"
	"context"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/view"
)

// investigateContext and investigateMax shape the overlay as bam build
// investigate's defaults do: a few lines around each error, a few errors per job.
const (
	investigateContext = 2
	investigateMax     = 3
)

// openInvestigate is i on the plan screen: why the open build failed, from the
// error lines of its failed jobs' logs, as bam build investigate prints them.
func (m Model) openInvestigate() (tea.Model, tea.Cmd) {
	if m.screen != screenPlan || m.svc == nil {
		return m, nil
	}
	b, ok := m.currentBuild()
	if !ok {
		return m, nil
	}
	switch {
	case b.State != provider.StateFailed:
		m.status = b.Key + " did not fail; nothing to investigate"
		return m, nil
	case m.plan.stagesLoading && m.detail != nil && m.detail.Key == b.Key:
		m.status = "still reading " + b.Key + "; try again in a moment"
		return m, nil
	}
	m.infoGen++
	m = m.openInfo("Why "+b.Key+" failed", "reading the failed jobs' logs…")
	width := overlayWidth(m.width, helpMaxWidth) - 2
	out := func(buf *bytes.Buffer) view.Out { return m.infoOut(buf, width) }
	return m, investigateCmd(m.baseCtx(), m.svc, b, out, m.infoGen)
}

// investigateCmd reads b's failed logs and renders them with the printer
// bam build investigate uses.
func investigateCmd(ctx context.Context, svc *app.Service, b provider.Build, o func(*bytes.Buffer) view.Out, gen int) tea.Cmd {
	return func() tea.Msg {
		inv, err := svc.Investigate(ctx, b, app.InvestigateOptions{Context: investigateContext, Max: investigateMax})
		if err != nil {
			return errMsg{Err: err, Where: "investigate", Stream: streamInfo, Gen: gen}
		}
		var buf bytes.Buffer
		if err := view.Investigation(o(&buf), inv); err != nil {
			return errMsg{Err: err, Where: "investigate", Stream: streamInfo, Gen: gen}
		}
		return infoLoadedMsg{Gen: gen, Title: "Why " + b.Key + " failed", Body: buf.String()}
	}
}

// planRerunMsg is the answer to F on the plan screen.
type planRerunMsg struct {
	Gen       int
	Key       string
	Build     provider.Build
	Restarted bool
	Err       error
}

// askRerun is F on the plan screen: it says why it cannot act, or asks before
// restarting the failed jobs of the open build.
func (m Model) askRerun() (tea.Model, tea.Cmd) {
	if m.screen != screenPlan || m.svc == nil {
		return m, nil
	}
	b, ok := m.currentBuild()
	if !ok {
		return m, nil
	}
	switch b.State {
	case provider.StateFailed, provider.StateStopped:
	case provider.StateSuccess:
		m.status = b.Key + " succeeded; nothing to rerun"
		return m, nil
	default:
		m.status = b.Key + " has not finished; nothing to rerun"
		return m, nil
	}
	if m.svc.RefusesRestart(m.baseCtx()) {
		m.status = "this Bamboo cannot restart a build over REST; rerun it from Bamboo (o opens it)"
		return m, nil
	}
	return m.ask("Rerun the failed jobs of "+b.Key+"?", func(m Model) (tea.Model, tea.Cmd) {
		m.rerunGen++
		gen, svc, ctx := m.rerunGen, m.svc, m.baseCtx()
		return m, func() tea.Msg {
			nb, restarted, err := svc.Restart(ctx, b.Key)
			return planRerunMsg{Gen: gen, Key: b.Key, Build: nb, Restarted: restarted, Err: err}
		}
	})
}

// handlePlanRerun reports the restart and, when the restarted build is the
// one open, reads it again so it is watched while it runs.
func (m Model) handlePlanRerun(msg planRerunMsg) (tea.Model, tea.Cmd) {
	if msg.Gen != m.rerunGen {
		return m, nil
	}
	switch {
	case msg.Err != nil:
		m.err, m.status = msg.Err, ""
		return m, nil
	case !msg.Restarted:
		m.status = msg.Key + " was not restarted"
		return m, nil
	}
	m.status = "restarted " + msg.Key
	delete(m.plan.cache, msg.Key)
	if m.screen != screenPlan || m.detail == nil || m.detail.Key != msg.Key {
		return m, nil
	}
	nb := msg.Build
	if nb.Key == "" {
		nb = *m.detail
	}
	return m.showBuild(nb)
}
