package tui

import (
	"maps"
	"slices"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/errs"
)

// presetEditedMsg says the editor opened by E has closed.
type presetEditedMsg struct {
	Name string
	Err  error
}

// presetRemovedMsg says D removed a preset from Files.
type presetRemovedMsg struct {
	Name  string
	Files []string
}

// openPresetDetail is enter on the presets table: what the preset would send
// and the file that holds it. Panels 1 2 3 still open the plan screen.
func (m Model) openPresetDetail() (tea.Model, tea.Cmd) {
	if _, ok := m.presets.selected(); !ok {
		return m, nil
	}
	m.overlay = overlayPreset
	return m, nil
}

// presetFile is the file E opens: the highest-precedence file defining t.
func presetFile(t app.TargetInfo) string {
	if len(t.Files) == 0 {
		return ""
	}
	return t.Files[len(t.Files)-1]
}

// presetBody lists what running the preset sends, then where it lives.
// Secrets and ${ENV} values are masked by TargetVar.Display.
func (m Model) presetBody(width int) string {
	t, ok := m.presets.selected()
	if !ok {
		return "no preset selected"
	}
	wrap := lipgloss.NewStyle().Width(width)
	line := func(k, v string) string { return wrap.Render(dimStyle.Render(k+" ") + v) }
	branch := t.Branch
	if branch == "" {
		branch = "the plan's own"
	}
	lines := []string{
		line("preset ", t.Name),
		line("plan   ", t.Plan),
		line("branch ", branch),
	}
	for _, name := range slices.Sorted(maps.Keys(t.Repositories)) {
		lines = append(lines, line("repo   ", name+" → "+t.Repositories[name]))
	}
	lines = append(lines, "")
	if len(t.Defaults) == 0 {
		lines = append(lines, "no variables would be sent")
	} else {
		lines = append(lines, "would send")
		for _, v := range t.Defaults {
			lines = append(lines, wrap.Render("  "+v.Name+" = "+v.Display()))
		}
	}
	if len(t.Required) > 0 {
		lines = append(lines, "", line("asks for ", strings.Join(t.Required, ", ")))
	}
	lines = append(lines, "")
	switch files := t.Files; len(files) {
	case 0:
		lines = append(lines, line("file   ", strings.Join(t.DefinedIn, ", ")))
	default:
		for i := len(files) - 1; i >= 0; i-- {
			lines = append(lines, line("file   ", files[i]))
		}
	}
	lines = append(lines, "", wrap.Inherit(dimStyle).Render("enter opens the plan · E edit the file · D remove · esc closes"))
	return strings.Join(lines, "\n")
}

// E is keys.Pager: the same key, bound to the screen it is pressed on (the log
// screen pages the log, the presets table and its panel edit the file).

// handlePresetKey is the preset detail overlay's keys.
func (m Model) handlePresetKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Back):
		m.overlay = overlayNone
		return m, nil
	case key.Matches(msg, keys.Quit):
		return m.quit()
	case key.Matches(msg, keys.Enter):
		m.overlay = overlayNone
		return m.drillFromHome(focusBuilds)
	case key.Matches(msg, keys.Pager):
		m.overlay = overlayNone
		return m.editPreset()
	case key.Matches(msg, keys.RemovePreset):
		m.overlay = overlayNone
		return m.askRemovePreset()
	}
	return m, nil
}

// editPreset opens the selected preset's file in the editor. The UI is
// suspended while it runs, and the presets are re-read when it closes.
func (m Model) editPreset() (tea.Model, tea.Cmd) {
	t, ok := m.presets.selected()
	if !ok || m.deps.EditorCmd == nil {
		return m, nil
	}
	path := presetFile(t)
	if path == "" {
		m.err = errs.Usagef("preset %s has no file to edit", t.Name)
		return m, nil
	}
	cmd, err := m.deps.EditorCmd(path)
	if err != nil {
		m.err = err
		return m, nil
	}
	name := t.Name
	return m, tea.ExecProcess(cmd, func(err error) tea.Msg { return presetEditedMsg{Name: name, Err: err} })
}

// askRemovePreset names the preset and every file it will leave, then waits
// for the answer. Nothing is removed before yes.
func (m Model) askRemovePreset() (tea.Model, tea.Cmd) {
	t, ok := m.presets.selected()
	if !ok || m.deps.RemovePreset == nil {
		return m, nil
	}
	where := strings.Join(t.Files, ", ")
	if where == "" {
		where = strings.Join(t.DefinedIn, ", ")
	}
	name := t.Name
	remove := m.deps.RemovePreset
	return m.ask("Remove preset "+name+" from "+where+"?", func(m Model) (tea.Model, tea.Cmd) {
		m.status = "removing preset " + name + "…"
		return m, func() tea.Msg {
			files, err := remove(name)
			if err != nil {
				return errMsg{Err: err, Where: "preset"}
			}
			return presetRemovedMsg{Name: name, Files: files}
		}
	})
}

// handlePresetsTableKey is E and D on the presets table.
func (m Model) handlePresetsTableKey(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	switch {
	case key.Matches(msg, keys.Pager):
		next, cmd := m.editPreset()
		return next, cmd, true
	case key.Matches(msg, keys.RemovePreset):
		next, cmd := m.askRemovePreset()
		return next, cmd, true
	}
	return m, nil, false
}

// presetsChanged reloads the table after an edit or a removal.
func (m Model) presetsChanged() (tea.Model, tea.Cmd) {
	if m.deps.Targets == nil {
		return m, nil
	}
	m.presetsGen++
	return m, loadPresetsCmd(m.deps, m.presetsGen)
}
