// Package theme defines nugctl's TUI color palette and layout constants.
//
// This is the same "slate-on-amber" palette used by the user's other Go
// Bubble Tea TUI (awxctl), reused verbatim for visual consistency across
// tools. Screen code should always reference these semantic names —
// never a raw hex literal.
package theme

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Surfaces
var (
	BG       = lipgloss.Color("#0e1116")
	BGAlt    = lipgloss.Color("#0a0d12")
	Panel    = lipgloss.Color("#161b22")
	Panel2   = lipgloss.Color("#1d232c")
	Panel3   = lipgloss.Color("#252c37")
	Border   = lipgloss.Color("#2a313b")
	BorderHi = lipgloss.Color("#3a424e")
)

// Ink
var (
	Text    = lipgloss.Color("#d8dde3")
	TextHi  = lipgloss.Color("#ffffff")
	Muted   = lipgloss.Color("#6b7480")
	MutedHi = lipgloss.Color("#8c95a3")
)

// Brand
var (
	Accent    = lipgloss.Color("#ffb454")
	Accent2   = lipgloss.Color("#5fb3b3")
	AccentDim = lipgloss.Color("#b07a30")
)

// Semantic
var (
	Success = lipgloss.Color("#7fbf6e")
	Fail    = lipgloss.Color("#e06c75")
	Warn    = lipgloss.Color("#e5b769")
	Info    = lipgloss.Color("#5fb3b3")
)

// Selection
var (
	SelBg  = lipgloss.Color("#2d3a4f")
	SelFg  = lipgloss.Color("#ffffff")
	SelBar = lipgloss.Color("#ffb454")
)

// Focus / Chrome
var (
	Focus   = lipgloss.Color("#ffb454")
	TitleFg = lipgloss.Color("#ffffff")
	BrandFg = lipgloss.Color("#ffb454")
)

// Layout dimensions (terminal cells)
const (
	DrawerW = 40 // right-side detail drawer width
)

// Pad pads or truncates s to exactly n visual columns. align is "left"
// (right-aligns the content, padding on the left) or "right" (left-aligns,
// padding on the right). ANSI-escape sequences are handled via
// lipgloss.Width so styled chips/pills don't get truncated through their
// escape codes.
func Pad(s string, n int, align string) string {
	w := lipgloss.Width(s)
	if w >= n {
		if !strings.Contains(s, "\x1b") {
			runes := []rune(s)
			if len(runes) >= n {
				return string(runes[:n])
			}
		}
		return s
	}
	padding := strings.Repeat(" ", n-w)
	if align == "left" {
		return padding + s
	}
	return s + padding
}
