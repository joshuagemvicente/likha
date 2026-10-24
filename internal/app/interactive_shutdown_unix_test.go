//go:build unix

package app

import (
	"io"
	"os"
	"syscall"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// idleModel is a Bubble Tea model that only waits to be quit.
type idleModel struct{}

func (idleModel) Init() tea.Cmd                       { return nil }
func (idleModel) Update(tea.Msg) (tea.Model, tea.Cmd) { return idleModel{}, nil }
func (idleModel) View() string                        { return "" }

// TestShutdownSignalQuitsARealProgram sends real SIGHUP and SIGTERM to the
// test process: the watch must own them (the default action would kill the
// test binary) and end a running Bubble Tea program with the signal's status.
func TestShutdownSignalQuitsARealProgram(t *testing.T) {
	cases := []struct {
		sig  syscall.Signal
		want int
	}{
		{syscall.SIGHUP, exitHangup},
		{syscall.SIGTERM, exitTerminated},
	}
	for _, tc := range cases {
		t.Run(tc.sig.String(), func(t *testing.T) {
			exits := newExitRecorder()
			w := watchShutdownSignals(exits.exit)
			defer w.stop()
			program := tea.NewProgram(idleModel{}, tea.WithInput(nil), tea.WithOutput(io.Discard), tea.WithoutSignalHandler())
			w.attach(program)
			ran := make(chan error, 1)
			go func() {
				_, err := program.Run()
				ran <- err
			}()
			if err := syscall.Kill(os.Getpid(), tc.sig); err != nil {
				t.Fatalf("send %v: %v", tc.sig, err)
			}
			select {
			case err := <-ran:
				if err != nil {
					t.Fatalf("Run() = %v, want a clean quit", err)
				}
			case <-time.After(waitLimit):
				program.Kill()
				t.Fatalf("program still running after %v", tc.sig)
			}
			if code, ok := w.caught(); !ok || code != tc.want {
				t.Fatalf("caught() = %d, %v, want %d, true", code, ok, tc.want)
			}
		})
	}
}
