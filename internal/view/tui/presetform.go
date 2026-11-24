package tui

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/rojcarranza/bam/internal/app"
)

// presetFormRows is how many variables the form shows at once.
const presetFormRows = 10

// EditPresetForm is the inline form of bam preset add: one row per variable,
// drawn below the prompt like the run picker. ctrl-s returns the form with the
// chosen values; Esc and ctrl-C return context.Canceled, so nothing is written.
// load reads the values recent builds ran with while the form is open; nil
// means there are none to read.
func EditPresetForm(ctx context.Context, in io.Reader, out io.Writer, form app.PresetForm, load func(context.Context) []app.RecentVar) (app.PresetForm, error) {
	m := newPresetForm(ctx, form, load)
	final, err := tea.NewProgram(m, tea.WithInput(in), tea.WithOutput(out), tea.WithContext(ctx)).Run()
	if err != nil {
		if ctx.Err() != nil {
			return form, ctx.Err()
		}
		return form, err
	}
	f := final.(presetFormModel)
	if !f.saved {
		return form, context.Canceled
	}
	return f.form, nil
}

type recentMsg []app.RecentVar

type presetFormModel struct {
	ctx     context.Context
	form    app.PresetForm
	load    func(context.Context) []app.RecentVar
	loading bool
	cursor  int
	editing bool
	input   textinput.Model
	err     string
	width   int
	saved   bool
	done    bool
}

func newPresetForm(ctx context.Context, form app.PresetForm, load func(context.Context) []app.RecentVar) presetFormModel {
	in := textinput.New()
	in.Prompt = ""
	return presetFormModel{ctx: ctx, form: form, load: load, loading: load != nil, width: 80, input: in}
}

func (m presetFormModel) Init() tea.Cmd {
	if m.load == nil {
		return nil
	}
	return func() tea.Msg { return recentMsg(m.load(m.ctx)) }
}

func (m presetFormModel) row() *app.FormRow { return &m.form.Rows[m.cursor] }

func (m presetFormModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
	case recentMsg:
		m.loading = false
		m.form.AddRecent(msg)
	case tea.KeyMsg:
		if m.editing {
			return m.editKey(msg)
		}
		return m.key(msg)
	}
	return m, nil
}

func (m presetFormModel) key(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.err = ""
	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		m.done = true
		return m, tea.Quit
	case tea.KeyCtrlS:
		m.saved, m.done = true, true
		return m, tea.Quit
	case tea.KeyUp, tea.KeyCtrlP, tea.KeyShiftTab:
		m.cursor = max(m.cursor-1, 0)
	case tea.KeyDown, tea.KeyCtrlN, tea.KeyTab:
		m.cursor = min(m.cursor+1, len(m.form.Rows)-1)
	case tea.KeyLeft:
		step(m.row(), -1)
	case tea.KeyRight:
		step(m.row(), 1)
	case tea.KeyEnter:
		m.editing = true
		m.input.SetValue(editText(*m.row()))
		m.input.CursorEnd()
		m.input.Focus()
	}
	return m, nil
}

func (m presetFormModel) editKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.editing = false
		m.input.Blur()
		return m, nil
	case tea.KeyCtrlC:
		m.done = true
		return m, tea.Quit
	case tea.KeyEnter:
		r := m.row()
		text := m.input.Value()
		if r.Secret && !app.ValidEnvName(text) {
			m.err = "an environment variable name is letters, digits and _, not starting with a digit"
			return m, nil
		}
		if r.Secret {
			r.Value, r.Commented = "${"+text+"}", false
		} else {
			r.Value = text
		}
		m.editing = false
		m.input.Blur()
		m.err = ""
		m.cursor = min(m.cursor+1, len(m.form.Rows)-1)
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// editText is what enter puts in the input: the value, or for a secret only
// the environment variable name, never a value.
func editText(r app.FormRow) string {
	if r.Secret {
		return strings.TrimSuffix(strings.TrimPrefix(r.Value, "${"), "}")
	}
	return r.Value
}

