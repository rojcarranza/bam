package tui

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/rojcarranza/bam/internal/app"
)

// pickerRows is how many presets the picker shows at once.
const pickerRows = 8

// PickPreset is the small inline picker of bare bam run: it draws below the
// prompt, not on a full screen, and leaves nothing behind. Typing filters on
// the name, the plan, the branch or the file, ignoring case. Enter returns the
// highlighted name; Esc and ctrl-C return context.Canceled, so nothing runs.
func PickPreset(ctx context.Context, in io.Reader, out io.Writer, items []app.TargetInfo) (string, error) {
	m := newPicker(items)
	final, err := tea.NewProgram(m, tea.WithInput(in), tea.WithOutput(out), tea.WithContext(ctx)).Run()
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", err
	}
	p := final.(pickerModel)
	if p.chosen == "" {
		return "", context.Canceled
	}
	return p.chosen, nil
}

type pickerModel struct {
	list   listState[app.TargetInfo]
	width  int
	chosen string
	done   bool
}

func newPicker(items []app.TargetInfo) pickerModel {
	m := pickerModel{width: 80, list: newList(pickerText)}
	m.list.setItems(items)
	return m
}

// pickerText is what a row is filtered on: every part the row shows.
func pickerText(t app.TargetInfo) string {
	return t.Name + " " + t.Summary() + " " + pickerFile(t)
}

// pickerFile is the file that wins the preset: the last to define it.
func pickerFile(t app.TargetInfo) string {
	if len(t.Files) == 0 {
		return ""
	}
	return filepath.Base(t.Files[len(t.Files)-1])
}

func (m pickerModel) Init() tea.Cmd { return nil }

func (m pickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyEsc, tea.KeyCtrlC:
			m.done = true
			return m, tea.Quit
		case tea.KeyEnter:
			if t, ok := m.list.selected(); ok {
				m.chosen = t.Name
				m.done = true
				return m, tea.Quit
			}
		case tea.KeyUp, tea.KeyCtrlP:
			m.list.move(-1)
		case tea.KeyDown, tea.KeyCtrlN, tea.KeyTab:
			m.list.move(1)
		case tea.KeyBackspace:
			if q := []rune(m.list.query); len(q) > 0 {
				m.list.setQuery(string(q[:len(q)-1]))
			}
		case tea.KeyCtrlU:
			m.list.setQuery("")
		case tea.KeySpace:
			m.list.setQuery(m.list.query + " ")
		case tea.KeyRunes:
			if !msg.Alt {
				m.list.setQuery(m.list.query + string(msg.Runes))
			}
		}
	}
	return m, nil
}

// View draws the prompt, a window of rows, the variables of the highlighted
// preset and the keys. It draws nothing once done, so the screen is left as
// it was.
func (m pickerModel) View() string {
	if m.done {
		return ""
	}
	cut := func(s string) string { return ansi.Truncate(s, max(m.width-1, 10), "…") }
	l := &m.list
	lines := []string{cut("run which preset? " + l.query + "▌")}
	if len(l.visible) == 0 {
		lines = append(lines, "  no preset matches")
	}
	nameW, sumW := 0, 0
	for _, i := range l.visible {
		nameW = max(nameW, len(l.items[i].Name))
		sumW = max(sumW, len([]rune(l.items[i].Summary())))
	}
	start, end := l.window(pickerRows)
	for row := start; row < end; row++ {
		t := l.items[l.visible[row]]
		mark := "  "
		if row == l.cursor {
			mark = "> "
		}
		lines = append(lines, cut(strings.TrimRight(fmt.Sprintf("%s%-*s  %-*s  %s", mark, nameW, t.Name, sumW, t.Summary(), pickerFile(t)), " ")))
	}
	if t, ok := l.selected(); ok {
		lines = append(lines, cut(dimStyle.Render("  sets: "+pickerVars(t))))
	}
	lines = append(lines, cut(dimStyle.Render(fmt.Sprintf("  %d of %d · enter runs · esc cancels", len(l.visible), len(l.items)))))
	return strings.Join(lines, "\n")
}

// pickerVars is the one line of what a preset sets: its variables with the
// secrets masked, and the ones it requires.
func pickerVars(t app.TargetInfo) string {
	var parts []string
	for _, v := range t.Defaults {
		parts = append(parts, v.Name+"="+v.Display())
	}
	for _, n := range t.Required {
		parts = append(parts, n+" (required)")
	}
	if len(parts) == 0 {
		return "nothing"
	}
	return strings.Join(parts, "  ")
}
