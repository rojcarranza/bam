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

func pickerItems() []app.TargetInfo {
	return []app.TargetInfo{
		{Name: "deploy-qa", Plan: "PROJ-DEPLOY", Branch: "develop", Files: []string{"/r/.bam/presets.yaml"},
			Defaults: []app.TargetVar{{Name: "env", Value: "qa"}, {Name: "api_token", Value: "s3cret", Secret: true}},
			Required: []string{"cluster"}},
		{Name: "provision-lab", Plan: "OPS-PROV", Files: []string{"/m/a.yaml", "/r/lab.yaml"}},
		{Name: "smoke", Plan: "PROJ-SMOKE", Branch: "main", Files: []string{"/r/presets.yaml"}},
	}
}

func press(m pickerModel, k tea.KeyMsg) pickerModel {
	n, _ := m.Update(k)
	return n.(pickerModel)
}

func typed(m pickerModel, s string) pickerModel {
	for _, r := range s {
		m = press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return m
}

func names(m pickerModel) []string {
	var out []string
	for _, i := range m.list.visible {
		out = append(out, m.list.items[i].Name)
	}
	return out
}

// Typing filters on the name, the plan, the branch and the file, in any case.
func TestPickerFiltersOnAnyPart(t *testing.T) {
	for q, want := range map[string][]string{
		"SMOKE": {"smoke"}, "ops-p": {"provision-lab"}, "develop": {"deploy-qa"},
		"lab.yaml": {"provision-lab"}, "· ma": {"smoke"}, "nope": nil,
	} {
		assert.Equal(t, want, names(typed(newPicker(pickerItems()), q)), q)
	}
}

// The preview shows the highlighted preset's variables, and never a secret.
func TestPickerPreviewMasksSecrets(t *testing.T) {
	v := newPicker(pickerItems()).View()
	assert.Contains(t, v, "> deploy-qa")
	assert.Contains(t, v, "PROJ-DEPLOY · develop")
	assert.Contains(t, v, "sets: env=qa  api_token=********  cluster (required)")
	assert.NotContains(t, v, "s3cret")
	m := press(newPicker(pickerItems()), tea.KeyMsg{Type: tea.KeyDown})
	assert.Contains(t, m.View(), "> provision-lab")
	assert.Contains(t, m.View(), "lab.yaml")
	assert.Contains(t, m.View(), "sets: nothing")
}

func TestPickerEnterChoosesAndEscChoosesNothing(t *testing.T) {
	m := press(typed(newPicker(pickerItems()), "smo"), tea.KeyMsg{Type: tea.KeyEnter})
	assert.Equal(t, "smoke", m.chosen)
	assert.Empty(t, m.View(), "nothing stays on screen")

	m = press(newPicker(pickerItems()), tea.KeyMsg{Type: tea.KeyEsc})
	assert.Empty(t, m.chosen)
	assert.True(t, m.done)

	m = press(typed(newPicker(pickerItems()), "zzz"), tea.KeyMsg{Type: tea.KeyEnter})
	assert.False(t, m.done, "Enter on no match runs nothing and keeps asking")
	assert.Contains(t, m.View(), "no preset matches")
}

// The real program, fed keys on a pipe: Enter returns the name, Esc cancels.
func TestPickPresetRunsInline(t *testing.T) {
	run := func(keys string) (string, string, error) {
		var out bytes.Buffer
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		name, err := PickPreset(ctx, strings.NewReader(keys), &out, pickerItems(), RunWords)
		return name, out.String(), err
	}
	name, out, err := run("smo\r")
	require.NoError(t, err)
	assert.Equal(t, "smoke", name)
	assert.NotContains(t, out, "\x1b[?1049h", "no alternate screen")

	_, _, err = run("\x1b")
	assert.ErrorIs(t, err, context.Canceled)
}

// Bare bam preset rm words the picker for removing, so Enter does not read as run.
func TestPickerRemoveWords(t *testing.T) {
	m := newPicker(pickerItems())
	m.words = RemoveWords
	v := m.View()
	assert.Contains(t, v, "remove which preset?")
	assert.Contains(t, v, "enter picks")
	assert.NotContains(t, v, "enter runs")
}
