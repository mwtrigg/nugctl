package widgets_test

import (
	"strings"
	"testing"

	"github.com/mwtrigg/nugctl/internal/widgets"
)

func TestConfirmRendersEntityName(t *testing.T) {
	m := widgets.NewConfirm("Delete profile", "prod", "prod")
	out := m.View()
	if !strings.Contains(out, "prod") {
		t.Errorf("Confirm.View() missing target name, got: %q", out)
	}
}

func TestConfirmNotReadyOnEmpty(t *testing.T) {
	m := widgets.NewConfirm("Delete profile", "prod", "prod")
	if m.Confirmed() {
		t.Error("Confirmed() should be false before any input")
	}
}

func TestConfirmReadyWhenTypedCorrectly(t *testing.T) {
	m := widgets.NewConfirm("Delete profile", "prod", "prod")
	m.SetInput("prod")
	if !m.Confirmed() {
		t.Error("Confirmed() should be true after typing the exact expected value")
	}
}

func TestConfirmNotReadyWhenTypedWrong(t *testing.T) {
	m := widgets.NewConfirm("Delete profile", "prod", "prod")
	m.SetInput("PROD")
	if m.Confirmed() {
		t.Error("Confirmed() should be false when input doesn't match exactly")
	}
}
