package tui

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rojcarranza/bam/internal/app"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testForm() app.PresetForm {
	return app.PresetForm{Name: "deploy-qa", Plan: "PROJ-DEPLOY", Branch: "main", Path: "~/bam/presets/proj.yaml", Rows: []app.FormRow{
		{Name: "region", Value: "eu", Plan: "eu", Choices: []string{"eu"}},
		{Name: "target_env", Value: "staging", Plan: "qa", Choices: []string{"staging", "qa"}},
		{Name: "version", Plan: "", Choices: []string{""}},
		{Name: "deploy_token", Value: "${DEPLOY_TOKEN}", Secret: true, CanStore: true, Commented: true},
	}}
}

func formKey(m presetFormModel, ks ...tea.KeyMsg) presetFormModel {
	for _, k := range ks {
		n, _ := m.Update(k)
		m = n.(presetFormModel)
	}
	return m
}

func rk(s string) tea.KeyMsg      { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }
func kt(t tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: t} }

// Recent values arrive after the form is open and join the choices.
func TestPresetFormRecentValuesArrive(t *testing.T) {
	m := newPresetForm(context.Background(), testForm(), func(context.Context) []app.RecentVar { return nil })
	assert.Contains(t, m.View(), "reading recent builds")
	n, _ := m.Update(recentMsg{{Name: "target_env", Values: []string{"staging", "dev"}}})
	m = n.(presetFormModel)
	assert.NotContains(t, m.View(), "reading recent builds")
	assert.Equal(t, []string{"staging", "qa", "dev"}, m.form.Rows[1].Choices)
}

// Arrows step through the choices and wrap; enter types a value and moves on.
func TestPresetFormKeys(t *testing.T) {
	m := newPresetForm(context.Background(), testForm(), nil)
	m = formKey(m, kt(tea.KeyDown), kt(tea.KeyRight))
	assert.Equal(t, "qa", m.form.Rows[1].Value)
	m = formKey(m, kt(tea.KeyRight))
	assert.Equal(t, "staging", m.form.Rows[1].Value, "wraps")
	m = formKey(m, kt(tea.KeyLeft))
	assert.Equal(t, "qa", m.form.Rows[1].Value)

	m = formKey(m, kt(tea.KeyDown), kt(tea.KeyEnter), rk("1"), rk("."), rk("2"), kt(tea.KeyEnter))
	assert.Equal(t, "1.2", m.form.Rows[2].Value)
	assert.Equal(t, 3, m.cursor, "enter accepts and moves to the next row")

	m = formKey(m, kt(tea.KeyUp), kt(tea.KeyEnter), rk("x"), kt(tea.KeyEsc))
	assert.Equal(t, "1.2", m.form.Rows[2].Value, "esc leaves the value as it was")
	assert.False(t, m.done, "esc while typing does not leave the form")
}

// A secret row never shows or takes a value: arrows switch the stored value
// and ${ENV}, and enter edits only the variable name.
func TestPresetFormSecretRow(t *testing.T) {
	m := newPresetForm(context.Background(), testForm(), nil)
	m = formKey(m, kt(tea.KeyUp)) // clamped at the top
	m = formKey(m, kt(tea.KeyDown), kt(tea.KeyDown), kt(tea.KeyDown))
	assert.Contains(t, m.View(), "plan's stored value")
	m = formKey(m, kt(tea.KeyRight))
	assert.False(t, m.form.Rows[3].Commented)
	assert.Contains(t, m.View(), "${DEPLOY_TOKEN}")

	m = formKey(m, kt(tea.KeyEnter), kt(tea.KeyCtrlU))
	for _, r := range "qa_token" {
		m = formKey(m, rk(string(r)))
	}
	m = formKey(m, kt(tea.KeyEnter))
	assert.Equal(t, "${qa_token}", m.form.Rows[3].Value)

	m = formKey(m, kt(tea.KeyEnter), kt(tea.KeyCtrlU), rk("a"), rk("-"), rk("b"), kt(tea.KeyEnter))
	assert.Equal(t, "${qa_token}", m.form.Rows[3].Value, "a bad name is refused")
	assert.Contains(t, m.err, "environment variable name")
}

