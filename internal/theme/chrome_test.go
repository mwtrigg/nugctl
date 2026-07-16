package theme

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestHeaderW(t *testing.T) {
	out := HeaderW("prod", "https://nuget.example.com/v3/index.json", false, true, 100)
	if out == "" {
		t.Fatal("HeaderW returned empty string")
	}
	if !strings.Contains(out, "nuget.example.com") {
		t.Errorf("HeaderW output missing hostname: %q", out)
	}
	if strings.Contains(out, "INSECURE") {
		t.Error("HeaderW should not render INSECURE badge when insecure=false")
	}
}

func TestHeaderW_Insecure(t *testing.T) {
	out := HeaderW("dev", "https://feed.internal/v3/index.json", true, false, 100)
	if !strings.Contains(out, "INSECURE") {
		t.Error("HeaderW should render INSECURE badge when insecure=true")
	}
}

func TestHeaderW_NoProfile(t *testing.T) {
	out := HeaderW("", "", false, true, 80)
	if !strings.Contains(out, "no profile") {
		t.Errorf("HeaderW should show a placeholder when profileName is empty: %q", out)
	}
}

func TestNav(t *testing.T) {
	out := Nav([]string{"Search", "Profiles", "Feed"}, 1)
	if out == "" {
		t.Fatal("Nav returned empty string")
	}
	for _, want := range []string{"Search", "Profiles", "Feed"} {
		if !strings.Contains(out, want) {
			t.Errorf("Nav output missing tab label %q", want)
		}
	}
}

func TestHr(t *testing.T) {
	out := Hr(40)
	if w := lipgloss.Width(out); w != 40 {
		t.Errorf("Hr(40) width = %d, want 40", w)
	}
}

func TestKeysBar_DedupesGlobals(t *testing.T) {
	out := KeysBar([]KeyHint{{Key: "q", Desc: "quit search"}})
	if strings.Count(out, "quit") != 1 {
		t.Errorf("KeysBar should not duplicate the 'q' hint, got: %q", out)
	}
}

func TestKeysBar_AppendsGlobals(t *testing.T) {
	out := KeysBar([]KeyHint{{Key: "/", Desc: "search"}})
	if !strings.Contains(out, "help") || !strings.Contains(out, "quit") {
		t.Errorf("KeysBar should append global hints, got: %q", out)
	}
}

func TestCard(t *testing.T) {
	out := Card("resources", "some content", 30)
	if !strings.Contains(out, "RESOURCES") {
		t.Errorf("Card should upper-case its label, got: %q", out)
	}
}

func TestPad(t *testing.T) {
	if got := Pad("hi", 5, "right"); lipgloss.Width(got) != 5 {
		t.Errorf("Pad right width = %d, want 5", lipgloss.Width(got))
	}
	if got := Pad("hi", 5, "left"); lipgloss.Width(got) != 5 {
		t.Errorf("Pad left width = %d, want 5", lipgloss.Width(got))
	}
	if got := Pad("toolongforthis", 4, "right"); len([]rune(got)) != 4 {
		t.Errorf("Pad should truncate plain text to n runes, got %q", got)
	}
}
