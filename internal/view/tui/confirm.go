package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// confirmState is a pending yes/no question. Action is what yes does; it is a
// function so the overlay knows nothing about what it is confirming.
type confirmState struct {
	Prompt string
	Action func(Model) (tea.Model, tea.Cmd)
}

// confirmBody names what yes will do, so the answer is never a guess. It
// wraps rather than cuts: the end of a prompt is often what reassures.
func (m Model) confirmBody(width int) string {
	wrap := lipgloss.NewStyle().Width(width)
	return strings.Join([]string{
		wrap.Render(m.confirm.Prompt),
		"",
		wrap.Inherit(dimStyle).Render("y or enter to confirm, n or esc to cancel"),
	}, "\n")
}

// ask opens the confirmation. Nothing is sent until the answer.
func (m Model) ask(prompt string, action func(Model) (tea.Model, tea.Cmd)) (tea.Model, tea.Cmd) {
	m.overlay = overlayConfirm
	m.confirm = confirmState{Prompt: prompt, Action: action}
	return m, nil
}
