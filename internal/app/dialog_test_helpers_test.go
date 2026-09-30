package app

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// forceANSI enables ANSI escape output for the duration of a test so styles
// like the dialog's dimmed background are observable. Tests run without a
// TTY, so the default profile is Ascii; the original profile is restored.
func forceANSI(t *testing.T) {
	t.Helper()
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI)
	t.Cleanup(func() { lipgloss.SetColorProfile(previous) })
}
