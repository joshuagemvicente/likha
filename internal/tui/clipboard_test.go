package tui

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"likha/internal/agent"
	"likha/internal/model"
	"likha/internal/session"
)

func TestClipboardSystemRoutes(t *testing.T) {
	tests := []struct {
		name, goos, wayland, display string
		available                    []string
		wantLookups                  []string
		wantArgs                     []string
		wantNotice                   string
	}{
		{name: "macOS", goos: "darwin", available: []string{"pbcopy"}, wantLookups: []string{"pbcopy"}, wantArgs: []string{"pbcopy"}},
		{name: "macOS missing pbcopy", goos: "darwin", wantLookups: []string{"pbcopy"}, wantNotice: "Copy unavailable: pbcopy not found in PATH"},
		{name: "Wayland preferred", goos: "linux", wayland: "wayland-0", display: ":0", available: []string{"wl-copy", "xclip", "xsel"}, wantLookups: []string{"wl-copy"}, wantArgs: []string{"wl-copy"}},
		{name: "Wayland only", goos: "linux", wayland: "wayland-0", available: []string{"wl-copy"}, wantLookups: []string{"wl-copy"}, wantArgs: []string{"wl-copy"}},
		{name: "X11 xclip", goos: "linux", display: ":0", available: []string{"wl-copy", "xclip", "xsel"}, wantLookups: []string{"xclip"}, wantArgs: []string{"xclip", "-selection", "clipboard"}},
		{name: "X11 xsel fallback", goos: "linux", display: ":0", available: []string{"xsel"}, wantLookups: []string{"xclip", "xsel"}, wantArgs: []string{"xsel", "--clipboard", "--input"}},
		{name: "Wayland missing helper with X11", goos: "linux", wayland: "wayland-0", display: ":0", available: []string{"xclip"}, wantLookups: []string{"wl-copy", "xclip"}, wantArgs: []string{"xclip", "-selection", "clipboard"}},
		{name: "Wayland missing helper with xsel", goos: "linux", wayland: "wayland-0", display: ":0", available: []string{"xsel"}, wantLookups: []string{"wl-copy", "xclip", "xsel"}, wantArgs: []string{"xsel", "--clipboard", "--input"}},
		{name: "Wayland missing helper without X11", goos: "linux", wayland: "wayland-0", available: []string{"xclip"}, wantLookups: []string{"wl-copy"}, wantNotice: "Copy unavailable: install wl-clipboard (wl-copy)"},
		{name: "X11 missing helpers", goos: "linux", display: ":0", wantLookups: []string{"xclip", "xsel"}, wantNotice: "Copy unavailable: install xclip or xsel"},
		{name: "Wayland and X11 missing helpers", goos: "linux", wayland: "wayland-0", display: ":0", wantLookups: []string{"wl-copy", "xclip", "xsel"}, wantNotice: "Copy unavailable: install xclip or xsel"},
		{name: "no display", goos: "linux", available: []string{"wl-copy", "xclip", "xsel"}, wantNotice: "Copy unavailable: run in a Wayland/X11 session"},
		{name: "unsupported OS", goos: "windows", available: []string{"pbcopy"}, wantNotice: "Copy unavailable: unsupported OS"},
	}
	// Leading/trailing whitespace, CRLF, Unicode, and shell syntax are all
	// payload bytes, not command arguments or an interpolated shell program.
	const selected = " \t你好 café 👩🏽‍💻\r\n$(echo secret); 'quoted'\n\n"
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var lookups []string
			runs := 0
			system := clipboardSystem{
				goos: tt.goos,
				getenv: func(key string) string {
					switch key {
					case "WAYLAND_DISPLAY":
						return tt.wayland
					case "DISPLAY":
						return tt.display
					default:
						t.Fatalf("unexpected environment lookup %q", key)
						return ""
					}
				},
				lookPath: func(name string) (string, error) {
					lookups = append(lookups, name)
					for _, available := range tt.available {
						if available == name {
							return "/fake tools/" + name, nil
						}
					}
					return "", errors.New("lookup error with sensitive information")
				},
				run: func(ctx context.Context, cmd *exec.Cmd) error {
					runs++
					wantArgs := append([]string(nil), tt.wantArgs...)
					wantArgs[0] = "/fake tools/" + wantArgs[0]
					if !reflect.DeepEqual(cmd.Args, wantArgs) || cmd.Path != wantArgs[0] {
						t.Fatalf("command = %q, path %q; want %q", cmd.Args, cmd.Path, wantArgs)
					}
					input, err := io.ReadAll(cmd.Stdin)
					if err != nil || string(input) != selected {
						t.Fatalf("stdin was not the exact selection: %q, %v", input, err)
					}
					if cmd.Stdout != nil || cmd.Stderr != nil {
						t.Fatal("child output must be discarded, not captured or displayed")
					}
					deadline, ok := ctx.Deadline()
					if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > clipboardWriteTimeout {
						t.Fatal("clipboard execution must have a bounded deadline")
					}
					if cmd.Cancel == nil || cmd.WaitDelay <= 0 {
						t.Fatal("command must be context-cancellable with bounded pipe cleanup")
					}
					return nil
				},
			}
			err := system.write(selected)
			if tt.wantNotice == "" {
				if err != nil || runs != 1 {
					t.Fatalf("write = %v, runs = %d; want success and one run", err, runs)
				}
			} else if err == nil || err.Error() != tt.wantNotice || runs != 0 {
				t.Fatalf("write = %v, runs = %d; want %q without execution", err, runs, tt.wantNotice)
			}
			if !reflect.DeepEqual(lookups, tt.wantLookups) {
				t.Fatalf("lookups = %q, want %q", lookups, tt.wantLookups)
			}
		})
	}
}

