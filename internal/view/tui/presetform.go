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

// presetFormRows is how many rows, variables and settings, the form shows at once.
const presetFormRows = 10

// EditPresetForm is the inline form of bam preset add and bam preset edit: one row per variable,
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

// reposMsg is the repositories Bamboo lists for branch, or why it could not.
type reposMsg struct {
	branch string
	repos  []app.RepoBranch
	err    error
}

type presetFormModel struct {
	ctx     context.Context
	form    app.PresetForm
	load    func(context.Context) []app.RecentVar
	loading bool
	cursor  int
	editing bool
	options bool      // the input holds the options of the variable under the cursor
	adding  bool      // the input holds a new repository, NAME=BRANCH
	reading string    // the branch whose repositories are being read
	held    *reposMsg // an answer that came in while a setting was being typed
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

// row is the variable under the cursor, or nil on a setting of the edit form.
func (m presetFormModel) row() *app.FormRow {
	if m.cursor >= len(m.form.Rows) {
		return nil
	}
	return &m.form.Rows[m.cursor]
}

func (m presetFormModel) field() *app.FormField {
	if i := m.cursor - len(m.form.Rows); i >= 0 && i < len(m.form.Fields) {
		return &m.form.Fields[i]
	}
	return nil
}

func (m presetFormModel) lines() int { return len(m.form.Rows) + len(m.form.Fields) }

func (m presetFormModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
	case recentMsg:
		m.loading = false
		m.form.AddRecent(msg)
	case reposMsg:
		return m.repos(msg)
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
		if m.form.Editing {
			if m.reading != "" {
				// The answer would replace the repositories, so saving now would keep the old branches.
				m.err = "still reading the repositories of " + m.reading + "; save once they are listed"
				return m, nil
			}
			if err := m.form.Validate(); err != nil {
				m.err = err.Error()
				return m, nil
			}
		}
		m.saved, m.done = true, true
		return m, tea.Quit
	case tea.KeyUp, tea.KeyCtrlP, tea.KeyShiftTab:
		m.cursor = max(m.cursor-1, 0)
	case tea.KeyDown, tea.KeyCtrlN, tea.KeyTab:
		m.cursor = min(m.cursor+1, m.lines()-1)
	case tea.KeyLeft:
		if r := m.row(); r != nil {
			step(r, -1)
		}
	case tea.KeyRight:
		if r := m.row(); r != nil {
			step(r, 1)
		}
	case tea.KeyEnter:
		if m.readingBlocks(false) {
			return m, nil
		}
		m.beginEdit(m.editValue())
	case tea.KeyRunes:
		if !m.form.Editing || len(msg.Runes) != 1 {
			return m, nil
		}
		if m.readingBlocks(msg.Runes[0] == 'a') {
			return m, nil
		}
		switch r, fl := m.row(), m.field(); {
		case msg.Runes[0] == 'a':
			m.adding = true
			m.beginEdit("")
		case msg.Runes[0] == 'x' && r != nil:
			r.Removed = !r.Removed
		case msg.Runes[0] == 'x' && fl != nil && strings.HasPrefix(fl.Key, "repositories."):
			fl.Removed = !fl.Removed
		case msg.Runes[0] == 'o' && r != nil && !r.Secret:
			m.options = true
			m.beginEdit(strings.Join(r.Options, ", "))
		}
	}
	return m, nil
}

// readingBlocks refuses, while the repositories of a branch are being read,
// adding one or changing the one under the cursor (enter, x): the answer
// replaces the list and would undo it.
func (m *presetFormModel) readingBlocks(adding bool) bool {
	if m.reading == "" {
		return false
	}
	if fl := m.field(); !adding && (fl == nil || !strings.HasPrefix(fl.Key, "repositories.")) {
		return false
	}
	m.err = "still reading the repositories of " + m.reading + "; change them once they are listed"
	return true
}

// repos applies the repositories Bamboo listed for the branch just typed. It
// is ignored when the branch has changed again since.
func (m presetFormModel) repos(msg reposMsg) (tea.Model, tea.Cmd) {
	if m.reading != msg.branch {
		return m, nil
	}
	if m.editing {
		// Applying it now would shift the settings under the text being typed.
		m.held = &msg
		return m, nil
	}
	return m.applyRepos(msg)
}

// applyRepos shows the repositories read, keeping the cursor on the setting
// it was on, since the answer adds and removes settings after the branch.
func (m presetFormModel) applyRepos(msg reposMsg) (tea.Model, tea.Cmd) {
	m.reading, m.held = "", nil
	if msg.err != nil {
		m.err = "could not read the repositories of " + msg.branch + ": " + msg.err.Error()
		return m, nil
	}
	at := m.lineID()
	m.form.UseRepositories(msg.repos)
	m.cursor = m.lineIndex(at)
	return m, nil
}

