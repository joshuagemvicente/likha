package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/unix"

	"likha/internal/actions"
)

// Event is the JSON object a hook receives on stdin. Every event carries the
// common fields; the others appear only on the events that define them.
type Event struct {
	Version    int       `json:"version"`
	Event      EventName `json:"event"`
	SessionID  string    `json:"session_id"`
	Repository string    `json:"repository"`
	Agent      string    `json:"agent"`
	// session_start
	Resumed *bool `json:"resumed,omitempty"`
	// prompt_submit
	Prompt string `json:"prompt,omitempty"`
	Origin string `json:"origin,omitempty"`
	// pre_tool and post_tool
	Tool *ToolCall `json:"tool,omitempty"`
	// post_tool
	Result *ToolResult `json:"result,omitempty"`
	// run_end
	Outcome string `json:"outcome,omitempty"`
	Error   string `json:"error,omitempty"`
	// attention
	Attention string `json:"attention,omitempty"`
}

// Prompt origins for prompt_submit.
const (
	OriginTyped    = "typed"
	OriginSteering = "steering"
	OriginInit     = "/init"
	OriginInstall  = "/install"
)

// ToolCall describes one validated tool call.
type ToolCall struct {
	Name      string          `json:"name"`
	Source    string          `json:"source"`
	Server    string          `json:"server,omitempty"`
	Effects   []string        `json:"effects"`
	Arguments json.RawMessage `json:"arguments"`
}

// ToolResult is the bounded final result a post_tool hook sees.
type ToolResult struct {
	Status           string `json:"status"`
	ExitCode         *int   `json:"exit_code,omitempty"`
	Content          string `json:"content"`
	ContentTruncated bool   `json:"content_truncated,omitempty"`
}

// BoundContent cuts result content to ResultLimit on a UTF-8 boundary and
// reports whether anything was cut.
func BoundContent(content string) (string, bool) {
	content = strings.ToValidUTF8(content, "�")
	if len(content) <= ResultLimit {
		return content, false
	}
	return utf8Prefix(content, ResultLimit), true
}

// BlockError is a refusal by a blocking hook. Its message is what the model
// receives for a blocked tool call.
type BlockError struct {
	Hook   string
	Reason string
}

func (e *BlockError) Error() string {
	return fmt.Sprintf("Blocked by hook %q: %s", e.Hook, e.Reason)
}

// PromptMessage is the notice shown when a prompt is blocked.
func (e *BlockError) PromptMessage() string {
	return fmt.Sprintf("Prompt blocked by hook %q: %s", e.Hook, e.Reason)
}

// Outcome is what firing one event produced. Block is non-nil only for a
// blocking event; Warnings are shown to the user and never sent to the model.
type Outcome struct {
	Block     *BlockError
	Warnings  []string
	Cancelled bool
}

// HookStatus is one configured hook with its most recent outcome, for /hooks.
type HookStatus struct {
	Hook
	Last   string
	LastAt time.Time
}

// Runner fires configured hooks. It is safe for concurrent use: parallel tool
// calls fire their hooks in parallel.
type Runner struct {
	path  string
	hooks []Hook

	mu   sync.Mutex
	last map[int]HookStatus
}

// NewRunner returns a runner for a loaded configuration, or nil when the
// configuration has no hooks, so callers keep their exact hook-free path.
func NewRunner(cfg Config) *Runner {
	if len(cfg.Hooks) == 0 {
		return nil
	}
	return &Runner{path: cfg.Path, hooks: append([]Hook(nil), cfg.Hooks...), last: make(map[int]HookStatus)}
}

// Has reports whether any hook is configured for the event. A nil runner has
// none.
func (r *Runner) Has(event EventName) bool {
	if r == nil {
		return false
	}
	for _, hook := range r.hooks {
		if hook.Event == event {
			return true
		}
	}
	return false
}

// Path is the hooks.json the runner was loaded from.
func (r *Runner) Path() string {
	if r == nil {
		return ""
	}
	return r.path
}

// Status lists every configured hook in file order with its last outcome.
func (r *Runner) Status() []HookStatus {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	statuses := make([]HookStatus, len(r.hooks))
	for i, hook := range r.hooks {
		status := r.last[i]
		status.Hook = hook
		statuses[i] = status
	}
	return statuses
}

