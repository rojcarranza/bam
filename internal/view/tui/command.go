package tui

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/errs"
)

// command is one entry of the : bar (home spec §5). The table below is the
// only definition: the prompt, completion and the help overlay all read it.
type command struct {
	name  string
	alias string // another exact name, for one a prefix cannot reach
	arg   string // what the argument is, or "" for none: project, server, column
	desc  string
	run   func(m Model, arg string) (tea.Model, tea.Cmd, error)
	// optional: the argument may be left out
	optional bool
}

func commands() []command {
	return []command{
		{name: "plans", desc: "the plans table", run: func(m Model, _ string) (tea.Model, tea.Cmd, error) {
			next, cmd := m.showPlans()
			return next, cmd, nil
		}},
		{name: "presets", desc: "the presets table", run: func(m Model, _ string) (tea.Model, tea.Cmd, error) {
			next, cmd := m.showPresets()
			return next, cmd, nil
		}},
		{name: "sets", desc: "the sets table", run: func(m Model, _ string) (tea.Model, tea.Cmd, error) {
			next, cmd := m.showSets()
			return next, cmd, nil
		}},
		{name: "projects", alias: "proj", desc: "the projects table; enter narrows the plans to one", run: func(m Model, _ string) (tea.Model, tea.Cmd, error) {
			next, cmd := m.showProjects()
			return next, cmd, nil
		}},
		{name: "project", arg: "project", desc: "filter by project, or all", run: Model.commandProject},
		{name: "server", arg: "server", desc: "switch server", run: Model.commandServer},
		{name: "sort", arg: "column", desc: "sort the table; -column reverses", run: Model.commandSort},
		{name: "tree", arg: "build", optional: true, desc: "the build tree of a build, or of the selected build", run: Model.commandTree},
		{name: "quit", desc: "quit", run: func(m Model, _ string) (tea.Model, tea.Cmd, error) {
			next, cmd := m.quit()
			return next, cmd, nil
		}},
	}
}

func commandNames() []string {
	var out []string
	for _, c := range commands() {
		out = append(out, c.name)
	}
	return out
}

// findCommand matches a name exactly, else by a unique prefix, in any case.
func findCommand(name string) (command, error) {
	name = strings.ToLower(name)
	var hits []command
	for _, c := range commands() {
		if c.name == name || (c.alias != "" && c.alias == name) {
			return c, nil
		}
		if strings.HasPrefix(c.name, name) {
			hits = append(hits, c)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return command{}, fmt.Errorf("unknown command %q; %scommands: %s", name, didYouMean(":", name, commandNames()), strings.Join(commandNames(), " "))
	}
	var names []string
	for _, c := range hits {
		names = append(names, c.name)
	}
	return command{}, fmt.Errorf("ambiguous command %q: %s", name, strings.Join(names, ", "))
}

// didYouMean is the typo hint for a : bar error, "" when nothing is close.
func didYouMean(prefix, input string, candidates []string) string {
	if c := app.Closest(input, candidates, 1); len(c) > 0 {
		return "did you mean " + prefix + c[0] + "? "
	}
	return ""
}

// startCommand opens the : prompt on Home or the panels.
func (m Model) startCommand() (tea.Model, tea.Cmd) {
	if m.screen != screenHome && m.screen != screenPlan {
		return m, nil
	}
	m.clearCmdErr()
	m.inputFor = inputCommand
	m.input.SetValue("")
	m.input.Prompt = ":"
	m.input.Focus()
	return m, textinput.Blink
}

// runCommandLine runs what was typed after :. A mistake goes to the status
// bar and changes nothing else.
func (m Model) runCommandLine(line string) (tea.Model, tea.Cmd) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return m, nil
	}
	m.clearCmdErr()
	c, err := findCommand(fields[0])
	if err == nil {
		arg := strings.Join(fields[1:], " ")
		switch {
		case c.arg != "" && arg == "" && !c.optional:
			err = fmt.Errorf(":%s needs a %s", c.name, c.arg)
			if choices := m.argChoices(c); len(choices) > 0 {
				err = fmt.Errorf("%w; %ss: %s", err, c.arg, strings.Join(choices, " "))
			}
		case c.arg == "" && arg != "":
			err = fmt.Errorf(":%s takes no argument", c.name)
		default:
			next, cmd, runErr := c.run(m, arg)
			if runErr == nil {
				return next, cmd
			}
			err = runErr
		}
	}
	m.err = errs.Usagef("%s", err.Error())
	m.cmdErr, m.home.plansErr = true, false
	return m, nil
}

// clearCmdErr drops an error the : bar itself set, so a mistyped command
// does not linger after the next one. Errors from anywhere else stay.
func (m *Model) clearCmdErr() {
	if m.cmdErr {
		m.err, m.cmdErr = nil, false
	}
}

