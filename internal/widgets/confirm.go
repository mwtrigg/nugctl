package widgets

import (
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mwtrigg/nugctl/internal/theme"
)

// Confirm is a type-to-confirm destructive action modal.
type Confirm struct {
	Title    string
	Target   string
	Expected string
	input    textinput.Model
}

func NewConfirm(title, target, expected string) Confirm {
	ti := textinput.New()
	ti.Placeholder = expected
	ti.Width = len([]rune(expected)) + 4
	ti.Focus()
	return Confirm{Title: title, Target: target, Expected: expected, input: ti}
}

func (c Confirm) Confirmed() bool {
	return c.input.Value() == c.Expected
}

func (c *Confirm) SetInput(val string) {
	c.input.SetValue(val)
}

func (c Confirm) Update(msg tea.Msg) (Confirm, tea.Cmd) {
	var cmd tea.Cmd
	c.input, cmd = c.input.Update(msg)
	return c, cmd
}

func (c Confirm) View() string {
	borderColor := theme.Fail

	titleStyle := lipgloss.NewStyle().Foreground(theme.Fail).Bold(true)
	bodyStyle := lipgloss.NewStyle().Foreground(theme.Text)
	targetStyle := lipgloss.NewStyle().Foreground(theme.TextHi).Bold(true)
	hintStyle := lipgloss.NewStyle().Foreground(theme.Muted)

	width := len([]rune(c.Expected)) + 12
	if width < 32 {
		width = 32
	}

	box := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(borderColor).
		Padding(1, 2).
		Width(width)

	content := titleStyle.Render(c.Title) + "\n\n" +
		bodyStyle.Render("This will permanently delete ") + targetStyle.Render(c.Target) + "\n\n" +
		hintStyle.Render("Type the name to confirm:") + "\n" +
		c.input.View()

	return box.Render(content)
}