func TestClipboardSystemCommandFailureDoesNotExposeSecretsOrRetry(t *testing.T) {
	const secret = "sensitive selected text and child stderr"
	for _, tt := range []struct {
		name       string
		err        error
		wantNotice string
	}{
		{name: "arbitrary failure", err: errors.New(secret), wantNotice: "Copy failed: wl-copy; check clipboard access"},
		{name: "child stderr", err: &exec.ExitError{Stderr: []byte(secret)}, wantNotice: "Copy failed: wl-copy; check clipboard access"},
		{name: "removed executable", err: &os.PathError{Op: "fork/exec", Path: secret, Err: os.ErrNotExist}, wantNotice: "Copy unavailable: wl-copy not found in PATH"},
		{name: "deadline error", err: context.DeadlineExceeded, wantNotice: "Copy timed out; try again"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			runs, lookups := 0, 0
			system := clipboardSystem{
				goos:   "linux",
				getenv: func(string) string { return "available" },
				lookPath: func(name string) (string, error) {
					lookups++
					return "/fake/" + name, nil
				},
				run: func(context.Context, *exec.Cmd) error { runs++; return tt.err },
			}
			err := system.write(secret)
			if err == nil || err.Error() != tt.wantNotice || strings.Contains(err.Error(), secret) {
				t.Fatalf("unsafe or unexpected error: %v", err)
			}
			if runs != 1 || lookups != 1 {
				t.Fatalf("a failed write retried: runs = %d, lookups = %d", runs, lookups)
			}
		})
	}
}

func TestClipboardSystemTimeout(t *testing.T) {
	system := clipboardSystem{
		goos:     "darwin",
		getenv:   func(string) string { return "" },
		lookPath: func(name string) (string, error) { return "/fake/" + name, nil },
		timeout:  10 * time.Millisecond,
		run: func(ctx context.Context, _ *exec.Cmd) error {
			<-ctx.Done()
			return errors.New("killed child: sensitive stderr")
		},
	}
	started := time.Now()
	err := system.write("selected text")
	if err == nil || err.Error() != "Copy timed out; try again" {
		t.Fatalf("timeout error = %v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("clipboard timeout was not bounded")
	}
}

func TestClipboardSystemCannotExtendTimeout(t *testing.T) {
	system := clipboardSystem{
		goos:     "darwin",
		getenv:   func(string) string { return "" },
		lookPath: func(name string) (string, error) { return "/fake/" + name, nil },
		timeout:  time.Hour,
		run: func(ctx context.Context, _ *exec.Cmd) error {
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) > clipboardWriteTimeout {
				t.Fatal("test seam extended the production timeout")
			}
			return nil
		},
	}
	if err := system.write("selected text"); err != nil {
		t.Fatal(err)
	}
}

