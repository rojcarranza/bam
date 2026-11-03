package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"
	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/errs"
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

// repoRow is one repository branch of the run, the form's --repo-branch: a
// preset's repo_branches entry or a row added with b.
type repoRow struct {
	Plan   string // "" applies to every plan of the run whose default repository is Repo
	Repo   string
	Branch string
	Added  bool   // added in the form with b; only these can be removed
	Err    string // shown against this row
}

// Key is the row as repo_branches and --repo-branch write it.
func (r repoRow) Key() string {
	if r.Plan == "" {
		return r.Repo
	}
	return r.Plan + ":" + r.Repo
}

// repoDry is what d shows for the repository branches: the plan branches the
// run would create or reuse, read from Bamboo and nothing written.
type repoDry struct {
	loading bool
	plan    app.RepoPlan
	err     error
}

// formState is the run form: what was fetched once, and what the user has
// done to it since.
type formState struct {
	ref          app.PlanRef
	target       string // the preset's name; empty for a bare plan
	base         app.VarSet
	fields       []formField
	revision     string              // commit to build; empty builds the newest. Not a variable.
	fromBuild    string              // build the form was prefilled from; empty for none
	fromRevision string              // its commit, offered on the Revision row by f
	repos        []repoRow           // the repository branches, after the variables
	choices      map[string][]string // Bamboo's branches by plan key; "" is the run's plan
	pick         int                 // the suggestion the last up or down put in the input
	dry          repoDry
	cursor       int
	editing      bool
	naming       bool // the input holds a new row's name, not a value
	namingRepo   bool // the name being typed is a repository branch's, REPO or PLAN:REPO
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
	var options map[string][]string
	if t := ref.Target; t != nil {
		for _, n := range t.Required {
			required[n] = true
		}
		options = t.Options
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
func (f formState) onRevision() bool { return f.cursor == len(f.fields)+len(f.repos) }

// onField says whether the cursor is on a variable; onRepo, on a repository
// branch, which sit between the variables and the Revision row.
func (f formState) onField() bool { return f.cursor < len(f.fields) }
func (f formState) onRepo() bool {
	return f.cursor >= len(f.fields) && f.cursor < len(f.fields)+len(f.repos)
}

// repoFlags is the rows in --repo-branch shape, which is how the run reads
// them: the entries for every plan first, as a preset lists them, so the
// first names the plan branches.
func (f formState) repoFlags() []string {
	var all, one []string
	for _, r := range f.repos {
		if r.Plan == "" {
			all = append(all, r.Key()+"="+r.Branch)
		} else {
			one = append(one, r.Key()+"="+r.Branch)
		}
	}
	return append(all, one...)
}

// suggestions are Bamboo's branches for the row under the cursor: those of
// the plan it names, else of the run's plan.
func (f formState) suggestions() []string {
	if !f.onRepo() {
		return nil
	}
	return f.choices[f.repos[f.cursor-len(f.fields)].Plan]
}

// presetRepoRows are a preset's repo_branches as form rows.
func presetRepoRows(ref app.PlanRef) []repoRow {
	if ref.Target == nil {
		return nil
	}
	var out []repoRow
	for _, r := range ref.Target.RepoBranchList() {
		out = append(out, repoRow{Plan: r.Plan, Repo: r.Repository, Branch: r.Branch})
	}
	return out
}

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
		if m.form.err != nil {
			return errorStyle.Render("could not load the plan's variables")
		}
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
	for i, r := range m.form.repos {
		here := m.form.cursor == len(m.form.fields)+i && !m.form.naming
		value := r.Branch
		if value == "" {
			value = dimStyle.Render(strings.Repeat("_", min(valueW, 12)))
		}
		note := "repository branch, " + map[bool]string{false: "preset", true: "added"}[r.Added]
		if m.form.editing && here {
			value = m.form.input.View()
			if n := len(m.form.suggestions()); n > 0 {
				note = fmt.Sprintf("up/down: pick from %d branches of Bamboo", n)
			}
		}
		showErr := r.Err != "" && !(m.form.editing && here)
		if showErr {
			note = r.Err
		}
		line := m.cursorFor(true, here) +
			lipgloss.NewStyle().Width(nameW).Render(truncate(r.Key(), nameW)) +
			lipgloss.NewStyle().Width(valueW).Render(truncate(value, valueW))
		if showErr {
			line += errorStyle.Render(truncate(note, width-nameW-valueW-4))
		} else {
			line += dimStyle.Render(truncate(note, width-nameW-valueW-4))
		}
		lines = append(lines, strings.TrimRight(line, " "))
	}
	if m.form.naming {
		hint := "name of the variable to add"
		if m.form.namingRepo {
			hint = "REPO, PLAN:REPO or REPO=BRANCH"
		}
		line := m.cursorFor(true, true) +
			lipgloss.NewStyle().Width(nameW).Render(m.form.input.View()) +
			dimStyle.Render(truncate(hint, width-nameW-4))
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

	count := fmt.Sprintf("%d of %d changed from the plan's values", m.changedCount(), len(m.form.fields))
	if n := len(m.form.repos); n > 0 {
		count += fmt.Sprintf(" · %d repository branches", n)
	}
	lines = append(lines, "", dimStyle.Render(count))
	if m.form.err != nil {
		// Wrapped, not cut: a Bamboo refusal ends with the reason.
		wrapped := errorStyle.Render(lipgloss.NewStyle().Width(width).Render(errorWhat(m.form.err)))
		lines = append(append(lines, ""), strings.Split(wrapped, "\n")...)
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
	if !f.onField() {
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
	rows := len(f.fields) + len(f.repos) + 1
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
	if f.onRepo() {
		f.input.SetValue(f.repos[f.cursor-len(f.fields)].Branch)
		f.input.CursorEnd()
		f.input.Focus()
		f.pick = -1
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
	switch {
	case f.onRevision():
		f.revision = f.input.Value()
	case f.onRepo():
		f.repos[f.cursor-len(f.fields)].Branch = strings.TrimSpace(f.input.Value())
	default:
		f.fields[f.cursor].Value = f.input.Value()
		f.fields[f.cursor].Touched = true
	}
	f.editing = false
	f.input.Blur()
}

func (f *formState) cancelEdit() {
	f.editing, f.naming, f.namingRepo = false, false, false
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

// startNamingRepo opens the input for a repository branch: REPO or
// PLAN:REPO, or REPO=BRANCH to give the branch at once.
func (f *formState) startNamingRepo() {
	f.startNaming()
	f.namingRepo = true
}

// acceptRepo ends naming a repository branch. A key already in the form
// moves the cursor to its row; a new one adds a row after the others. It
// says whether the branch should be typed next, which it is unless
// REPO=BRANCH gave it.
func (f *formState) acceptRepo() (typeBranch bool, err error) {
	text := strings.TrimSpace(f.input.Value())
	f.cancelEdit()
	if text == "" {
		return false, nil
	}
	key, branch, _ := strings.Cut(text, "=")
	rk, perr := app.ParseRepoBranchFlag(strings.TrimSpace(key) + "=x")
	if perr != nil || strings.ContainsAny(rk.Repository, " \t") {
		return false, errs.Usagef("%q is not REPO or PLAN:REPO", text).
			WithWhy("a repository is named as a build of the plan shows it").
			WithTry("repo-a, or PROJ-CHILD:repo-a for one plan")
	}
	at := -1
	for i, r := range f.repos {
		if r.Plan == rk.Plan && r.Repo == rk.Repository {
			at = i
		}
	}
	if at < 0 {
		f.repos = append(f.repos, repoRow{Plan: rk.Plan, Repo: rk.Repository, Added: true})
		at = len(f.repos) - 1
	}
	if b := strings.TrimSpace(branch); b != "" {
		f.repos[at].Branch = b
	}
	f.cursor = len(f.fields) + at
	return strings.TrimSpace(branch) == "", nil
}

// suggest puts the next (or previous) of Bamboo's branches in the input, so
// a branch is picked with up and down instead of typed.
func (f *formState) suggest(delta int) {
	s := f.suggestions()
	if len(s) == 0 {
		return
	}
	f.pick = (f.pick + delta + len(s)) % len(s)
	f.input.SetValue(s[f.pick])
	f.input.CursorEnd()
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
	if f.onRepo() {
		at := f.cursor - len(f.fields)
		if !f.repos[at].Added {
			return false
		}
		f.repos = append(f.repos[:at], f.repos[at+1:]...)
		return true
	}
	if !f.onField() || !f.fields[f.cursor].Added {
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
	for i := range m.form.repos {
		m.form.repos[i].Err = ""
		if m.form.repos[i].Branch == "" {
			m.form.repos[i].Err = "branch required"
		}
	}

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
	for _, r := range m.form.repos {
		if r.Err != "" {
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
		return errorStyle.Render(lipgloss.NewStyle().Width(width).Render(errorWhat(err)))
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
		return strings.Join(append(lines, m.repoDryLines(width)...), "\n")
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
	return strings.Join(append(lines, m.repoDryLines(width)...), "\n")
}

// repoDryLines is the dry-run's repository branches: each plan branch the
// run would create or reuse, as bam run --dry-run lists them, then how to
// delete one, since the UI never does.
func (m Model) repoDryLines(width int) []string {
	if len(m.form.repos) == 0 {
		return nil
	}
	d := m.form.dry
	switch {
	case d.loading:
		return []string{"", dimStyle.Render("finding the plans " + m.form.ref.MasterKey + " triggers…")}
	case d.err != nil:
		return []string{"", errorStyle.Render(lipgloss.NewStyle().Width(width).Render(errorWhat(d.err)))}
	case len(d.plan.Plans) == 0:
		return nil
	}
	lines := []string{"", fmt.Sprintf("plan branch %s on %d plans", d.plan.Name, len(d.plan.Plans))}
	for _, bp := range d.plan.Plans {
		what := bp.Repository + "  " + bp.VCSBranch
		if bp.Repository == "" {
			what = "its own branch"
		}
		verb := "create"
		if bp.Existing {
			verb = "reuse " + bp.BranchKey
		}
		lines = append(lines, lipgloss.NewStyle().Width(width).Render(fmt.Sprintf("  %s  %s  %s", bp.PlanKey, what, verb)))
	}
	for _, w := range d.plan.Warnings {
		lines = append(lines, "", dimStyle.Render(lipgloss.NewStyle().Width(width).Render(w)))
	}
	note := "bam changes no plan and never deletes plan branches. To remove one, delete it on its plan's Branches page in Bamboo, or with DELETE /rest/api/latest/plan/<branch key>."
	return append(lines, "", dimStyle.Render(lipgloss.NewStyle().Width(width).Render(note)))
}