func (m Model) commandProject(arg string) (tea.Model, tea.Cmd, error) {
	if strings.EqualFold(arg, "all") {
		next, cmd := m.setProject("")
		return next, cmd, nil
	}
	// Only a configured project list is a validation list. projectChoices
	// falls back to the projects seen in the loaded plans when none is
	// configured, and that is completion's job, not a reason to reject a
	// project the user knows about but the UI has not happened to load yet.
	if m.svc != nil {
		if known := m.svc.ProjectKeys(); len(known) > 0 && !slices.Contains(known, arg) {
			return m, nil, fmt.Errorf("project %q is not listed; %sprojects: %s", arg, didYouMean(":project ", arg, known), strings.Join(known, " "))
		}
	}
	next, cmd := m.setProject(arg)
	return next, cmd, nil
}

func (m Model) commandServer(arg string) (tea.Model, tea.Cmd, error) {
	var aliases []string
	for _, s := range m.deps.Servers {
		aliases = append(aliases, s.Alias)
	}
	if !slices.Contains(aliases, arg) {
		return m, nil, fmt.Errorf("no server %q; %sservers: %s", arg, didYouMean(":server ", arg, aliases), strings.Join(aliases, " "))
	}
	if arg == m.server {
		return m, nil, nil
	}
	next, cmd := m.switchServer(arg)
	return next, cmd, nil
}

func (m Model) commandSort(arg string) (tea.Model, tea.Cmd, error) {
	s, err := parseSort(m.home.view, arg)
	if err != nil {
		return m, nil, err
	}
	m.setHomeSort(s)
	return m, nil, nil
}

// projectChoices is the configured projects, or the projects of the loaded
// plans when none is configured.
func (m Model) projectChoices() []string {
	if m.svc != nil {
		if keys := m.svc.ProjectKeys(); len(keys) > 0 {
			return append([]string(nil), keys...)
		}
	}
	seen := map[string]bool{}
	var out []string
	for _, p := range m.plans.items {
		if p.ProjectKey != "" && !seen[p.ProjectKey] {
			seen[p.ProjectKey] = true
			out = append(out, p.ProjectKey)
		}
	}
	sort.Strings(out)
	return out
}

// complete is the candidates for what is typed: command names while the
// first word is typed, then that command's argument values.
func (m Model) complete(line string) []string {
	name, arg, spaced := strings.Cut(line, " ")
	if !spaced {
		var out []string
		for _, c := range commands() {
			// An alias names one command exactly; as a prefix it would also
			// reach another (proj is :projects, not the start of :project).
			if c.alias != "" && c.alias == name {
				return []string{c.name}
			}
		}
		for _, c := range commands() {
			if strings.HasPrefix(c.name, name) {
				out = append(out, c.name)
			}
		}
		return out
	}
	c, err := findCommand(name)
	if err != nil {
		return nil
	}
	values := m.argChoices(c)
	if c.arg == "column" {
		for _, n := range homeColNames[m.home.view] {
			values = append(values, "-"+n)
		}
	}
	var out []string
	for _, v := range values {
		if strings.HasPrefix(v, strings.TrimSpace(arg)) {
			out = append(out, c.name+" "+v)
		}
	}
	return out
}

// argChoices is the values a command's argument takes, for completion and
// for the hint when the argument is left out.
func (m Model) argChoices(c command) []string {
	var values []string
	switch c.arg {
	case "project":
		values = append(m.projectChoices(), "all")
	case "server":
		for _, s := range m.deps.Servers {
			values = append(values, s.Alias)
		}
	case "column":
		values = append(values, homeColNames[m.home.view]...)
	}
	return values
}

// completeInput is tab in the prompt: take the only candidate, or the
// longest prefix they share.
func (m Model) completeInput() Model {
	cands := m.complete(m.input.Value())
	switch len(cands) {
	case 0:
		return m
	case 1:
		v := cands[0]
		if c, err := findCommand(v); err == nil && c.arg != "" && !strings.Contains(v, " ") {
			v += " "
		}
		m.input.SetValue(v)
	default:
		m.input.SetValue(commonPrefix(cands))
	}
	m.input.CursorEnd()
	return m
}

// commonPrefix is the longest prefix the candidates share, cut on a rune
// boundary so tab never leaves half a multi-byte rune in the prompt.
func commonPrefix(ss []string) string {
	p := ss[0]
	for _, s := range ss[1:] {
		for !strings.HasPrefix(s, p) {
			_, size := utf8.DecodeLastRuneInString(p)
			p = p[:len(p)-size]
		}
	}
	return p
}

// commandHelpRows is the COMMANDS section of the help overlay.
func commandHelpRows() []helpRow {
	var out []helpRow
	for _, c := range commands() {
		k := ":" + c.name
		if c.arg != "" {
			k += " " + strings.ToUpper(c.arg)
		}
		out = append(out, helpRow{Group: "CMD", Keys: k, Desc: c.desc})
	}
	return out
}

func (m Model) commandTree(arg string) (tea.Model, tea.Cmd, error) {
	key := strings.ToUpper(strings.TrimSpace(arg))
	if key == "" {
		b, ok := m.currentBuild()
		if !ok {
			return m, nil, fmt.Errorf("no build selected; :tree KEY")
		}
		key = b.Key
	}
	next, cmd := m.openTree(key)
	return next, cmd, nil
}