func TestClipboardCopyCapturesTextWriterAndSessionWithoutRunningInline(t *testing.T) {
	var copied string
	m := &ui{snapshot: session.Snapshot{ID: "session-a"}}
	m.clipboard.writer = func(text string) error { copied = text; return nil }
	selected := "  α\n你好\n"
	cmd := m.copyText(selected)
	if cmd == nil || copied != "" || m.clipboardHint() != "" {
		t.Fatal("copy must return a command without invoking the writer inline")
	}
	generation := m.clipboard.generation
	selected = "changed selection"
	m.clipboard.writer = func(string) error { t.Fatal("writer was not captured"); return nil }
	m.snapshot.ID = "session-b"
	msg, ok := cmd().(clipboardResultMsg)
	if !ok || msg.generation != generation || msg.sessionID != "session-a" || msg.err != nil {
		t.Fatalf("copy result = %#v", msg)
	}
	if copied != "  α\n你好\n" {
		t.Fatalf("captured selection = %q", copied)
	}
	if m.handleClipboardResult(msg) != nil || m.clipboardHint() != "" {
		t.Fatal("result for a different session must be dropped")
	}
}

func TestClipboardEmptySelectionShowsExpiringNoticeWithoutWriting(t *testing.T) {
	m := &ui{snapshot: session.Snapshot{ID: "session-a"}}
	m.clipboard.writer = func(string) error { t.Fatal("empty selection must not be written"); return nil }
	cmd := m.copyText("")
	if cmd == nil || m.clipboardHint() != "Select text to copy" {
		t.Fatalf("empty selection hint = %q, command present = %t", m.clipboardHint(), cmd != nil)
	}
	started := time.Now()
	msg, ok := cmd().(clipboardNoticeExpiredMsg)
	if !ok || msg.generation != m.clipboard.generation || msg.sessionID != m.snapshot.ID {
		t.Fatalf("expiry message = %#v", msg)
	}
	if elapsed := time.Since(started); elapsed < clipboardNoticeDuration/2 || elapsed > clipboardNoticeDuration+2*time.Second {
		t.Fatalf("notice expiry duration = %s", elapsed)
	}
	m.handleClipboardNoticeExpired(msg)
	if m.clipboardHint() != "" {
		t.Fatal("current expiry did not clear feedback")
	}
}

func TestClipboardFeedbackDoesNotChangeConversationOrAgentState(t *testing.T) {
	for _, tt := range []struct {
		name, text, wantNotice string
		err                    error
	}{
		{name: "success", text: "selected", wantNotice: "Copied selection"},
		{name: "whitespace is a selection", text: "\n \t", wantNotice: "Copied selection"},
		{name: "safe helper failure", text: "selected", err: &clipboardWriteError{notice: "Copy unavailable: install xclip or xsel"}, wantNotice: "Copy unavailable: install xclip or xsel"},
		{name: "arbitrary writer failure", text: "selected", err: errors.New("selected secret must not be shown"), wantNotice: "Copy failed; check clipboard access"},
		{name: "timeout", text: "selected", err: context.DeadlineExceeded, wantNotice: "Copy timed out; try again"},
		{name: "empty selection", wantNotice: "Select text to copy"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := &ui{
				snapshot: session.Snapshot{ID: "session-a"},
				entries:  []entry{{role: "Assistant", content: "retained transcript"}},
				history:  []model.Message{{Role: "user", Content: "retained history"}},
				input:    []rune("unsent draft"),
				status:   "Executing approved command",
				working:  true,
				pending:  &agent.ApprovalRequest{Title: "existing review"},
			}
			entries := append([]entry(nil), m.entries...)
			history := append([]model.Message(nil), m.history...)
			pending := m.pending
			writes := 0
			m.clipboard.writer = func(text string) error {
				writes++
				if text != tt.text {
					t.Fatalf("writer input = %q, want %q", text, tt.text)
				}
				return tt.err
			}
			cmd := m.copyText(tt.text)
			if tt.text != "" {
				cmd = m.handleClipboardResult(cmd().(clipboardResultMsg))
			}
			if cmd == nil || m.clipboardHint() != tt.wantNotice {
				t.Fatalf("hint = %q, expiry command present = %t", m.clipboardHint(), cmd != nil)
			}
			wantWrites := 1
			if tt.text == "" {
				wantWrites = 0
			}
			if writes != wantWrites {
				t.Fatalf("writes = %d, want %d", writes, wantWrites)
			}
			m.handleClipboardNoticeExpired(clipboardNoticeExpiredMsg{generation: m.clipboard.generation, sessionID: m.snapshot.ID})
			m.clearClipboardNotice()
			if !reflect.DeepEqual(m.entries, entries) || !reflect.DeepEqual(m.history, history) || string(m.input) != "unsent draft" || m.status != "Executing approved command" || !m.working || m.pending != pending || pending.Title != "existing review" {
				t.Fatal("clipboard feedback changed conversation, composer, or agent state")
			}
		})
	}
}

