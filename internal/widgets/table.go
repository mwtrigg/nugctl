package widgets

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mwtrigg/nugctl/internal/theme"
)

type Align int

const (
	AlignLeft Align = iota
	AlignRight
)

// Column defines one column in the table.
type Column struct {
	Key   string
	Label string
	Width int
	Align Align
	// Format transforms the raw cell value before padding/display.
	// If nil, the raw value is used as-is.
	Format func(val string) string
	// Color optionally overrides the cell foreground based on the raw cell value.
	Color func(val string) lipgloss.Color
}

// Table is a stateless, render-only monospace block table.
// Embed in a tea.Model and call View() from the parent's View().
type Table struct {
	Columns []Column
	Rows    []map[string]string
	Cursor  int
}

func (t Table) View() string {
	var sb strings.Builder
	for i, row := range t.Rows {
		sb.WriteString(t.renderRow(row, i == t.Cursor))
		sb.WriteString("\n")
	}
	return sb.String()
}

func (t Table) renderRow(row map[string]string, selected bool) string {
	cells := make([]string, len(t.Columns))
	for j, col := range t.Columns {
		val := row[col.Key]
		display := val
		if col.Format != nil {
			display = col.Format(val)
		}
		padded := theme.Pad(display, col.Width, alignStr(col.Align))

		fg := theme.Text
		if col.Color != nil {
			fg = col.Color(val)
		} else if selected {
			fg = theme.SelFg
		}

		s := lipgloss.NewStyle().Foreground(fg)
		if selected {
			s = s.Background(theme.SelBg)
		}
		cells[j] = s.Render(padded)
	}

	content := strings.Join(cells, " ")
	if selected {
		bar := lipgloss.NewStyle().Background(theme.SelBar).Render(" ")
		return bar + content
	}
	return " " + content
}

// alignStr converts widget Align to the side-of-padding convention used by theme.Pad.
// AlignRight → pad on left side ("left") so text appears right-aligned.
// AlignLeft  → pad on right side ("right") so text appears left-aligned.
func alignStr(a Align) string {
	if a == AlignRight {
		return "left"
	}
	return "right"
}
