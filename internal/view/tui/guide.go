package tui

import (
	"context"
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// Choice is one row of Choose: a label and a dimmer detail beside it.
type Choice struct {
	Label, Detail string
}

// Choose is the small inline list of the guided bam preset add, drawn like
// the run picker. Typing filters on the label and the detail. Enter returns
// the index of the highlighted choice in items; Esc and ctrl-C return
// context.Canceled.
func Choose(ctx context.Context, in io.Reader, out io.Writer, prompt string, items []Choice) (int, error) {
	m := newChooser(prompt, items)
	final, err := tea.NewProgram(m, tea.WithInput(in), tea.WithOutput(out), tea.WithContext(ctx)).Run()
	if err != nil {
		if ctx.Err() != nil {
			return -1, ctx.Err()
		}
		return -1, err
	}
	c := final.(chooserModel)
	if c.chosen < 0 {
		return -1, context.Canceled
	}
	return c.chosen, nil
}

type indexed struct {
	Choice
	at int
}

type chooserModel struct {
	prompt string
	list   listState[indexed]
	width  int
	chosen int
	done   bool
}

func newChooser(prompt string, items []Choice) chooserModel {
	m := chooserModel{prompt: prompt, width: 80, chosen: -1, list: newList(func(i indexed) string { return i.Label + " " + i.Detail })}
	rows := make([]indexed, len(items))
	for i, it := range items {
		rows[i] = indexed{it, i}
	}
	m.list.setItems(rows)
	return m
}

func (m chooserModel) Init() tea.Cmd { return nil }

func (m chooserModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyEsc, tea.KeyCtrlC:
			m.done = true
			return m, tea.Quit
		case tea.KeyEnter:
			if it, ok := m.list.selected(); ok {
				m.chosen, m.done = it.at, true
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

func (m chooserModel) View() string {
	if m.done {
		return ""
	}
	cut := func(s string) string { return ansi.Truncate(s, max(m.width-1, 10), "…") }
	l := &m.list
	lines := []string{cut(m.prompt + " " + l.query + "▌")}
	if len(l.visible) == 0 {
		lines = append(lines, "  nothing matches")
	}
	labelW := 0
	for _, i := range l.visible {
		labelW = max(labelW, len([]rune(l.items[i].Label)))
	}
	start, end := l.window(pickerRows)
	for row := start; row < end; row++ {
		it := l.items[l.visible[row]]
		mark := "  "
		if row == l.cursor {
			mark = "> "
		}
		pad := strings.Repeat(" ", labelW-len([]rune(it.Label)))
		lines = append(lines, cut(strings.TrimRight(mark+it.Label+pad+"  "+dimStyle.Render(it.Detail), " ")))
	}
	lines = append(lines, cut(dimStyle.Render("  enter picks · esc cancels")))
	return strings.Join(lines, "\n")
}

// Ask is a one-line inline prompt with initial text in it. Enter returns the
// text; Esc and ctrl-C return context.Canceled.
func Ask(ctx context.Context, in io.Reader, out io.Writer, prompt, initial string) (string, error) {
	ti := textinput.New()
	ti.Prompt = ""
	ti.SetValue(initial)
	ti.CursorEnd()
	ti.Focus()
	final, err := tea.NewProgram(askModel{prompt: prompt, input: ti, width: 80}, tea.WithInput(in), tea.WithOutput(out), tea.WithContext(ctx)).Run()
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", err
	}
	a := final.(askModel)
	if !a.ok {
		return "", context.Canceled
	}
	return strings.TrimSpace(a.input.Value()), nil
}

type askModel struct {
	prompt string
	input  textinput.Model
	width  int
	ok     bool
	done   bool
}

func (m askModel) Init() tea.Cmd { return textinput.Blink }

func (m askModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyEsc, tea.KeyCtrlC:
			m.done = true
			return m, tea.Quit
		case tea.KeyEnter:
			m.ok, m.done = true, true
			return m, tea.Quit
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m askModel) View() string {
	if m.done {
		return ""
	}
	return ansi.Truncate(m.prompt+" "+m.input.View(), max(m.width-1, 10), "…") + "\n" +
		dimStyle.Render("  enter accepts · esc cancels")
}