func TestClipboardResultsAndExpiryDropStaleGenerations(t *testing.T) {
	m := &ui{snapshot: session.Snapshot{ID: "session-a"}}
	m.clipboard.writer = func(string) error { return nil }
	oldResult := m.copyText("first")().(clipboardResultMsg)
	currentResult := m.copyText("second")().(clipboardResultMsg)
	if m.handleClipboardResult(oldResult) != nil || m.clipboardHint() != "" {
		t.Fatal("stale result replaced feedback")
	}
	if m.handleClipboardResult(currentResult) == nil || m.clipboardHint() != "Copied selection" {
		t.Fatal("current result was not shown with an expiry")
	}
	for _, expired := range []clipboardNoticeExpiredMsg{
		{generation: oldResult.generation, sessionID: "session-a"},
		{generation: currentResult.generation, sessionID: "session-b"},
	} {
		m.handleClipboardNoticeExpired(expired)
		if m.clipboardHint() != "Copied selection" {
			t.Fatal("stale expiry cleared current feedback")
		}
	}
	m.handleClipboardNoticeExpired(clipboardNoticeExpiredMsg{generation: currentResult.generation, sessionID: "session-a"})
	if m.clipboardHint() != "" {
		t.Fatal("matching expiry did not clear feedback")
	}
	// Even an empty selection is a new feedback generation.
	_ = m.copyText("")
	if m.handleClipboardResult(currentResult) != nil || m.clipboardHint() != "Select text to copy" {
		t.Fatal("an earlier write overwrote empty-selection feedback")
	}
}

func TestClipboardSupersededQueuedCopyDoesNotOverwriteLatestSelection(t *testing.T) {
	m := &ui{snapshot: session.Snapshot{ID: "session-a"}}
	var writes []string
	m.clipboard.writer = func(text string) error { writes = append(writes, text); return nil }
	old := m.copyText("old selection")
	latest := m.copyText("latest selection")
	latest()
	old()
	if !reflect.DeepEqual(writes, []string{"latest selection"}) {
		t.Fatalf("superseded clipboard command ran: %q", writes)
	}
}

func TestClipboardInFlightWritesKeepLatestCopyLast(t *testing.T) {
	m := &ui{snapshot: session.Snapshot{ID: "session-a"}}
	started, release := make(chan struct{}), make(chan struct{})
	var writes []string
	m.clipboard.writer = func(text string) error {
		if text == "old" {
			close(started)
			<-release
		}
		writes = append(writes, text)
		return nil
	}
	old := m.copyText("old")
	oldDone := make(chan tea.Msg, 1)
	go func() { oldDone <- old() }()
	<-started
	latest := m.copyText("latest")
	latestDone := make(chan tea.Msg, 1)
	go func() { latestDone <- latest() }()
	close(release)
	<-oldDone
	<-latestDone
	if !reflect.DeepEqual(writes, []string{"old", "latest"}) {
		t.Fatalf("clipboard write order = %q", writes)
	}
}

func TestClipboardClearInvalidatesInFlightResultAndPreservesWriter(t *testing.T) {
	m := &ui{snapshot: session.Snapshot{ID: "session-a"}}
	started, release := make(chan struct{}), make(chan struct{})
	m.clipboard.writer = func(string) error {
		close(started)
		<-release
		return nil
	}
	cmd := m.copyText("captured selection")
	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()
	<-started
	generation := m.clipboard.generation
	m.clearClipboardNotice()
	if m.clipboard.generation == generation || m.clipboard.writer == nil || m.clipboardHint() != "" {
		t.Fatal("clear did not invalidate the generation while preserving the writer")
	}
	close(release)
	if m.handleClipboardResult((<-result).(clipboardResultMsg)) != nil || m.clipboardHint() != "" {
		t.Fatal("cleared surface accepted an in-flight result")
	}
	m.handleClipboardNoticeExpired(clipboardNoticeExpiredMsg{generation: generation, sessionID: "session-a"})
	if m.clipboardHint() != "" {
		t.Fatal("cleared surface accepted an old expiry")
	}
}