// step moves a row to its next or previous choice, wrapping. A typed value
// that is none of the choices steps to the first or the last.
func step(r *app.FormRow, dir int) {
	if r.Secret {
		if r.CanStore {
			r.Commented = !r.Commented
		}
		return
	}
	n := len(r.Choices)
	if n == 0 {
		return
	}
	at := -1
	for i, c := range r.Choices {
		if c == r.Value {
			at = i
		}
	}
	switch {
	case at < 0 && dir > 0:
		at = 0
	case at < 0:
		at = n - 1
	default:
		at = (at + dir + n) % n
	}
	r.Value = r.Choices[at]
}

// View draws the header, one row per variable, the choices of the row under
// the cursor and the keys. It draws nothing once done, so the screen is left
// as it was.
func (m presetFormModel) View() string {
	if m.done {
		return ""
	}
	cut := func(s string) string { return ansi.Truncate(s, max(m.width-1, 10), "…") }
	f := m.form
	head := "new preset " + f.Name + " · " + f.Plan
	if f.Branch != "" {
		head += " · branch " + f.Branch
	}
	if f.Path != "" {
		head += " → " + f.Path
	}
	lines := []string{cut(head)}
	nameW := 0
	for _, r := range f.Rows {
		nameW = max(nameW, len(r.Name))
	}
	start, end := 0, len(f.Rows)
	if len(f.Rows) > presetFormRows {
		start = min(max(m.cursor-presetFormRows/2, 0), len(f.Rows)-presetFormRows)
		end = start + presetFormRows
	}
	for i := start; i < end; i++ {
		r := f.Rows[i]
		mark := "  "
		if i == m.cursor {
			mark = "> "
		}
		val := rowValue(r)
		if i == m.cursor && m.editing {
			val = m.input.View()
			if r.Secret {
				val = "${" + m.input.View() + "}"
			}
		}
		lines = append(lines, cut(strings.TrimRight(fmt.Sprintf("%s%-*s  %s  %s", mark, nameW, r.Name, val, dimStyle.Render(rowNote(r))), " ")))
	}
	if r := f.Rows[m.cursor]; len(r.Choices) > 1 && !m.editing {
		lines = append(lines, cut(dimStyle.Render("  ←→ "+strings.Join(quoted(r.Choices), " · "))))
	}
	lines = append(lines, cut(dimStyle.Render("  "+m.summary())))
	if m.err != "" {
		lines = append(lines, cut("  "+m.err))
	}
	keys := "↑↓ move · enter edit · ←→ choices · ctrl-s save · esc cancel"
	if m.editing {
		keys = "enter accept · esc keep the old value"
	}
	if m.loading {
		lines = append(lines, cut(dimStyle.Render("  reading recent builds…")))
	}
	lines = append(lines, cut(dimStyle.Render("  "+keys)))
	return strings.Join(lines, "\n")
}

func rowValue(r app.FormRow) string {
	switch {
	case r.Secret && r.Commented:
		return "plan's stored value"
	case r.Secret:
		return r.Value
	case r.Value == "":
		return "____"
	}
	return r.Value
}

func rowNote(r app.FormRow) string {
	switch {
	case r.Secret && r.Commented:
		return "secret · written as a commented line"
	case r.Secret:
		return "secret · read from your shell at run time"
	case r.Value == "" && r.Plan == "":
		return "no plan default"
	case r.Value == r.Plan:
		return "plan default"
	}
	return "changed"
}

func quoted(vs []string) []string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = fmt.Sprintf("%q", v)
	}
	return out
}

// summary counts the rows that differ from the plan and names them.
func (m presetFormModel) summary() string {
	var names []string
	for _, r := range m.form.Rows {
		if r.Differs() {
			names = append(names, r.Name)
		}
	}
	if len(names) == 0 {
		return fmt.Sprintf("0 of %d differ from the plan", len(m.form.Rows))
	}
	return fmt.Sprintf("%d of %d differ from the plan: %s", len(names), len(m.form.Rows), strings.Join(names, ", "))
}
