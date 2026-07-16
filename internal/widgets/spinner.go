package widgets

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mwtrigg/nugctl/internal/theme"
)

var spinFrames = []rune("⠹⠸⠼⠴⠦⠧⠇⠏")

// SpinTickMsg is sent by the spinner's tick command.
type SpinTickMsg struct{}

// Spinner is a small tea.Model for the braille spinner.
type Spinner struct {
	frame   int
	running bool
}

func NewSpinner() Spinner { return Spinner{running: true} }

func (s Spinner) Init() tea.Cmd {
	return s.tick()
}

func (s Spinner) Update(msg tea.Msg) (Spinner, tea.Cmd) {
	if _, ok := msg.(SpinTickMsg); ok && s.running {
		s.frame = (s.frame + 1) % len(spinFrames)
		return s, s.tick()
	}
	return s, nil
}

func (s Spinner) View() string {
	return lipgloss.NewStyle().Foreground(theme.Accent).Render(string(spinFrames[s.frame]))
}

func (s Spinner) tick() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg { return SpinTickMsg{} })
}
