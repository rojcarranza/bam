package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// projectItem is one row of the projects table. The row with no key is
// (all): enter on it clears the project filter.
type projectItem struct {
	Key, Name, URL string
	Missing        bool // configured, but not on the server
}

// projectMatchText is what / matches a project on.
func projectMatchText(p projectItem) string {
	if p.Key == "" {
		return "(all) every project"
	}
	return p.Key + " " + p.Name
}

// projectLess orders the projects table by key or name under s. (all) is
// always first and missing projects always last.
func projectLess(s homeSort) func(a, b projectItem) bool {
	return func(a, b projectItem) bool {
		if (a.Key == "") != (b.Key == "") {
			return a.Key == ""
		}
		if a.Missing != b.Missing {
			return b.Missing
		}
		if a.Missing {
			return a.Key < b.Key
		}
		x, y := a.Key, b.Key
		if s.col == colName {
			x, y = a.Name, b.Name
		}
		if c := strings.Compare(x, y); c != 0 {
			if s.desc {
				return c > 0
			}
			return c < 0
		}
		return a.Key < b.Key
	}
}

func (m *Model) loadProjects() tea.Cmd {
	m.projectsGen++
	m.projects.loading = true
	return loadProjectsCmd(m.baseCtx(), m.svc, streamProjects, m.projectsGen)
}

// showProjects picks Home's projects table (:projects) and reloads it: the
// list is one request.
func (m Model) showProjects() (tea.Model, tea.Cmd) {
	m.home.view = homeProjects
	m.fromProjects = false
	var load tea.Cmd
	if m.svc != nil {
		load = m.loadProjects()
	}
	next, home := m.goHome()
	return next, tea.Batch(load, home)
}

// projectsLoaded fills the table. The cursor stays on the row it was on, or
// starts on the active project.
func (m Model) projectsLoaded(msg projectsLoadedMsg) Model {
	keep, had := m.projects.selected()
	items := []projectItem{{}}
	for _, p := range msg.Projects {
		items = append(items, projectItem{Key: p.Key, Name: p.Name, URL: p.URL})
	}
	for _, k := range msg.Missing {
		items = append(items, projectItem{Key: k, Missing: true})
	}
	m.projects.loading = false
	m.projects.setItems(items)
	want := m.project
	if had {
		want = keep.Key
	}
	m.projects.selectFirst(func(p projectItem) bool { return p.Key == want })
	return m
}

// projectCount is the projects the table shows, not counting (all) or the
// missing ones.
func (m Model) projectCount() int {
	n := 0
	for _, p := range m.projects.rows() {
		if p.Key != "" && !p.Missing {
			n++
		}
	}
	return n
}

// projectColumns fits the projects table to width; NAME takes what is left.
// The first column is the ● of the active project, the last says missing.
func projectColumns(width int, rows []projectItem) []tableCol {
	keyW := len("(all)")
	for _, p := range rows {
		keyW = max(keyW, lipgloss.Width(p.Key))
	}
	return fillWidth([]tableCol{
		{title: "", width: 1},
		{title: "KEY", width: min(keyW, 24)},
		{title: "NAME"},
		{title: "", width: len("missing")},
	}, width, "NAME")
}

func (m Model) projectCells(p projectItem) []string {
	mark := ""
	if p.Key == m.project {
		mark = "●"
	}
	switch {
	case p.Key == "":
		return []string{mark, "(all)", "every project", ""}
	case p.Missing:
		return []string{mark, dimStyle.Render(p.Key), "", dimStyle.Render("missing")}
	}
	return []string{mark, p.Key, p.Name, ""}
}

func (m Model) projectTable(width, height int) []string {
	rows := m.projects.rows()
	cols := projectColumns(width, rows)
	start, end := m.projects.window(height - 1)
	cells := make([][]string, 0, end-start)
	for _, p := range rows[start:end] {
		cells = append(cells, m.projectCells(p))
	}
	return tableLines(cols, cells, m.projects.cursor-start)
}

// handleProjectsKey takes the keys that differ on the projects table: enter
// narrows the plans to the project, r reloads the list, run and panel keys
// mean nothing here.
func (m Model) handleProjectsKey(msg tea.KeyMsg) (Model, tea.Cmd, bool) {
	switch {
	case key.Matches(msg, keys.Enter):
		p, ok := m.projects.selected()
		if !ok {
			return m, nil, true
		}
		if p.Missing {
			m.status = fmt.Sprintf("%s is not on this server", p.Key)
			return m, nil, true
		}
		next, load := m.setProject(p.Key)
		nm := next.(Model)
		nm.home.view = homePlans
		shown, home := nm.goHome()
		sm := shown.(Model)
		sm.fromProjects = true // after setProject, which ends any older trail
		return sm, tea.Batch(load, home), true
	case key.Matches(msg, keys.Refresh):
		if m.svc == nil {
			return m, nil, true
		}
		cmd := m.loadProjects()
		return m, cmd, true
	case key.Matches(msg, keys.Run),
		key.Matches(msg, keys.Panel1), key.Matches(msg, keys.Panel2), key.Matches(msg, keys.Panel3),
		key.Matches(msg, keys.NextPanel), key.Matches(msg, keys.PrevPanel):
		return m, nil, true
	}
	return m, nil, false
}

// backToProjects is esc on a plans table reached from the projects view:
// the projects table again, the cursor on the project just left.
func (m Model) backToProjects() (tea.Model, tea.Cmd) {
	project := m.project
	next, cmd := m.showProjects()
	nm := next.(Model)
	nm.projects.selectFirst(func(p projectItem) bool { return p.Key == project })
	return nm, cmd
}
