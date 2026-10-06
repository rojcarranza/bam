package tui

import (
	"cmp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/view/style"
)

// setItem is one row of the sets table: a configured set and this
// repository's newest record of it.
type setItem struct {
	Info app.SetInfo
	Last *app.SetRecord
}

type setsLoadedMsg struct {
	Gen   int
	Items []setItem
}

// loadSetsCmd reads the sets from the configuration and each one's newest
// record. Both are files, so the load is cheap and needs no server.
func loadSetsCmd(d Deps, svc *app.Service, gen int) tea.Cmd {
	return func() tea.Msg {
		if d.Sets == nil {
			return setsLoadedMsg{Gen: gen}
		}
		infos, err := d.Sets()
		if err != nil {
			return errMsg{Err: err, Where: "sets", Stream: streamSets, Gen: gen}
		}
		items := make([]setItem, 0, len(infos))
		for _, in := range infos {
			it := setItem{Info: in}
			if svc != nil && svc.Sets != nil {
				if rec, ok, err := svc.LatestSetRun(in.Name); err == nil && ok {
					it.Last = &rec
				}
			}
			items = append(items, it)
		}
		return setsLoadedMsg{Gen: gen, Items: items}
	}
}

func (m *Model) loadSets() tea.Cmd {
	m.setsGen++
	m.sets.loading = true
	return loadSetsCmd(m.deps, m.svc, m.setsGen)
}

// showSets picks Home's sets table (:sets).
func (m Model) showSets() (tea.Model, tea.Cmd) {
	m.home.view = homeSets
	load := m.loadSets()
	next, home := m.goHome()
	return next, tea.Batch(load, home)
}

// runCount is how many runs a set has after matrix expansion.
func runCount(in app.SetInfo) int {
	n := 0
	for _, st := range in.Stages {
		for _, r := range st {
			n += max(len(r.Runs), 1)
		}
	}
	return n
}

// setStatusState maps a record's status onto the build state whose glyph
// and colour it shares.
func setStatusState(s app.SetStatus) provider.State {
	switch s {
	case app.SetRunning:
		return provider.StateRunning
	case app.SetSuccessful:
		return provider.StateSuccess
	case app.SetFailed:
		return provider.StateFailed
	}
	return provider.StateStopped
}

// setStatusCell is the LAST RUN cell: the status's glyph and its words.
func setStatusCell(rec *app.SetRecord) string {
	if rec == nil {
		return "–"
	}
	s := setStatusState(rec.Status)
	return stateStyle(s).Render(style.Glyph(s) + " " + strings.ReplaceAll(string(rec.Status), "_", " "))
}

func setColumns(width int, rows []setItem) []tableCol {
	nameW := len("SET")
	for _, s := range rows {
		nameW = max(nameW, len(s.Info.Name))
	}
	cols := []tableCol{
		{title: "SET", width: min(nameW, 24)},
		{title: "STAGES", width: 6, right: true},
		{title: "RUNS", width: 4, right: true},
		{title: "LAST RUN"},
		{title: "AGE", width: 5},
	}
	return fillWidth(cols, width, "LAST RUN")
}

func (m Model) setTable(width, height int) []string {
	rows := m.sets.rows()
	cols := setColumns(width, rows)
	start, end := m.sets.window(height - 1)
	cells := make([][]string, 0, end-start)
	for _, s := range rows[start:end] {
		age := "–"
		if s.Last != nil {
			age = ageShort(m.now().Sub(s.Last.StartedAt))
		}
		status := setStatusCell(s.Last)
		if m.train.running() && m.train.set == s.Info.Name {
			status, age = setStatusCell(&app.SetRecord{Status: app.SetRunning}), "now"
		}
		row := make([]string, len(cols))
		for i, c := range cols {
			switch c.title {
			case "SET":
				row[i] = s.Info.Name
			case "STAGES":
				row[i] = strconv.Itoa(len(s.Info.Stages))
			case "RUNS":
				row[i] = strconv.Itoa(runCount(s.Info))
			case "LAST RUN":
				row[i] = status
			case "AGE":
				row[i] = age
			}
		}
		cells = append(cells, row)
	}
	return tableLines(cols, cells, m.sets.cursor-start)
}

// sortSets orders the sets by the sets sort: name, last-run status, or age.
func (m *Model) sortSets() {
	s := m.home.setSort
	keep, had := m.sets.selected()
	items := m.sets.items
	rank := func(it setItem) int {
		if it.Last == nil {
			return 9
		}
		return stateRank(&provider.BuildSummary{State: setStatusState(it.Last.Status)})
	}
	started := func(it setItem) time.Time {
		if it.Last == nil {
			return time.Time{}
		}
		return it.Last.StartedAt
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		var c int
		switch s.col {
		case colName:
			c = cmp.Compare(rank(a), rank(b))
		case colState:
			c = compareAge(started(a), started(b))
		default:
			c = strings.Compare(a.Info.Name, b.Info.Name)
		}
		if c != 0 {
			if s.desc {
				return c > 0
			}
			return c < 0
		}
		return a.Info.Name < b.Info.Name
	})
	m.sets.refilter()
	if had {
		m.sets.selectFirst(func(it setItem) bool { return it.Info.Name == keep.Info.Name })
	}
}

// handleSetsKey takes the keys that differ on the sets table: enter opens
// the set screen, R runs the set, panel keys mean nothing here.
func (m Model) handleSetsKey(msg tea.KeyMsg) (Model, tea.Cmd, bool) {
	switch {
	case key.Matches(msg, keys.Enter):
		next, cmd := m.openSet()
		return next.(Model), cmd, true
	case key.Matches(msg, keys.Run):
		if _, ok := m.sets.selected(); !ok {
			return m, nil, true
		}
		next, load := m.openSet()
		nm := next.(Model)
		if why := nm.refuseSetAction(); why != "" {
			nm.status = why
			return nm, load, true
		}
		next, cmd := nm.openSetVars(setActionRun)
		return next.(Model), tea.Batch(load, cmd), true
	case key.Matches(msg, keys.Refresh):
		return m, m.loadSets(), true
	case key.Matches(msg, keys.Panel1), key.Matches(msg, keys.Panel2), key.Matches(msg, keys.Panel3),
		key.Matches(msg, keys.NextPanel), key.Matches(msg, keys.PrevPanel):
		return m, nil, true
	}
	return m, nil, false
}