// lineID names the line under the cursor in a way that survives lines being
// added or removed.
func (m presetFormModel) lineID() string {
	if r := m.row(); r != nil {
		return "row:" + r.Name
	}
	if fl := m.field(); fl != nil {
		return "field:" + fl.Key
	}
	return ""
}

// lineIndex is where the line named id is now, or the last line when it is gone.
func (m presetFormModel) lineIndex(id string) int {
	for i := range m.lines() {
		c := m
		c.cursor = i
		if c.lineID() == id {
			return i
		}
	}
	return max(m.lines()-1, 0)
}

// branchValue is the text of the branch setting.
func (m presetFormModel) branchValue() string {
	for _, fl := range m.form.Fields {
		if fl.Key == "branch" {
			return strings.TrimSpace(fl.Value)
		}
	}
	return ""
}

func (m presetFormModel) editKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.endEdit()
		if h := m.held; h != nil {
			return m.applyRepos(*h)
		}
		return m, nil
	case tea.KeyCtrlC:
		m.done = true
		return m, tea.Quit
	case tea.KeyEnter:
		text := m.input.Value()
		before := m.branchValue()
		switch r, fl := m.row(), m.field(); {
		case m.adding:
			name, branch, ok := strings.Cut(text, "=")
			if name, branch = strings.TrimSpace(name), strings.TrimSpace(branch); !ok || name == "" || branch == "" {
				m.err = "type the repository and its branch as NAME=BRANCH"
				return m, nil
			}
			m.form.SetRepository(name, branch)
		case m.options:
			r.Options = app.SplitList(text)
			r.Choices = optionChoices(*r)
		case fl != nil:
			fl.Value = text
		case r.Secret && !app.ValidEnvName(text):
			m.err = "an environment variable name is letters, digits and _, not starting with a digit"
			return m, nil
		case r.Secret:
			r.Value, r.Commented, r.Removed = "${"+text+"}", false, false
		default:
			r.Value, r.Removed = text, false
		}
		m.endEdit()
		m.err = ""
		m.cursor = min(m.cursor+1, m.lines()-1)
		after := m.branchValue()
		if after != before && after == "" {
			m.reading, m.held = "", nil
		}
		if after != before && after != "" && m.form.ReadRepos != nil {
			m.reading, m.held = after, nil
			read, ctx := m.form.ReadRepos, m.ctx
			return m, func() tea.Msg {
				repos, err := read(ctx, after)
				return reposMsg{branch: after, repos: repos, err: err}
			}
		}
		if h := m.held; h != nil {
			return m.applyRepos(*h)
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// beginEdit opens the input on text.
func (m *presetFormModel) beginEdit(text string) {
	m.editing = true
	m.input.SetValue(text)
	m.input.CursorEnd()
	m.input.Focus()
}

func (m *presetFormModel) endEdit() {
	m.editing, m.options, m.adding = false, false, false
	m.input.Blur()
}

// editValue is the text enter opens for the row or setting under the cursor.
func (m presetFormModel) editValue() string {
	if fl := m.field(); fl != nil {
		return fl.Value
	}
	return editText(*m.row())
}

// optionChoices is the values the arrow keys step through: the allowed values
// when the preset lists them, else the current value and the plan's.
func optionChoices(r app.FormRow) []string {
	if len(r.Options) > 0 {
		return append([]string(nil), r.Options...)
	}
	return app.AppendChoices(nil, r.Value, r.Plan)
}

// editText is what enter puts in the input: the value, or for a secret only
// the environment variable name, never a value.
func editText(r app.FormRow) string {
	if r.Literal && r.Value == r.Orig {
		return ""
	}
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
			r.Commented, r.Removed = !r.Commented, false
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
	r.Value, r.Removed = r.Choices[at], false
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
	if f.Editing {
		head = "edit preset " + f.Name + " · " + f.Plan
	}
	if f.Branch != "" && !f.Editing {
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
	for _, fl := range f.Fields {
		nameW = max(nameW, len(fl.Label))
	}
	total := m.lines()
	start, end := 0, total
	if total > presetFormRows {
		start = min(max(m.cursor-presetFormRows/2, 0), total-presetFormRows)
		end = start + presetFormRows
	}
	for i := start; i < end; i++ {
		mark := "  "
		if i == m.cursor {
			mark = "> "
		}
		input := m.input.View()
		var label, val, note string
		if i < len(f.Rows) {
			r := f.Rows[i]
			label, val, note = r.Name, rowValue(r), rowNote(r, f.Editing, f.Held)
			if i == m.cursor && m.editing && !m.adding && !m.options {
				val = input
				if r.Secret {
					val = "${" + input + "}"
				}
			} else if i == m.cursor && m.editing && !m.adding {
				val, note = rowValue(r), "options, separated by commas: "+input
			}
		} else {
			fl := f.Fields[i-len(f.Rows)]
			label, val = fl.Label, fl.Value
			if val == "" {
				val = "____"
			}
			if i == m.cursor && m.editing && !m.adding {
				val = input
			}
			switch {
			case fl.Removed && f.Held.Also[fl.Key] != "":
				note = "removed here: the value in " + f.Held.Also[fl.Key] + " applies"
			case fl.Removed:
				note = "removed"
			case fl.Orig == "" && strings.HasPrefix(fl.Key, "repositories."):
				note = "added"
			case strings.TrimSpace(fl.Value) == "" && strings.TrimSpace(fl.Orig) != "" && f.Held.Also[fl.Key] != "":
				note = "cleared here: the value in " + f.Held.Also[fl.Key] + " applies"
			case fl.Value != fl.Orig:
				note = "changed"
			}
		}
		lines = append(lines, cut(strings.TrimRight(fmt.Sprintf("%s%-*s  %s  %s", mark, nameW, label, val, dimStyle.Render(note)), " ")))
	}
	if r := m.row(); r != nil && len(r.Choices) > 1 && !m.editing {
		lines = append(lines, cut(dimStyle.Render("  ←→ "+strings.Join(quoted(r.Choices), " · "))))
	}
	if m.adding {
		lines = append(lines, cut("  new repository, NAME=BRANCH: "+m.input.View()))
	}
	lines = append(lines, cut(dimStyle.Render("  "+m.summary())))
	if m.err != "" {
		lines = append(lines, cut("  "+m.err))
	}
	keys := "↑↓ move · enter edit · ←→ choices · ctrl-s save · esc cancel"
	if f.Editing {
		keys = "↑↓ move · enter edit · ←→ choices · x remove · o options · a add repository · ctrl-s save · esc cancel"
	}
	if m.editing {
		keys = "enter accept · esc keep the old value"
	}
	if m.loading {
		lines = append(lines, cut(dimStyle.Render("  reading recent builds…")))
	}
	if m.reading != "" {
		lines = append(lines, cut(dimStyle.Render("  reading the repositories of "+m.reading+"…")))
	}
	lines = append(lines, cut(dimStyle.Render("  "+keys)))
	return strings.Join(lines, "\n")
}

func rowValue(r app.FormRow) string {
	switch {
	case r.Secret && r.Commented:
		return "plan's stored value"
	case r.Secret && r.Literal && r.Value == r.Orig:
		return "********"
	case r.Secret:
		return r.Value
	case r.Value == "":
		return "____"
	}
	return r.Value
}

func rowNote(r app.FormRow, editing bool, held app.Held) string {
	if editing {
		return editNote(r, held)
	}
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

// editNote says where a row of the edit form stands against the saved preset.
func editNote(r app.FormRow, held app.Held) string {
	var parts []string
	switch {
	case r.Removed && r.InPreset && held.Also["variables."+r.Name] != "":
		parts = append(parts, "removed here: the value in "+held.Also["variables."+r.Name]+" applies")
	case r.Removed && r.InPreset:
		parts = append(parts, "removed: the plan's own value applies")
	case r.Removed:
		parts = append(parts, "plan value")
	case !r.InPreset && r.Value == r.Orig:
		parts = append(parts, "not in the preset")
	case r.Value != r.Orig:
		parts = append(parts, "changed")
	case r.Literal:
		parts = append(parts, "secret · a literal value is saved; enter a variable name to replace it")
	case r.Secret:
		parts = append(parts, "secret · read from your shell at run time")
	default:
		parts = append(parts, "saved")
	}
	if len(r.Options) > 0 && r.Secret {
		// A secret's options are values like its own, so they stay hidden.
		parts = append(parts, "options: ********")
	} else if len(r.Options) > 0 {
		parts = append(parts, "options: "+strings.Join(r.Options, ", "))
	} else if len(r.OrigOptions) > 0 && held.Also["options."+r.Name] != "" {
		parts = append(parts, "options cleared here: the value in "+held.Also["options."+r.Name]+" applies")
	}
	return strings.Join(parts, " · ")
}

func quoted(vs []string) []string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = fmt.Sprintf("%q", v)
	}
	return out
}

// summary counts the rows that differ from the plan and names them; in the
// edit form it counts what ctrl-s would change instead.
func (m presetFormModel) summary() string {
	if m.form.Editing {
		e := m.form.Edit()
		n := len(e.Set) + len(e.Unset) + len(e.Options) + m.form.RepositoryChanges()
		for _, p := range []bool{e.Branch != nil, e.Required != nil, e.Timeout != nil, e.Rename != ""} {
			if p {
				n++
			}
		}
		if n == 0 {
			return "nothing changed yet"
		}
		return fmt.Sprintf("%d change(s) to save", n)
	}
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
