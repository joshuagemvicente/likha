package tui

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Exercise the terminal byte decoder and command loop, not just constructed
// KeyMsgs. No real terminal or system clipboard is touched by this test.
func TestSelectionTerminalKeyEncodingAndAsyncCopy(t *testing.T) {
	for _, mouse := range []bool{false, true} {
		name := "keyboard"
		if mouse {
			name = "mouse"
		}
		t.Run(name, func(t *testing.T) { runSelectionTerminalCopy(t, mouse) })
	}
}

func runSelectionTerminalCopy(t *testing.T, mouse bool) {
	t.Helper()
	m := newKeysTestUI(t)
	sendRunes(m, "copy me")
	sequence := strings.Repeat("\x1b[1;2D", 2) + "\x1bc"
	if mouse {
		left, top, _, _ := m.composerTextBounds()
		// SGR mouse positions are one-based. Press at rune 5, drag to rune
		// 7, then release before requesting an explicit clipboard copy.
		sequence = fmt.Sprintf("\x1b[<0;%d;%dM\x1b[<32;%d;%dM\x1b[<0;%d;%dm\x1bc", left+6, top+1, left+8, top+1, left+8, top+1)
	}
	copied := make(chan string, 1)
	m.clipboard.writer = func(text string) error {
		copied <- text
		return nil
	}
	reader, writer := io.Pipe()
	t.Cleanup(func() { _ = reader.Close(); _ = writer.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	program := tea.NewProgram(m, tea.WithContext(ctx), tea.WithInput(reader), tea.WithOutput(io.Discard), tea.WithoutRenderer(), tea.WithoutSignalHandler())
	done := make(chan error, 1)
	go func() {
		_, err := program.Run()
		done <- err
	}()
	// CSI 1;2D is Shift+Left, ESC+c is Alt+C in ordinary terminal encoding.
	if _, err := io.WriteString(writer, sequence); err != nil {
		t.Fatal(err)
	}
	select {
	case text := <-copied:
		if text != "me" {
			t.Fatalf("clipboard received %q, want me", text)
		}
	case <-ctx.Done():
		t.Fatal("terminal key sequence did not select and copy")
	}
	program.Send(tea.KeyMsg{Type: tea.KeyCtrlD})
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("terminal program did not quit")
	}
}
