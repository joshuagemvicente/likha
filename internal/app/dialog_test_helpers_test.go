package app

import (
	"fmt"
	"net/http"
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

// serveModels answers the connection-check path used by client.EnsureConnected.
func serveModels(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	t.Helper()
	if r.URL.Path != "/v1/models" {
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{"object":"list","data":[]}`)
	return true
}