// Fire runs the hooks for ev.Event one after another in file order. For a
// blocking event the first block wins and later hooks do not run; a failing
// hook blocks unless it set on_error to allow. Observe-only events never
// block. A cancelled ctx stops the remaining hooks and blocks nothing.
func (r *Runner) Fire(ctx context.Context, ev Event) Outcome {
	var outcome Outcome
	if r == nil {
		return outcome
	}
	ev.Version = PayloadVersion
	payload, err := json.Marshal(ev)
	if err != nil {
		outcome.Warnings = append(outcome.Warnings, fmt.Sprintf("Hooks for %s did not run: encode event: %v", ev.Event, err))
		return outcome
	}
	for index, hook := range r.hooks {
		if hook.Event != ev.Event || (ev.Tool != nil && !hook.matches(ev.Tool.Name)) {
			continue
		}
		if ctx.Err() != nil {
			outcome.Cancelled = true
			return outcome
		}
		result := runHook(ctx, hook, ev, payload)
		if ctx.Err() != nil {
			r.record(index, "cancelled")
			outcome.Cancelled = true
			return outcome
		}
		if result.warning != "" {
			outcome.Warnings = append(outcome.Warnings, result.warning)
		}
		switch {
		case !ev.Event.Blocking():
			if result.err != nil {
				r.record(index, "failed: "+result.err.Error())
				outcome.Warnings = append(outcome.Warnings, fmt.Sprintf("Hook %q (%s) failed: %v.", hook.Name, hook.Event, result.err))
				continue
			}
			r.record(index, "ok")
		case result.err != nil && hook.FailOpen:
			r.record(index, "failed, allowed: "+result.err.Error())
			outcome.Warnings = append(outcome.Warnings, fmt.Sprintf("Hook %q (%s) failed: %v; allowed because on_error is allow.", hook.Name, hook.Event, result.err))
		case result.err != nil:
			r.record(index, "failed, blocked: "+result.err.Error())
			outcome.Warnings = append(outcome.Warnings, fmt.Sprintf("Hook %q (%s) failed: %v; blocked.", hook.Name, hook.Event, result.err))
			outcome.Block = &BlockError{Hook: hook.Name, Reason: "hook failed: " + result.err.Error()}
			return outcome
		case result.block:
			r.record(index, "blocked: "+result.reason)
			outcome.Block = &BlockError{Hook: hook.Name, Reason: result.reason}
			return outcome
		default:
			r.record(index, "ok")
		}
	}
	return outcome
}

func (r *Runner) record(index int, last string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.last[index] = HookStatus{Last: last, LastAt: time.Now()}
}

type hookResult struct {
	block   bool
	reason  string
	warning string
	err     error
}

// decision is the optional stdout object of a blocking hook.
type decision struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

// runHook runs one hook with `sh -c` in the repository root, in its own
// process group so a timeout or cancel kills everything it started.
func runHook(ctx context.Context, hook Hook, ev Event, payload []byte) hookResult {
	runCtx, cancel := context.WithTimeout(ctx, hook.Timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "sh", "-c", hook.Command)
	if info, err := os.Stat(ev.Repository); err == nil && info.IsDir() {
		cmd.Dir = ev.Repository
	}
	cmd.Env = append(actions.CommandEnv(os.Environ()), "LIKHA_HOOK_EVENT="+string(ev.Event), "LIKHA_SESSION_ID="+ev.SessionID)
	cmd.Stdin = bytes.NewReader(payload)
	stdout, stderr := &boundedBuffer{limit: OutputLimit}, &boundedBuffer{limit: OutputLimit}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		if err := unix.Kill(-cmd.Process.Pid, unix.SIGKILL); err != nil {
			if errors.Is(err, unix.ESRCH) {
				return os.ErrProcessDone
			}
			return err
		}
		return nil
	}
	// A background child holding stdout open must not keep Wait blocked.
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	if ctx.Err() != nil {
		return hookResult{err: ctx.Err()}
	}
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return hookResult{err: fmt.Errorf("timed out after %s", hook.Timeout)}
	}
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		if exit.ExitCode() == 2 && ev.Event.Blocking() {
			return hookResult{block: true, reason: boundReason(stderr.String(), "exit status 2")}
		}
		return hookResult{err: fmt.Errorf("exit status %d%s", exit.ExitCode(), stderrSuffix(stderr.String()))}
	case err != nil:
		return hookResult{err: fmt.Errorf("start: %w", err)}
	}
	if !ev.Event.Blocking() {
		return hookResult{}
	}
	return readDecision(hook, stdout.String())
}

// readDecision interprets a blocking hook's stdout after exit status 0.
// Nothing means no objection; anything other than a JSON object is
// unreadable. Any decision but "block" is no objection, and a decision that
// tries to approve is reported, because hooks cannot approve.
func readDecision(hook Hook, out string) hookResult {
	out = strings.TrimSpace(out)
	if out == "" {
		return hookResult{}
	}
	var d decision
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		return hookResult{err: errors.New("unreadable output: stdout must be empty or a JSON object")}
	}
	switch strings.ToLower(strings.TrimSpace(d.Decision)) {
	case "":
		return hookResult{}
	case "block":
		return hookResult{block: true, reason: boundReason(d.Reason, "no reason given")}
	default:
		return hookResult{warning: fmt.Sprintf("Hook %q returned decision %q; hooks cannot approve, so it counts as no objection and every usual review still applies.", hook.Name, d.Decision)}
	}
}

func boundReason(reason, fallback string) string {
	reason = strings.TrimSpace(strings.ToValidUTF8(reason, "�"))
	if reason == "" {
		return fallback
	}
	if len(reason) > ReasonLimit {
		reason = utf8Prefix(reason, ReasonLimit) + "…"
	}
	return reason
}

func stderrSuffix(stderr string) string {
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		return ""
	}
	return ": " + boundReason(stderr, "")
}

func utf8Prefix(s string, limit int) string {
	if limit >= len(s) {
		return s
	}
	for limit > 0 && !utf8.RuneStart(s[limit]) {
		limit--
	}
	return s[:limit]
}

// boundedBuffer keeps the first limit bytes and discards the rest while still
// draining the writer, so a chatty hook can neither block nor grow memory.
type boundedBuffer struct {
	limit int
	buf   bytes.Buffer
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if room := b.limit - b.buf.Len(); room > 0 {
		if len(p) <= room {
			b.buf.Write(p)
		} else {
			b.buf.Write(p[:room])
		}
	}
	return len(p), nil
}

func (b *boundedBuffer) String() string { return b.buf.String() }
