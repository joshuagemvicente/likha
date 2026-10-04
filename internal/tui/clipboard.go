package tui

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

const (
	clipboardWriteTimeout   = 2 * time.Second
	clipboardNoticeDuration = 3 * time.Second
)

// clipboardState is transient UI feedback only. writer is an optional test
// seam; a nil writer uses the system clipboard without reading it first.
type clipboardState struct {
	writer     func(string) error
	writeMu    sync.Mutex
	cancel     context.CancelFunc
	generation uint64
	notice     string
}

// Results deliberately carry neither selected text nor child output.
type clipboardResultMsg struct {
	generation uint64
	sessionID  string
	err        error
}

type clipboardNoticeExpiredMsg struct {
	generation uint64
	sessionID  string
}

// copyText captures everything the command needs before leaving the UI
// goroutine. A newer copy or surface/session change invalidates its result.
func (m *ui) copyText(text string) tea.Cmd {
	m.clearClipboardNotice()
	generation, sessionID := m.clipboard.generation, m.snapshot.ID
	if text == "" {
		m.clipboard.notice = "Select text to copy"
		return expireClipboardNotice(generation, sessionID)
	}
	writer := m.clipboard.writer
	ctx, cancel := context.WithCancel(context.Background())
	m.clipboard.cancel = cancel
	writeMu := &m.clipboard.writeMu
	if writer == nil {
		writer = func(text string) error { return (clipboardSystem{}).writeContext(ctx, text) }
	}
	return func() tea.Msg {
		defer cancel()
		// Serialize native writes and skip superseded queued commands. Result
		// generation checks alone cannot stop an older write from overwriting
		// the clipboard after the most recent selection has been copied.
		writeMu.Lock()
		defer writeMu.Unlock()
		if err := ctx.Err(); err != nil {
			return clipboardResultMsg{generation: generation, sessionID: sessionID, err: err}
		}
		return clipboardResultMsg{generation: generation, sessionID: sessionID, err: writer(text)}
	}
}

func (m *ui) handleClipboardResult(msg clipboardResultMsg) tea.Cmd {
	if msg.generation != m.clipboard.generation || msg.sessionID != m.snapshot.ID {
		return nil
	}
	m.clipboard.notice = "Copied selection"
	if msg.err != nil {
		m.clipboard.notice = clipboardFailureNotice(msg.err)
	}
	return expireClipboardNotice(msg.generation, msg.sessionID)
}

func expireClipboardNotice(generation uint64, sessionID string) tea.Cmd {
	return tea.Tick(clipboardNoticeDuration, func(time.Time) tea.Msg {
		return clipboardNoticeExpiredMsg{generation: generation, sessionID: sessionID}
	})
}

func (m *ui) handleClipboardNoticeExpired(msg clipboardNoticeExpiredMsg) {
	if msg.generation != m.clipboard.generation || msg.sessionID != m.snapshot.ID {
		return
	}
	m.clipboard.notice = ""
}

func (m *ui) clipboardHint() string { return m.clipboard.notice }

// clearClipboardNotice also invalidates outstanding writes and expiry timers.
// It preserves an injected writer across session and surface changes.
func (m *ui) clearClipboardNotice() {
	if m.clipboard.cancel != nil {
		m.clipboard.cancel()
		m.clipboard.cancel = nil
	}
	m.clipboard.generation++
	m.clipboard.notice = ""
}

// clipboardWriteError contains only a fixed, safe user-facing explanation.
// Never wrap process/lookup errors: they may contain paths or selected text.
type clipboardWriteError struct{ notice string }

func (e *clipboardWriteError) Error() string { return e.notice }

func clipboardFailureNotice(err error) string {
	var failure *clipboardWriteError
	if errors.As(err, &failure) {
		return failure.notice
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "Copy timed out; try again"
	}
	// An injected writer's arbitrary error text is not safe to display either.
	return "Copy failed; check clipboard access"
}

// clipboardSystem isolates host dependencies for tests. Tests can shorten the
// deadline, but a caller cannot extend it beyond clipboardWriteTimeout.
type clipboardSystem struct {
	goos     string
	getenv   func(string) string
	lookPath func(string) (string, error)
	run      func(context.Context, *exec.Cmd) error
	timeout  time.Duration
}

func writeSystemClipboard(text string) error { return (clipboardSystem{}).write(text) }

func (system clipboardSystem) write(text string) error {
	return system.writeContext(context.Background(), text)
}

func (system clipboardSystem) writeContext(parent context.Context, text string) error {
	goos, getenv, lookPath, run := system.goos, system.getenv, system.lookPath, system.run
	if goos == "" {
		goos = runtime.GOOS
	}
	if getenv == nil {
		getenv = os.Getenv
	}
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	if run == nil {
		run = func(_ context.Context, cmd *exec.Cmd) error { return cmd.Run() }
	}
	name, path, args, err := clipboardCommand(goos, getenv, lookPath)
	if err != nil {
		return err
	}
	timeout := system.timeout
	if timeout <= 0 || timeout > clipboardWriteTimeout {
		timeout = clipboardWriteTimeout
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Stdin = strings.NewReader(text)
	// Nil stdout/stderr send child output to the null device, never to the
	// terminal or an error buffer. Bound cleanup if a descendant retains stdin.
	cmd.WaitDelay = 100 * time.Millisecond
	err = run(ctx, cmd)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return &clipboardWriteError{notice: "Copy timed out; try again"}
	}
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &clipboardWriteError{notice: "Copy unavailable: " + name + " not found in PATH"}
		}
		return &clipboardWriteError{notice: "Copy failed: " + name + "; check clipboard access"}
	}
	return nil
}

// Prefer Wayland when available. If wl-copy is missing, fall back to X11 only
// when DISPLAY is also set; prefer xclip, then xsel. Do not retry after a
// helper starts and fails, since the write may already have taken effect.
func clipboardCommand(goos string, getenv func(string) string, lookPath func(string) (string, error)) (name, path string, args []string, err error) {
	switch goos {
	case "darwin":
		path, err = lookPath("pbcopy")
		if err != nil {
			return "", "", nil, &clipboardWriteError{notice: "Copy unavailable: pbcopy not found in PATH"}
		}
		return "pbcopy", path, nil, nil
	case "linux":
		wayland := getenv("WAYLAND_DISPLAY") != ""
		x11 := getenv("DISPLAY") != ""
		if wayland {
			if path, err = lookPath("wl-copy"); err == nil {
				return "wl-copy", path, nil, nil
			}
		}
		if !x11 {
			if wayland {
				return "", "", nil, &clipboardWriteError{notice: "Copy unavailable: install wl-clipboard (wl-copy)"}
			}
			return "", "", nil, &clipboardWriteError{notice: "Copy unavailable: run in a Wayland/X11 session"}
		}
		if path, err = lookPath("xclip"); err == nil {
			return "xclip", path, []string{"-selection", "clipboard"}, nil
		}
		if path, err = lookPath("xsel"); err == nil {
			return "xsel", path, []string{"--clipboard", "--input"}, nil
		}
		return "", "", nil, &clipboardWriteError{notice: "Copy unavailable: install xclip or xsel"}
	default:
		return "", "", nil, &clipboardWriteError{notice: "Copy unavailable: unsupported OS"}
	}
}
