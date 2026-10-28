package tui

import (
	"context"
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/stretchr/testify/require"
)

func TestPSavesTheSelectedBuildAsAPreset(t *testing.T) {
	m := cancelModel()
	var gotName, gotBuild string
	m.deps.SavePreset = func(_ context.Context, _ *app.Service, name, build string) (string, error) {
		gotName, gotBuild = name, build
		return "/home/jdoe/.config/bam/presets/proj.yaml", nil
	}
	m, _ = send(m, mkKey("p"))
	require.Equal(t, inputPreset, m.inputFor)
	require.Contains(t, m.input.Prompt, "PROJ-BUILD-44")

	m, _ = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("deploy-eu")})
	m, cmd := send(m, mkKey("enter"))
	require.Equal(t, inputNone, m.inputFor)
	require.NotNil(t, cmd)
	m, _ = send(m, cmd())
	require.Equal(t, "deploy-eu", gotName)
	require.Equal(t, "PROJ-BUILD-44", gotBuild)
	require.Equal(t, "added preset deploy-eu to /home/jdoe/.config/bam/presets/proj.yaml", m.status)
}

func TestASavedPresetShowsInThePresetsPanel(t *testing.T) {
	m := cancelModel()
	saved := false
	m.deps.SavePreset = func(context.Context, *app.Service, string, string) (string, error) {
		saved = true
		return "/home/jdoe/.config/bam/presets/proj.yaml", nil
	}
	m.deps.Targets = func() ([]app.TargetInfo, error) {
		if !saved {
			return nil, nil
		}
		return []app.TargetInfo{{Name: "deploy-eu", Plan: "PROJ-BUILD"}}, nil
	}
	m, _ = send(m, mkKey("p"))
	m, _ = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("deploy-eu")})
	m, cmd := send(m, mkKey("enter"))
	m, cmd = send(m, cmd())
	require.NotNil(t, cmd, "saving reloads the presets")
	m, _ = send(m, cmd())
	require.Len(t, m.presets.items, 1)
	require.Equal(t, "deploy-eu", m.presets.items[0].Name)
}

func TestSavePresetEmptyNameOrFailure(t *testing.T) {
	m := cancelModel()
	calls := 0
	m.deps.SavePreset = func(context.Context, *app.Service, string, string) (string, error) {
		calls++
		return "", errors.New("preset deploy-eu already exists")
	}
	m, _ = send(m, mkKey("p"))
	m, cmd := send(m, mkKey("enter"))
	require.Nil(t, cmd, "an empty name saves nothing")

	m, _ = send(m, mkKey("p"))
	m, _ = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("deploy-eu")})
	m, cmd = send(m, mkKey("enter"))
	m, _ = send(m, cmd())
	require.Equal(t, 1, calls)
	require.ErrorContains(t, m.err, "already exists")
}
