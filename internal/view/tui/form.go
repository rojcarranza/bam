package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"
	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/provider"
)

// formField is one variable in the run form.
type formField struct {
	Name     string
	Value    string
	Source   string // plan, target, from #8, env LAB_TOKEN, flag
	Secret   bool
	Required bool
	Options  []string // non-empty means a cycle widget, not free text
	Touched  bool     // the user typed or cycled this field
	Added    bool     // added in the form with a; only these can be removed
	Err      string   // from ValidateVars, shown against this field
}

// formState is the run form: what was fetched once, and what the user has
// done to it since.
type formState struct {
	ref          app.PlanRef
	target       string // the preset's name; empty for a bare plan
	base         app.VarSet
	fields       []formField
	revision     string // commit to build; empty builds the newest. Not a variable.
	fromBuild    string // build the form was prefilled from; empty for none
	fromRevision string // its commit, offered on the Revision row by f
	cursor       int
	editing      bool
	naming       bool // the input holds a new row's name, not a value
	input        textinput.Model
	err          error // a form-level error that ctrl-R refuses on
	loading      bool
}

// revisionReused says whether the Revision row holds the offered commit of
// the build the form came from, unchanged.
func (f formState) revisionReused() bool {
	return f.fromRevision != "" && f.revision == f.fromRevision
}

// planRef is what ctrl-R runs: the loaded plan with the Revision row.
func (f formState) planRef() app.PlanRef {
	ref := f.ref
	ref.Revision = f.revision
	if f.revisionReused() {
		ref.RevisionFrom = f.fromBuild
	}
	return ref
}

