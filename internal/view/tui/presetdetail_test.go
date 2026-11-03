package tui

import (
	"errors"
	"os/exec"
	"testing"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/stretchr/testify/require"
)

func presetDetailModel() Model {
	m := presetModel()
	for i := range m.presets.items {
		m.presets.items[i].Files = []string{"/home/jdoe/.config/bam/presets/proj.yaml"}
	}
	m.presets.refilter()
	return m
}

func TestEnterOnAPresetShowsWhatItSendsAndItsFile(t *testing.T) {
	m := presetDetailModel()
	t0, _ := m.presets.selected()
	t0.Defaults = []app.TargetVar{{Name: "cluster", Value: "dcos"}, {Name: "db_password", Value: "hunter2", Secret: true}}
	m.presets.items[m.presets.cursor] = t0
	m.presets.refilter()
	m, cmd := send(m, mkKey("enter"))
	require.Equal(t, overlayPreset, m.overlay)
	require.Equal(t, screenHome, m.screen, "enter no longer opens the plan")
	require.Nil(t, cmd)
	body := m.presetBody(70)
	require.Contains(t, body, "cluster = dcos")
	require.Contains(t, body, "db_password = ********")
	require.NotContains(t, body, "hunter2")
	require.Contains(t, body, "/home/jdoe/.config/bam/presets/proj.yaml")
}

func TestEnterInThePresetDetailOpensThePlan(t *testing.T) {
	m, _ := send(presetDetailModel(), mkKey("enter"))
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, overlayNone, m.overlay)
	m, _ = send(m, mkKey("enter"))
	m, cmd := send(m, mkKey("enter"))
	require.Equal(t, screenPlan, m.screen)
	require.NotNil(t, cmd)
}

func TestEEditsThePresetFileAndReloadsAfter(t *testing.T) {
	m := presetDetailModel()
	var got string
	m.deps.EditorCmd = func(path string) (*exec.Cmd, error) { got = path; return exec.Command("true"), nil }
	reloaded := 0
	m.deps.Targets = func() ([]app.TargetInfo, error) { reloaded++; return m.presets.items, nil }
	m, cmd := send(m, mkKey("E"))
	require.NotNil(t, cmd, "the editor runs as a process the UI hands the terminal to")
	require.Equal(t, "/home/jdoe/.config/bam/presets/proj.yaml", got)

	m, cmd = send(m, presetEditedMsg{Name: "x"})
	require.NotNil(t, cmd)
	_, _ = send(m, cmd())
	require.Equal(t, 1, reloaded)
}

func TestEditorFailureIsShownNotSwallowed(t *testing.T) {
	m := presetDetailModel()
	m, _ = send(m, presetEditedMsg{Name: "x", Err: errors.New("exit status 1")})
	require.Error(t, m.err)
}

func TestEFromTheDetailOverlayEditsToo(t *testing.T) {
	m := presetDetailModel()
	m.deps.EditorCmd = func(string) (*exec.Cmd, error) { return exec.Command("true"), nil }
	m, _ = send(m, mkKey("enter"))
	m, cmd := send(m, mkKey("E"))
	require.Equal(t, overlayNone, m.overlay)
	require.NotNil(t, cmd)
}

func TestDAsksBeforeRemovingAndNoKeepsThePreset(t *testing.T) {
	m := presetDetailModel()
	var removed []string
	m.deps.RemovePreset = func(n string) ([]string, error) { removed = append(removed, n); return nil, nil }
	m, cmd := send(m, mkKey("D"))
	require.Equal(t, overlayConfirm, m.overlay)
	require.Nil(t, cmd)
	require.Contains(t, m.confirm.Prompt, "/home/jdoe/.config/bam/presets/proj.yaml")
	m, _ = send(m, mkKey("n"))
	require.Equal(t, overlayNone, m.overlay)
	require.Empty(t, removed)
}

func TestYesRemovesThePresetAndReloads(t *testing.T) {
	m := presetDetailModel()
	name := func() string { t, _ := m.presets.selected(); return t.Name }()
	var removed []string
	m.deps.RemovePreset = func(n string) ([]string, error) {
		removed = append(removed, n)
		return []string{"/home/jdoe/.config/bam/presets/proj.yaml"}, nil
	}
	m.deps.Targets = func() ([]app.TargetInfo, error) { return nil, nil }
	m, _ = send(m, mkKey("D"))
	m, cmd := send(m, mkKey("y"))
	require.NotNil(t, cmd)
	m, cmd = send(m, cmd())
	require.Equal(t, []string{name}, removed)
	require.Contains(t, m.status, "removed preset "+name)
	m, _ = send(m, cmd())
	require.Empty(t, m.presets.items)
}

func TestRemoveFailureShowsTheError(t *testing.T) {
	m := presetDetailModel()
	m.deps.RemovePreset = func(string) ([]string, error) { return nil, errors.New("preset is used by set nightly") }
	m, _ = send(m, mkKey("D"))
	m, cmd := send(m, mkKey("y"))
	m, _ = send(m, cmd())
	require.Error(t, m.err)
}
