package theme

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// KeyHint is a single key+description pair shown in the keys bar.
type KeyHint struct {
	Key  string
	Desc string
}

// HeaderW renders nugctl's single-row top bar: brand on the left, and on the
// right a connection pip (green = last API call ok, red = failed/unknown),
// the active profile name, the feed's hostname, and an INSECURE badge when
// the active profile has TLS verification disabled.
func HeaderW(profileName, feedURL string, insecure, lastCallOK bool, width int) string {
	brand := lipgloss.NewStyle().Foreground(BrandFg).Bold(true).Render("nugctl")

	host := feedURL
	if u, err := url.Parse(feedURL); err == nil && u.Host != "" {
		host = u.Hostname()
	}
	if profileName == "" {
		profileName = "(no profile)"
	}

	pipColor := Success
	if !lastCallOK {
		pipColor = Fail
	}
	pip := lipgloss.NewStyle().Foreground(pipColor).Render("●")

	profPart := lipgloss.NewStyle().Foreground(MutedHi).Render(profileName)
	hostPart := lipgloss.NewStyle().Foreground(Muted).Render(host)
	sep := lipgloss.NewStyle().Foreground(BorderHi).Render(" · ")

	right := pip + " " + profPart + sep + hostPart
	if insecure {
		right += sep + lipgloss.NewStyle().Foreground(Warn).Bold(true).Render("INSECURE")
	}

	leftW := lipgloss.Width(brand)
	rightW := lipgloss.Width(right)
	gap := width - leftW - rightW
	if gap < 1 {
		gap = 1
	}
	return lipgloss.NewStyle().Background(Panel2).Foreground(Text).
		Render(brand + strings.Repeat(" ", gap) + right)
}

// Nav renders the tab bar. Active tab: bright text + amber bottom border,
// with an amber tab number prefix.
func Nav(tabs []string, activeIdx int) string {
	var parts []string
	for i, tab := range tabs {
		num := lipgloss.NewStyle().Foreground(Accent).Render(fmt.Sprintf("%d", i+1))
		var label string
		if i == activeIdx {
			label = lipgloss.NewStyle().
				Foreground(TextHi).BorderBottom(true).BorderForeground(Accent).
				Render(tab)
		} else {
			label = lipgloss.NewStyle().Foreground(Muted).Render(tab)
		}
		parts = append(parts, lipgloss.NewStyle().Padding(0, 1).Render(num+" "+label))
	}
	return lipgloss.NewStyle().Background(Panel).Render(strings.Join(parts, ""))
}

// Card renders a labeled panel block.
func Card(label, content string, width int) string {
	header := lipgloss.NewStyle().Foreground(Muted).Render(strings.ToUpper(label))
	body := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(BorderHi).
		Background(Panel).
		Width(width - 2).
		Padding(0, 1).
		Render(content)
	return header + "\n" + body
}

// GlobalKeyHints returns the hints that appear on every screen's keys bar.
// nugctl has no command palette, so unlike awxctl this omits ":".
func GlobalKeyHints() []KeyHint {
	return []KeyHint{
		{Key: "?", Desc: "help"},
		{Key: "q", Desc: "quit"},
	}
}

// KeysBar renders the bottom hint bar from a slice of KeyHint. Keys in
// amber, descriptions in muted. Global hints are appended after a ·
// separator; any that already appear in hints are skipped to avoid
// duplication.
func KeysBar(hints []KeyHint) string {
	var globals []KeyHint
	for _, g := range GlobalKeyHints() {
		dup := false
		for _, l := range hints {
			if l.Key == g.Key {
				dup = true
				break
			}
		}
		if !dup {
			globals = append(globals, g)
		}
	}

	parts := make([]string, 0, len(hints)+1+len(globals))
	for _, h := range hints {
		key := lipgloss.NewStyle().Foreground(Accent).Render(h.Key)
		desc := lipgloss.NewStyle().Foreground(Muted).Render(" " + h.Desc)
		parts = append(parts, key+desc)
	}
	if len(globals) > 0 {
		sep := lipgloss.NewStyle().Foreground(BorderHi).Render("·")
		parts = append(parts, sep)
		for _, h := range globals {
			key := lipgloss.NewStyle().Foreground(Accent).Render(h.Key)
			desc := lipgloss.NewStyle().Foreground(Muted).Render(" " + h.Desc)
			parts = append(parts, key+desc)
		}
	}
	return lipgloss.NewStyle().
		Background(Panel).
		Padding(0, 1).
		Render(strings.Join(parts, "  "))
}

// Hr renders a full-width horizontal rule.
func Hr(width int) string {
	return lipgloss.NewStyle().Foreground(BorderHi).Render(strings.Repeat("─", width))
}