// buildFields turns a fetched VarSet into the form's rows, adding any name the
// target requires that the plan does not declare — otherwise a required
// variable would be invisible and impossible to satisfy.
func buildFields(base app.VarSet, ref app.PlanRef) []formField {
	required := map[string]bool{}
	options := map[string][]string{}
	if t := ref.Target; t != nil {
		for _, n := range t.Required {
			required[n] = true
		}
		for n, vals := range t.Options {
			options[n] = vals
		}
	}

	seen := map[string]bool{}
	out := make([]formField, 0, len(base.Vars)+len(required))
	add := func(name, value, source string, secret bool) {
		if seen[name] {
			return
		}
		seen[name] = true
		// A secret is never prefilled: what is on screen is then never a
		// value the user did not type.
		if secret {
			value = ""
		}
		out = append(out, formField{
			Name: name, Value: value, Source: source, Secret: secret,
			Required: required[name], Options: options[name],
		})
	}
	for _, v := range base.Vars {
		add(v.Name, v.Value, v.Source, v.Secret)
	}
	for name := range required {
		// A required name the plan does not declare still has to obey the
		// name heuristic: db_password must arrive masked, not echoing.
		add(name, "", "", app.IsSecretName(name))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// onRevision says whether the cursor is on the Revision row, the last stop
// after the variables.
func (f formState) onRevision() bool { return f.cursor == len(f.fields) }

// flags is what the form hands ValidateVars, in --var shape.
//
// Only a field the user typed or cycled is sent. Every untouched value
// already sits in the base that ValidateVars is given, so resending it would
// change nothing and would raise a spurious undeclared-name warning for a
// target default the plan does not declare. An untouched secret therefore
// sends nothing, and the plan's or the environment's value stands.
func (f formState) flags() []string {
	var out []string
	for _, fl := range f.fields {
		if !fl.Touched {
			continue
		}
		out = append(out, fl.Name+"="+fl.Value)
	}
	return out
}

// changedCount is the footer's count: how many values differ from the plan's,
// because only those are sent to Bamboo.
//
// It counts the set the trigger would actually send, not the fields the user
// touched. Typing a value back to the plan's own is not a change, and a
// preset's untouched default is one.
func (m Model) changedCount() int {
	getenv := m.deps.Getenv
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	vs, err := app.ValidateVars(m.form.ref, m.form.base, m.form.flags(), getenv)
	if err != nil {
		// Nothing can be sent while a rule is broken, so the honest count is
		// what the user has touched.
		n := 0
		for _, fl := range m.form.fields {
			if fl.Touched {
				n++
			}
		}
		return n
	}
	return len(m.form.stripUntypedMasks(vs).Changed())
}

// formView is the whole terminal: the plan's name, what will run, and the
// variables in the standard panel frame.
func (m Model) formView() string {
	ref := m.form.ref
	head := []string{}
	if m.height >= 20 {
		what := ref.PlanKey
		if ref.Branch != "" {
			what += " · branch " + ref.Branch
		}
		if m.form.target != "" {
			what += " · preset " + m.form.target
		}
		head = append(head, m.nameLine(m.width, m.masterOf(ref.PlanKey)), dimStyle.Render(" "+cut(what, m.width-1)))
	}
	title := "Variables"
	if len(head) == 0 {
		// A small terminal keeps the old title, so the plan is still named.
		title = "Run " + ref.PlanKey
		if m.form.target != "" {
			title = "Run " + m.form.target + " · " + ref.PlanKey
		}
		if ref.Branch != "" {
			title += " · " + ref.Branch
		}
	}
	body := m.height - 1 - len(head)
	parts := append(head, panel(title, true, m.width, body, m.formBody(m.width-2, body-2)), m.statusBar(m.width))
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// formBody is three columns: the name, the value, and the source or the
// constraint. A secret's value column is always ********, whatever the field
// holds, so a typed secret cannot reach the screen.
func (m Model) formBody(width, height int) string {
	if m.form.loading {
		return dimStyle.Render("loading the plan's variables…")
	}
	nameW := formNameWidth(width)
	valueW := formValueWidth(width)

	lines := []string{""}
	for i, f := range m.form.fields {
		value := f.Value
		switch {
		case f.Secret:
			value = app.MaskedDisplay
		case value == "":
			value = dimStyle.Render(strings.Repeat("_", min(valueW, 12)))
		}
		here := m.form.cursor == i && !m.form.naming
		if m.form.editing && here {
			value = m.form.input.View()
		}
		line := m.cursorFor(true, here) +
			lipgloss.NewStyle().Width(nameW).Render(truncate(f.Name, nameW)) +
			lipgloss.NewStyle().Width(valueW).Render(truncate(value, valueW)) +
			m.fieldNote(f, width-nameW-valueW-4)
		lines = append(lines, strings.TrimRight(line, " "))
	}
	if m.form.naming {
		line := m.cursorFor(true, true) +
			lipgloss.NewStyle().Width(nameW).Render(m.form.input.View()) +
			dimStyle.Render(truncate("name of the variable to add", width-nameW-4))
		lines = append(lines, strings.TrimRight(line, " "))
	}

	rev, note := m.form.revision, "commit to build"
	switch {
	case rev == "" && m.form.fromRevision != "":
		rev = dimStyle.Render("newest")
		note = app.BuildLabel(m.form.fromBuild) + " built " + provider.Revision{Revision: m.form.fromRevision}.Short()
	case rev == "":
		rev = dimStyle.Render("newest")
	case m.form.revisionReused():
		rev = provider.Revision{Revision: rev}.Short()
		note = "from " + app.BuildLabel(m.form.fromBuild)
	}
	onRev := m.form.onRevision() && !m.form.naming
	if m.form.editing && onRev {
		rev = m.form.input.View()
	}
	revLine := m.cursorFor(true, onRev) +
		lipgloss.NewStyle().Width(nameW).Render("revision") +
		lipgloss.NewStyle().Width(valueW).Render(truncate(rev, valueW)) +
		dimStyle.Render(truncate(note, width-nameW-valueW-4))
	lines = append(lines, strings.TrimRight(revLine, " "))

	lines = append(lines, "", dimStyle.Render(fmt.Sprintf("%d of %d changed from the plan's values",
		m.changedCount(), len(m.form.fields))))
	if m.form.err != nil {
		lines = append(lines, "", errorStyle.Render(truncate(errorWhat(m.form.err), width)))
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	return strings.Join(lines, "\n")
}

// fieldNote is the third column: this field's own error, else its constraint,
// else where its value came from.
func (m Model) fieldNote(f formField, width int) string {
	switch {
	case f.Err != "":
		return errorStyle.Render(truncate(f.Err, width))
	case len(f.Options) > 0:
		return dimStyle.Render(truncate(strings.Join(f.Options, " | "), width))
	case f.Required && f.Value == "":
		return errorStyle.Render("required")
	case f.Required:
		return dimStyle.Render("required")
	}
	return dimStyle.Render(truncate(f.Source, width))
}

func formNameWidth(width int) int {
	w := width / 3
	switch {
	case w < 10:
		w = 10
	case w > 22:
		w = 22
	}
	return w
}

func formValueWidth(width int) int {
	w := width / 3
	if w < 10 {
		w = 10
	}
	return w
}

// cycle steps an options field to the next allowed value and wraps. It is how
// an options field is edited: free text is never accepted for one, so the
// commonest rejection cannot be typed.
func (f *formState) cycle(delta int) {
	if f.onRevision() {
		return
	}
	fl := &f.fields[f.cursor]
	if len(fl.Options) == 0 {
		return
	}
	at := 0
	for i, v := range fl.Options {
		if v == fl.Value {
			at = i
			break
		}
	}
	at = (at + delta + len(fl.Options)) % len(fl.Options)
	fl.Value, fl.Touched = fl.Options[at], true
}

// move walks the variables and the Revision row after them, and wraps.
func (f *formState) move(delta int) {
	rows := len(f.fields) + 1
	f.cursor = (f.cursor + delta + rows) % rows
}

// startEditing opens the text input on the current field, or on the Revision
// row. A secret echoes asterisks, so nothing typed reaches the screen.
func (f *formState) startEditing() {
	f.input = textinput.New()
	f.input.Prompt = ""
	if f.onRevision() {
		f.input.SetValue(f.revision)
		f.input.CursorEnd()
		f.input.Focus()
		f.editing = true
		return
	}
	fl := f.fields[f.cursor]
	f.input.SetValue(fl.Value)
	f.input.CursorEnd()
	if fl.Secret {
		f.input.EchoMode = textinput.EchoPassword
		f.input.EchoCharacter = '*'
		f.input.SetValue("")
	}
	f.input.Focus()
	f.editing = true
}

// acceptEdit takes what was typed. An empty value typed on purpose is a
// value, so Touched is set either way. The Revision row is not a field: it
// has no Touched flag and never counts in "N of M changed".
func (f *formState) acceptEdit() {
	if f.onRevision() {
		f.revision = f.input.Value()
	} else {
		f.fields[f.cursor].Value = f.input.Value()
		f.fields[f.cursor].Touched = true
	}
	f.editing = false
	f.input.Blur()
}

func (f *formState) cancelEdit() {
	f.editing, f.naming = false, false
	f.input.Blur()
}

// startNaming opens the input for the name of a row the plan may not
// declare, the form's --var NAME=value for an undeclared name.
func (f *formState) startNaming() {
	f.input = textinput.New()
	f.input.Prompt = ""
	f.input.Focus()
	f.editing, f.naming = true, true
}

// acceptName ends naming. A declared or already added name moves the cursor
// to that row rather than adding a duplicate; a new name adds a row after
// the others. It says whether the row's value should be typed next.
func (f *formState) acceptName() bool {
	name := strings.TrimSpace(f.input.Value())
	f.cancelEdit()
	if name == "" {
		return false
	}
	at := -1
	for i, fl := range f.fields {
		if fl.Name == name {
			at = i
		}
	}
	if at < 0 {
		source := "added"
		if f.base.DeclaredKnown {
			source = "not declared"
		}
		f.fields = append(f.fields, formField{Name: name, Source: source, Secret: app.IsSecretName(name), Added: true})
		at = len(f.fields) - 1
	}
	f.cursor = at
	return len(f.fields[at].Options) == 0
}

// removeAdded drops the row under the cursor if the form added it. A
// declared variable stays: removing it would not unset it on Bamboo.
func (f *formState) removeAdded() bool {
	if f.onRevision() || !f.fields[f.cursor].Added {
		return false
	}
	f.fields = append(f.fields[:f.cursor], f.fields[f.cursor+1:]...)
	return true
}

// revalidate runs the same rules bam run applies, against the fields as they
// stand. It makes no request: ValidateVars is pure, and the values it needs
// were fetched once when the form opened. A usage error naming a variable is
// shown against that field; anything else is a form-level error.
func (m *Model) revalidate() {
	for i := range m.form.fields {
		m.form.fields[i].Err = ""
	}
	m.form.err = nil

	getenv := m.deps.Getenv
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	_, err := app.ValidateVars(m.form.ref, m.form.base, m.form.flags(), getenv)
	if err == nil {
		return
	}
	if name, msg, ok := fieldError(err, m.form.fields); ok {
		for i := range m.form.fields {
			if m.form.fields[i].Name == name {
				m.form.fields[i].Err = msg
				return
			}
		}
	}
	m.form.err = err
}

// fieldError finds which field an error is about, so the mark lands on the
// row the reader is looking at rather than only in the footer.
func fieldError(err error, fields []formField) (string, string, bool) {
	what := errorWhat(err)
	for _, f := range fields {
		if f.Name != "" && strings.Contains(what, f.Name) {
			return f.Name, shortFieldError(what, f.Name), true
		}
	}
	return "", "", false
}

// shortFieldError keeps the third column narrow: the rule, not the sentence.
func shortFieldError(what, name string) string {
	switch {
	case strings.Contains(what, "is required"):
		return "required"
	case strings.Contains(what, "is not allowed"):
		return "not allowed"
	case strings.Contains(what, "needs environment variable"):
		return "unset ${ENV}"
	}
	return strings.TrimPrefix(what, name+" ")
}

// canRun says whether ctrl-R may send. A warning does not block, exactly as
// it does not block bam run.
func (m Model) canRun() bool {
	if m.form.err != nil {
		return false
	}
	for _, f := range m.form.fields {
		if f.Err != "" {
			return false
		}
	}
	return true
}

// stripUntypedMasks stops a value Bamboo returned as ******** from being sent
// straight back as the literal string ********, which would overwrite the
// real secret with asterisks.
//
// It deliberately strips nothing else. An untouched secret that the preset
// configures — a literal default, or a ${ENV} reference the target resolved —
// is still sent, because that is exactly what bam run sends for the same
// preset. Dropping it would make R on provision-lab and bam run provision-lab
// disagree, and would run the build with the plan's credentials instead of
// the preset's. The rule the form owes the user is that the empty box on
// screen never overwrites anything, and flags() is what keeps it: an
// untouched field contributes no flag, so the value underneath stands.
func (f formState) stripUntypedMasks(vs app.VarSet) app.VarSet {
	typed := map[string]bool{}
	for _, fl := range f.fields {
		if fl.Touched {
			typed[fl.Name] = true
		}
	}
	out := vs
	out.Vars = make([]app.ResolvedVar, len(vs.Vars))
	copy(out.Vars, vs.Vars)
	for i := range out.Vars {
		v := &out.Vars[i]
		if v.Value == app.MaskedDisplay && !typed[v.Name] {
			v.Value = v.PlanValue
		}
	}
	return out
}

// dryRunBody is exactly what a trigger would send, with secrets masked. It
// reaches no server: the values are already in hand.
func (m Model) dryRunBody(width int) string {
	if m.screen == screenSetVars {
		return strings.Join(m.setDry, "\n")
	}
	getenv := m.deps.Getenv
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	vs, err := app.ValidateVars(m.form.ref, m.form.base, m.form.flags(), getenv)
	if err != nil {
		return errorStyle.Render(truncate(errorWhat(err), width))
	}
	vs = m.form.stripUntypedMasks(vs)

	lines := []string{dimStyle.Render("plan " + m.form.ref.PlanKey)}
	if m.form.revision != "" {
		lines = append(lines, dimStyle.Render("revision "+m.form.revision))
	}
	changed := vs.Changed()
	secret := vs.Secret()
	if len(changed) == 0 {
		lines = append(lines, "", "no variables would be sent")
		return strings.Join(lines, "\n")
	}
	lines = append(lines, "")
	names := make([]string, 0, len(changed))
	for n := range changed {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		v := changed[n]
		if secret[n] {
			v = app.MaskedDisplay
		}
		lines = append(lines, truncate(n+"="+v, width))
	}
	return strings.Join(lines, "\n")
}
