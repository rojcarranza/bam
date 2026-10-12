package tui

import (
	"slices"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/r0jjames/bam-cli/internal/provider"
)

const homeIdleAfter = 10 * time.Minute

// homeRefreshEvery is a variable only so tests can make the tick fire at
// once (see instantHomeTick); nothing else changes it.
var homeRefreshEvery = 30 * time.Second

// homeTickMsg is one beat of Home's auto-refresh loop.
type homeTickMsg struct{ Gen int }

func homeTickCmd(gen int) tea.Cmd {
	return tea.Tick(homeRefreshEvery, func(time.Time) tea.Msg { return homeTickMsg{Gen: gen} })
}

// restartHomeTick ends the running loop and, when Home shows, starts a new
// one. Bumping the generation is what ends the old loop: its next beat is
// dropped, so two loops can never run at once.
func (m *Model) restartHomeTick() tea.Cmd {
	m.home.tickGen++
	m.home.idle = false
	if m.screen != screenHome {
		return nil
	}
	return homeTickCmd(m.home.tickGen)
}

// homeTick reloads the plans while Home shows (home spec §4.2). A beat from
// an old loop, or one that arrives off Home, ends that loop: whatever shows
// Home again starts a new one. Under an overlay, a prompt or a load in
// flight the loop waits a beat without loading.
func (m Model) homeTick(msg homeTickMsg) (tea.Model, tea.Cmd) {
	if msg.Gen != m.home.tickGen || m.screen != screenHome {
		return m, nil
	}
	if m.now().Sub(m.home.lastKey) >= homeIdleAfter {
		m.home.idle = true
		return m, nil
	}
	next := homeTickCmd(m.home.tickGen)
	if m.overlay != overlayNone || m.inputFor != inputNone || m.svc == nil || m.plans.loading {
		return m, next
	}
	load := m.loadPlans()
	return m, tea.Batch(load, next)
}

// noteKey records a key press for the idle cut-off, and restarts the loop
// if idleness had stopped it.
func (m *Model) noteKey() tea.Cmd {
	m.home.lastKey = m.now()
	if !m.home.idle || m.screen != screenHome {
		return nil
	}
	return m.restartHomeTick()
}

// noteLive records a build the UI is watching or just started, for Home's
// running marker (home spec §4.4). A finished one leaves the marker and
// becomes its plan's last build, unless the row already shows a newer one:
// opening an old build must not rewind the table.
func (m *Model) noteLive(b provider.Build) {
	if b.Key == "" || b.PlanKey == "" {
		return
	}
	if m.home.live == nil {
		m.home.live = map[string]provider.Build{}
	}
	if !b.State.Finished() {
		m.home.live[b.PlanKey] = b
		return
	}
	delete(m.home.live, b.PlanKey)
	for i := range m.plans.items {
		p := &m.plans.items[i]
		if p.Key != b.PlanKey {
			continue
		}
		if p.LastBuild != nil && p.LastBuild.Number > b.Number {
			return
		}
		p.LastBuild = &provider.BuildSummary{Key: b.Key, Number: b.Number, State: b.State,
			FinishedAt: b.FinishedAt, Reason: b.Reason}
		keep, had := m.plans.selected()
		m.plans.refilter()
		if had {
			m.plans.selectFirst(func(q provider.Plan) bool { return q.Key == keep.Key })
		}
		return
	}
}

// plansLoaded folds a plans load into the shared list. The rows of a project
// that failed this time are kept from the last load, the cursor stays on the
// plan it was on, and a configured project the server lacks is named.
func (m Model) plansLoaded(msg plansLoadedMsg) Model {
	m.plans.loading = false
	items := append([]provider.Plan(nil), msg.Plans...)
	if len(msg.Failed) > 0 {
		failed := map[string]bool{}
		for _, k := range msg.Failed {
			failed[k] = true
		}
		for _, p := range m.plans.items {
			if failed[p.ProjectKey] {
				items = append(items, p)
			}
		}
		sort.Slice(items, func(i, j int) bool { return items[i].Key < items[j].Key })
	}
	keep, had := m.plans.selected()
	m.plans.setItems(items)
	if had {
		m.plans.selectFirst(func(p provider.Plan) bool { return p.Key == keep.Key })
	}
	if msg.Err != nil {
		m.err = msg.Err
		m.home.stale = true
		m.home.plansErr = true
	} else {
		m.home.stale = false
		m.home.loadedAt = m.now()
		// Only an error that came from the plans stream is this load's to
		// clear: one from another stream (a failed cancel, say) is not a
		// fact about the plans and must survive a plans refresh.
		if m.home.plansErr {
			m.err = nil
			m.home.plansErr = false
		}
	}
	// A 30s refresh repeats the same Missing set most of the time; setting
	// the status every time would overwrite an unrelated one (a cancel, a
	// copy) that has nothing to do with this load. Only a changed set says
	// so again.
	missing := append([]string(nil), msg.Missing...)
	if !slices.Equal(missing, m.home.missing) {
		if len(missing) > 0 {
			verb := "is"
			if len(missing) > 1 {
				verb = "are"
			}
			m.status = "project " + strings.Join(missing, ", ") + " " + verb + " configured but not on " + m.server
		}
		m.home.missing = missing
	}
	if msg.ProjectMissing != "" && msg.ProjectMissing == m.project {
		m.project = ""
		m.status = "project " + msg.ProjectMissing + " not found on " + m.server
	}
	if m.home.view == homePresets {
		m.sortPresets()
	}
	return m
}
