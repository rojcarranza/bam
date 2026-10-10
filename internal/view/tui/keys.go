package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
)

// keyMap is the whole keyboard surface. It is the single source of the help
// overlay, so a binding cannot ship without a line in the table.
type keyMap struct {
	Up, Down, Top, Bottom        key.Binding
	NextPanel, PrevPanel         key.Binding
	Panel1, Panel2, Panel3       key.Binding
	Enter, Back                  key.Binding
	Logs, AllLogs, Follow        key.Binding
	Filter, NextMatch, PrevMatch key.Binding
	Refresh, ExpandErr           key.Binding
	Open, Copy                   key.Binding
	Run, Trigger, DryRun         key.Binding
	CycleOption                  key.Binding
	Cancel                       key.Binding
	Resume, RerunFailed          key.Binding
	RemoveRow                    key.Binding
	Server, Project, Branch      key.Binding
	Sort, Help, Quit             key.Binding
	Command                      key.Binding
	Tree                         key.Binding
}

// helpRow is one line of the help overlay and of the README table.
type helpRow struct{ Group, Keys, Desc string }

// splitKeys turns "q ctrl+c" into the key list key.WithKeys wants.
//
// The one key that cannot be written literally is the space bar, because the
// separator is a space. It is spelled "space" and translated back here.
func splitKeys(s string) []string {
	out := strings.Fields(s)
	for i, k := range out {
		if k == "space" {
			out[i] = " "
		}
	}
	return out
}

func defaultKeys() keyMap {
	b := func(keys, help, desc string) key.Binding {
		return key.NewBinding(key.WithKeys(splitKeys(keys)...), key.WithHelp(help, desc))
	}
	return keyMap{
		Up:        b("up k", "j k ↑ ↓", "up / down"),
		Down:      b("down j", "j k ↑ ↓", "up / down"),
		Top:       b("g", "g G", "first / last"),
		Bottom:    b("G", "g G", "first / last"),
		NextPanel: b("tab", "tab", "next panel"),
		PrevPanel: b("shift+tab", "shift-tab", "previous panel"),
		Panel1:    b("1", "1 2 3", "focus a panel"),
		Panel2:    b("2", "1 2 3", "focus a panel"),
		Panel3:    b("3", "1 2 3", "focus a panel"),
		Enter:     b("enter", "enter", "drill in"),
		Back:      b("esc", "esc", "back / close"),
		Logs:      b("l", "l", "logs"),
		AllLogs:   b("a", "a", "all logs, not only failed"),
		Follow:    b("f", "f", "follow"),
		Filter:    b("/", "/", "filter or search"),
		NextMatch: b("n", "n N", "next / previous match"),
		PrevMatch: b("N", "n N", "next / previous match"),
		Refresh:   b("r", "r", "refresh the panel"),
		ExpandErr: b("e", "e", "expand the error"),
		Run:       b("R", "R", "open the run form"),
		Trigger:   b("ctrl+r", "ctrl-R", "run"),
		DryRun:    b("d", "d", "dry-run"),
		// space never opens a text field; it only steps an options field.
		CycleOption: b("space", "space", "cycle an options field"),
		Cancel:      b("C", "C", "cancel the build"),
		Resume:      b("U", "U", "resume the set run"),
		RerunFailed: b("F", "F", "rerun the failed runs"),
		RemoveRow:   b("x", "x", "remove the variable row"),
		Open:        b("o", "o", "open in the browser"),
		Copy:        b("y", "y", "copy the URL"),
		Server:      b("S", "S", "switch server"),
		Project:     b("P", "P", "filter by project"),
		Branch:      b("b", "b", "switch branch"),
		Sort:        b("s", "s", "next sort column"),
		Help:        b("?", "?", "help"),
		Quit:        b("q ctrl+c", "q ctrl-c", "quit"),
		Command:     b(":", ":", "command bar"),
		Tree:        b("t", "t", "build tree"),
	}
}

var keys = defaultKeys()

func allBindings(k keyMap) []key.Binding {
	return []key.Binding{
		k.Up, k.Down, k.Top, k.Bottom, k.NextPanel, k.PrevPanel,
		k.Panel1, k.Panel2, k.Panel3, k.Enter, k.Back,
		k.Logs, k.AllLogs, k.Follow, k.Filter, k.NextMatch, k.PrevMatch,
		k.Refresh, k.ExpandErr, k.Open, k.Copy, k.Run, k.Trigger, k.DryRun,
		k.CycleOption, k.Cancel, k.Resume, k.RerunFailed, k.RemoveRow, k.Server, k.Project, k.Branch, k.Sort, k.Help, k.Quit,
		k.Command, k.Tree,
	}
}

// helpRows is the help overlay's table, grouped as spec §4 prints it.
func (k keyMap) helpRows() []helpRow {
	return []helpRow{
		{"MOVE", "j k ↑ ↓", "up / down in the focused panel"},
		{"MOVE", "g G", "first / last row"},
		{"MOVE", "tab", "next panel: Builds, Stages, Tree"},
		{"MOVE", "shift-tab", "previous panel"},
		{"MOVE", "1 2 3", "focus Builds, Stages, Tree (on Home: open the plan screen there)"},
		{"MOVE", "enter", "drill in: a build's stages, expand a stage, open a triggered plan"},
		{"MOVE", "esc", "back one level, close an overlay; from Stages or Tree to Builds; from a triggered plan to its parent; from Builds to Home; from a project's plans to :projects"},
		{"HOME", "enter", "open the plan screen on the row's plan"},
		{"HOME", "s", "next sort column"},
		{"HOME", ":", "command bar: :plans :presets :sets :projects :tree :project :server :sort :q"},
		{"VIEW", "l", "logs for the selection: a stage, a job, or failed jobs; a running build: its current stage, live"},
		{"VIEW", "a", "all logs, not only failed; in the set vars prompt, add a variable"},
		{"VIEW", "f", "follow the jobs on the log screen"},
		{"VIEW", "/", "filter the focused list, or search the log"},
		{"VIEW", "n N", "next / previous match, or next failure with no search"},
		{"VIEW", "r", "refresh the focused panel"},
		{"VIEW", "t", "the build tree of the selected build: every build it triggered"},
		{"VIEW", "e", "expand the current error"},
		{"RUN", "R", "open the run form for the selection"},
		{"RUN", "ctrl-R", "run (in the form)"},
		{"RUN", "d", "dry-run: show what would be sent (in the form)"},
		{"RUN", "space", "cycle an options field (in the form)"},
		{"RUN", "f", "use the build's revision (in the form, on the revision row)"},
		{"RUN", "C", "cancel the selected build (asks first)"},
		{"RUN", "U", "resume the set's interrupted run (set screen)"},
		{"RUN", "F", "rerun the failed runs (set screen) or builds (tree screen)"},
		{"RUN", "x", "remove a variable row (set vars prompt)"},
		{"GO", "o", "open the selection's Bamboo URL in the browser"},
		{"GO", "y", "copy the selection's Bamboo URL"},
		{"GO", "S", "switch server"},
		{"GO", "P", "filter by project"},
		{"GO", "b", "switch plan branch"},
		{"META", "?", "help"},
		{"META", "q ctrl-c", "quit"},
	}
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.NextPanel, k.Enter, k.Logs, k.Open, k.Help, k.Quit}
}

func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.Top, k.Bottom, k.NextPanel, k.Enter, k.Back},
		{k.Logs, k.AllLogs, k.Follow, k.Filter, k.NextMatch, k.Refresh, k.ExpandErr},
		{k.Open, k.Copy, k.Server, k.Project, k.Branch, k.Help, k.Quit},
	}
}
