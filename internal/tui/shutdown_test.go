package tui

import (
	"context"
	"errors"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"likha/internal/agent"
)

// startFakeRun wires m to a run goroutine that, like the real one, blocks on
// an undroppable send until abandon closes and closes events on return.
func startFakeRun(m *ui) (ctx context.Context, returned <-chan struct{}) {
	ctx, cancel := context.WithCancel(context.Background())
	events := make(chan agent.TurnEvent) // unbuffered: the send blocks
	abandon := make(chan struct{})
	m.cancel, m.events, m.abandon = cancel, events, abandon
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer close(events)
		<-ctx.Done()
		select {
		case events <- agent.TurnEvent{Kind: "error"}:
		case <-abandon:
		}
	}()
	return ctx, done
}

func TestStopRunCancelsAndWaitsForTheRun(t *testing.T) {
	m := &ui{}
	runCtx, returned := startFakeRun(m)
	if err := m.StopRun(context.Background()); err != nil {
		t.Fatalf("StopRun() = %v, want nil", err)
	}
	if runCtx.Err() == nil {
		t.Fatal("run context not cancelled")
	}
	select {
	case <-returned:
	default:
		t.Fatal("StopRun returned before the run goroutine")
	}
}

func TestStopRunAfterCtrlDClosedAbandon(t *testing.T) {
	m := &ui{}
	_, returned := startFakeRun(m)
	m.abandonRun() // what Ctrl+D and /quit leave behind
	if err := m.StopRun(context.Background()); err != nil {
		t.Fatalf("StopRun() = %v, want nil", err)
	}
	<-returned
}

func TestStopRunWithoutARun(t *testing.T) {
	if err := (&ui{}).StopRun(context.Background()); err != nil {
		t.Fatalf("StopRun() = %v, want nil", err)
	}
}

func TestStopRunGivesUpWhenTheRunHangs(t *testing.T) {
	m := &ui{}
	_, cancel := context.WithCancel(context.Background())
	m.cancel, m.events = cancel, make(chan agent.TurnEvent) // never closed
	ctx, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer stop()
	err := m.StopRun(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("StopRun() = %v, want context.DeadlineExceeded", err)
	}
}

// Repeated quit keys during a run release the run goroutine once; a second
// close of abandon would panic.
func TestRepeatedQuitKeysDuringRunDoNotPanic(t *testing.T) {
	cases := []struct {
		name string
		keys []tea.Msg
	}{
		{"Ctrl+D twice", []tea.Msg{tea.KeyMsg{Type: tea.KeyCtrlD}, tea.KeyMsg{Type: tea.KeyCtrlD}}},
		{"Esc, Ctrl+D, Esc", []tea.Msg{tea.KeyMsg{Type: tea.KeyEsc}, tea.KeyMsg{Type: tea.KeyCtrlD}, tea.KeyMsg{Type: tea.KeyEsc}}},
		{"Esc, Ctrl+D, watchdog", []tea.Msg{tea.KeyMsg{Type: tea.KeyEsc}, tea.KeyMsg{Type: tea.KeyCtrlD}, cancelWatchdogMsg{runID: 1}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newFlowUI(t)
			beginFlowRun(m)
			abandon := make(chan struct{})
			m.abandon = abandon
			for _, msg := range tc.keys {
				m.Update(msg)
			}
			select {
			case <-abandon:
			default:
				t.Fatal("abandon never closed")
			}
		})
	}
}

// /quit followed by Ctrl+D closes abandon once.
func TestQuitThenCtrlDDoesNotPanic(t *testing.T) {
	m := newFlowUI(t)
	beginFlowRun(m)
	abandon := make(chan struct{})
	m.abandon = abandon
	m.handleCommand("/quit")
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	select {
	case <-abandon:
	default:
		t.Fatal("abandon never closed")
	}
}