func TestPresetFormSaveAndCancel(t *testing.T) {
	m := formKey(newPresetForm(context.Background(), testForm(), nil), kt(tea.KeyCtrlS))
	assert.True(t, m.saved)
	assert.Empty(t, m.View(), "nothing stays on screen")
	for _, k := range []tea.KeyMsg{kt(tea.KeyEsc), kt(tea.KeyCtrlC)} {
		m = formKey(newPresetForm(context.Background(), testForm(), nil), k)
		assert.False(t, m.saved)
		assert.True(t, m.done)
	}
}

func TestPresetFormView(t *testing.T) {
	m := newPresetForm(context.Background(), testForm(), nil)
	m = formKey(m, kt(tea.KeyDown))
	requireGolden(t, "presetform", m.View())
}

// The real program, fed keys on a pipe: ctrl-s returns the chosen values and
// draws no alternate screen; Esc cancels.
func TestEditPresetFormRunsInline(t *testing.T) {
	run := func(keys string) (app.PresetForm, string, error) {
		var out bytes.Buffer
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		f, err := EditPresetForm(ctx, strings.NewReader(keys), &out, testForm(), nil)
		return f, out.String(), err
	}
	f, out, err := run("\x1b[B\x1b[C\x13")
	require.NoError(t, err)
	assert.Equal(t, "qa", f.Rows[1].Value)
	assert.NotContains(t, out, "\x1b[?1049h")

	_, _, err = run("\x03")
	assert.ErrorIs(t, err, context.Canceled)
}

func testEditForm() app.PresetForm {
	return app.PresetForm{Name: "deploy-qa", Plan: "PROJ-DEPLOY", Path: "~/bam/presets/proj.yaml", Editing: true,
		Rows: []app.FormRow{
			{Name: "ENV", Value: "qa", Orig: "qa", InPreset: true, Options: []string{"qa", "prod"}, OrigOptions: []string{"qa", "prod"}, Choices: []string{"qa", "prod"}},
			{Name: "REGION", Value: "eu", Orig: "eu", Plan: "eu", Choices: []string{"eu"}},
		},
		Fields: []app.FormField{
			{Key: "name", Label: "name", Value: "deploy-qa", Orig: "deploy-qa"},
			{Key: "timeout", Label: "timeout", Value: "", Orig: ""},
		}}
}

// x removes a saved variable and again restores it; o edits its options;
// enter on a setting types its text; a bad timeout keeps the form open.
func TestPresetEditFormKeys(t *testing.T) {
	m := newPresetForm(context.Background(), testEditForm(), nil)
	assert.Contains(t, m.View(), "edit preset deploy-qa")
	assert.Contains(t, m.View(), "nothing changed yet")

	m = formKey(m, rk("x"))
	assert.True(t, m.form.Rows[0].Removed)
	assert.Contains(t, m.View(), "removed: the plan's own value applies")
	m = formKey(m, rk("x"))
	assert.False(t, m.form.Rows[0].Removed)

	m = formKey(m, rk("o"))
	assert.True(t, m.editing)
	m = formKey(m, kt(tea.KeyEnd), rk(","), rk("d"), rk("e"), rk("v"), kt(tea.KeyEnter))
	assert.Equal(t, []string{"qa", "prod", "dev"}, m.form.Rows[0].Options)
	assert.Equal(t, []string{"qa", "prod", "dev"}, m.form.Rows[0].Choices)

	m = formKey(m, kt(tea.KeyDown), kt(tea.KeyDown), kt(tea.KeyDown), kt(tea.KeyEnter), rk("s"), rk("o"), kt(tea.KeyEnter))
	assert.Equal(t, "so", m.form.Fields[1].Value)
	m = formKey(m, kt(tea.KeyCtrlS))
	assert.False(t, m.saved, "a timeout that is no duration is refused while the form is open")
	assert.Contains(t, m.View(), "not a duration")

	m = formKey(m, kt(tea.KeyEnter), kt(tea.KeyBackspace), kt(tea.KeyBackspace), rk("4"), rk("5"), rk("m"), kt(tea.KeyEnter))
	m = formKey(m, kt(tea.KeyCtrlS))
	assert.True(t, m.saved)
	assert.Equal(t, "45m", m.form.Fields[1].Value)
}

// x and o are the edit form's; the add form types nothing on them.
func TestPresetAddFormIgnoresEditKeys(t *testing.T) {
	m := newPresetForm(context.Background(), testForm(), nil)
	m = formKey(m, rk("x"), rk("o"))
	assert.False(t, m.editing)
	assert.False(t, m.form.Rows[0].Removed)
}
