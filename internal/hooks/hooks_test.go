package hooks

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeConfig(t *testing.T, dir, content string, mode os.FileMode) {
	t.Helper()
	file := filepath.Join(dir, FileName)
	if err := os.WriteFile(file, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(file, mode); err != nil {
		t.Fatal(err)
	}
}

func TestLoadMissingFileIsEmptyAndNotCreated(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Hooks) != 0 || NewRunner(cfg) != nil {
		t.Fatalf("missing file must load no hooks, got %+v", cfg)
	}
	if _, err := os.Stat(filepath.Join(dir, FileName)); !os.IsNotExist(err) {
		t.Fatalf("Load must never create hooks.json, stat err = %v", err)
	}
}

func TestLoadValidFile(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `{"hooks":{
		"run_end":[{"command":"true"}],
		"pre_tool":[{"name":"no-dotenv","match":["read","mcp__github__*"],"command":"~/bin/deny","timeout":5,"on_error":"allow"},{"command":"true"}]
	}}`, 0o600)
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Hooks) != 3 {
		t.Fatalf("hooks = %d, want 3", len(cfg.Hooks))
	}
	first := cfg.Hooks[0]
	if first.Event != PreTool || first.Name != "no-dotenv" || first.Timeout != 5*time.Second || !first.FailOpen {
		t.Fatalf("first hook = %+v", first)
	}
	if cfg.Hooks[1].Name != "pre_tool[1]" || cfg.Hooks[1].Timeout != DefaultTimeout || cfg.Hooks[1].FailOpen {
		t.Fatalf("defaults not applied: %+v", cfg.Hooks[1])
	}
	if !first.matches("read") || !first.matches("mcp__github__issues__ab12") || first.matches("grep") {
		t.Fatal("match globs over canonical names are wrong")
	}
	if cfg.Hooks[2].Event != RunEnd {
		t.Fatalf("hooks must be listed in event order, got %s last", cfg.Hooks[2].Event)
	}
}

func TestLoadRejectsWholeFile(t *testing.T) {
	cases := []struct {
		name    string
		content string
		mode    os.FileMode
		want    string
	}{
		{"invalid json", `{"hooks":`, 0o600, "invalid JSON"},
		{"unknown top-level key", `{"hook":{}}`, 0o600, "invalid JSON"},
		{"unknown event", `{"hooks":{"before_tool":[{"command":"true"}]}}`, 0o600, `unknown event "before_tool"`},
		{"empty command", `{"hooks":{"run_end":[{"command":"true"},{"command":"  "}]}}`, 0o600, "run_end[1]: command is required"},
		{"bad glob", `{"hooks":{"pre_tool":[{"command":"true","match":["[read"]}]}}`, 0o600, "match pattern"},
		{"match on observe event", `{"hooks":{"run_end":[{"command":"true","match":["read"]}]}}`, 0o600, "match is only allowed"},
		{"timeout too large", `{"hooks":{"pre_tool":[{"command":"true","timeout":61}]}}`, 0o600, "timeout must be"},
		{"timeout zero", `{"hooks":{"pre_tool":[{"command":"true","timeout":0}]}}`, 0o600, "timeout must be"},
		{"fractional timeout", `{"hooks":{"pre_tool":[{"command":"true","timeout":1.5}]}}`, 0o600, "timeout must be"},
		{"bad on_error", `{"hooks":{"pre_tool":[{"command":"true","on_error":"ignore"}]}}`, 0o600, "on_error must be"},
		{"unknown entry field", `{"hooks":{"pre_tool":[{"command":"true","decision":"allow"}]}}`, 0o600, "invalid JSON"},
		{"group writable", `{"hooks":{}}`, 0o620, "writable by group or others"},
		{"world writable", `{"hooks":{}}`, 0o602, "writable by group or others"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeConfig(t, dir, tc.content, tc.mode)
			cfg, err := Load(dir)
			if err == nil {
				t.Fatalf("Load accepted %s", tc.content)
			}
			if !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), FileName) {
				t.Fatalf("error %q does not name %q and the file", err, tc.want)
			}
			if len(cfg.Hooks) != 0 {
				t.Fatalf("a rejected file must start no hooks, got %d", len(cfg.Hooks))
			}
		})
	}
}

// script writes an executable shell script and returns its path.
func script(t *testing.T, body string) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "hook.sh")
	if err := os.WriteFile(file, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return file
}

func runner(hooks ...Hook) *Runner {
	for i := range hooks {
		if hooks[i].Timeout == 0 {
			hooks[i].Timeout = 5 * time.Second
		}
		if hooks[i].Name == "" {
			hooks[i].Name = string(hooks[i].Event)
		}
	}
	return NewRunner(Config{Path: "hooks.json", Hooks: hooks})
}

func toolEvent(name string, args string) Event {
	return Event{Event: PreTool, SessionID: "s1", Repository: os.TempDir(), Agent: "main", Tool: &ToolCall{Name: name, Source: "builtin", Effects: []string{"read"}, Arguments: json.RawMessage(args)}}
}

func TestFireExitTwoBlocksWithStderrReason(t *testing.T) {
	hook := script(t, `payload=$(cat); case "$payload" in *'.env'*) echo "no dotenv reads" >&2; exit 2;; esac; exit 0`)
	r := runner(Hook{Event: PreTool, Name: "no-dotenv", Command: hook, Match: []string{"read"}})
	out := r.Fire(context.Background(), toolEvent("read", `{"path":"config/.env"}`))
	if out.Block == nil || out.Block.Reason != "no dotenv reads" || out.Block.Hook != "no-dotenv" {
		t.Fatalf("outcome = %+v", out)
	}
	if got := out.Block.Error(); got != `Blocked by hook "no-dotenv": no dotenv reads` {
		t.Fatalf("message = %q", got)
	}
	if out := r.Fire(context.Background(), toolEvent("read", `{"path":"main.go"}`)); out.Block != nil {
		t.Fatalf("other reads must proceed, got %+v", out)
	}
	if out := r.Fire(context.Background(), toolEvent("grep", `{"pattern":".env"}`)); out.Block != nil {
		t.Fatalf("an unmatched tool must not run the hook, got %+v", out)
	}
}

func TestFireStdoutBlockDecision(t *testing.T) {
	hook := script(t, `cat >/dev/null; echo '{"decision":"block","reason":"not today"}'`)
	out := runner(Hook{Event: PreTool, Command: hook}).Fire(context.Background(), toolEvent("run_command", `{"command":"ls"}`))
	if out.Block == nil || out.Block.Reason != "not today" {
		t.Fatalf("outcome = %+v", out)
	}
}

func TestFireApproveDecisionIsIgnoredWithWarning(t *testing.T) {
	for _, decision := range []string{"allow", "approve", "yes"} {
		hook := script(t, `cat >/dev/null; echo '{"decision":"`+decision+`"}'`)
		out := runner(Hook{Event: PreTool, Command: hook}).Fire(context.Background(), toolEvent("run_command", `{"command":"ls"}`))
		if out.Block != nil {
			t.Fatalf("%s: must not block, got %+v", decision, out)
		}
		if len(out.Warnings) != 1 || !strings.Contains(out.Warnings[0], "hooks cannot approve") {
			t.Fatalf("%s: warnings = %q", decision, out.Warnings)
		}
	}
}

func TestFireTimeoutFailsClosedOrOpen(t *testing.T) {
	hook := script(t, `sleep 30`)
	closed := runner(Hook{Event: PreTool, Name: "slow", Command: hook, Timeout: time.Second})
	start := time.Now()
	out := closed.Fire(context.Background(), toolEvent("read", `{}`))
	if time.Since(start) > 10*time.Second {
		t.Fatalf("timed-out hook was not killed promptly (%s)", time.Since(start))
	}
	if out.Block == nil || !strings.Contains(out.Block.Reason, "timed out") {
		t.Fatalf("timeout must block by default, got %+v", out)
	}
	if len(out.Warnings) != 1 || !strings.Contains(out.Warnings[0], "blocked") {
		t.Fatalf("warning must say the call was blocked, got %q", out.Warnings)
	}
	open := runner(Hook{Event: PreTool, Name: "slow", Command: hook, Timeout: time.Second, FailOpen: true})
	out = open.Fire(context.Background(), toolEvent("read", `{}`))
	if out.Block != nil || len(out.Warnings) != 1 || !strings.Contains(out.Warnings[0], "allowed") {
		t.Fatalf("on_error allow must let the call through with a warning, got %+v", out)
	}
}

func TestFireUnreadableOutputAndNonzeroExitFailClosed(t *testing.T) {
	for name, body := range map[string]string{
		"unreadable": `cat >/dev/null; echo "looks fine to me"`,
		"exit 1":     `cat >/dev/null; echo oops >&2; exit 1`,
	} {
		out := runner(Hook{Event: PreTool, Command: script(t, body)}).Fire(context.Background(), toolEvent("read", `{}`))
		if out.Block == nil || !strings.HasPrefix(out.Block.Reason, "hook failed: ") {
			t.Fatalf("%s: outcome = %+v", name, out)
		}
	}
}

func TestFireOrderFirstBlockStopsLaterHooks(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	first := script(t, `cat >/dev/null; echo first >>"`+marker+`"`)
	blocker := script(t, `cat >/dev/null; echo blocker >>"`+marker+`"; echo stop >&2; exit 2`)
	never := script(t, `cat >/dev/null; echo never >>"`+marker+`"`)
	r := runner(Hook{Event: PreTool, Name: "a", Command: first}, Hook{Event: PreTool, Name: "b", Command: blocker}, Hook{Event: PreTool, Name: "c", Command: never})
	out := r.Fire(context.Background(), toolEvent("read", `{}`))
	if out.Block == nil || out.Block.Hook != "b" {
		t.Fatalf("outcome = %+v", out)
	}
	data, _ := os.ReadFile(marker)
	if got := string(data); got != "first\nblocker\n" {
		t.Fatalf("hooks ran %q, want first then blocker only", got)
	}
	statuses := r.Status()
	if statuses[0].Last != "ok" || !strings.HasPrefix(statuses[1].Last, "blocked: stop") || statuses[2].Last != "" {
		t.Fatalf("statuses = %+v", statuses)
	}
}

func TestFireObserveFailureNeverBlocks(t *testing.T) {
	hook := script(t, `cat >/dev/null; exit 2`)
	out := runner(Hook{Event: RunEnd, Name: "sound", Command: hook}).Fire(context.Background(), Event{Event: RunEnd, Outcome: "completed"})
	if out.Block != nil {
		t.Fatalf("observe events never block, got %+v", out)
	}
	if len(out.Warnings) != 1 || !strings.Contains(out.Warnings[0], `Hook "sound" (run_end) failed: exit status 2`) {
		t.Fatalf("warnings = %q", out.Warnings)
	}
}

func TestFirePayloadAndEnvironment(t *testing.T) {
	dir := t.TempDir()
	payloadFile := filepath.Join(dir, "payload.json")
	envFile := filepath.Join(dir, "env.txt")
	hook := script(t, `cat >"`+payloadFile+`"; env >"`+envFile+`"; pwd >>"`+envFile+`"`)
	t.Setenv("LIKHA_API_KEY", "sk-secret-main")
	t.Setenv("LIKHA_OPENAI_API_KEY", "sk-secret-openai")
	t.Setenv("LIKHA_HOOK_KEEP", "kept")
	repo := t.TempDir()
	resumed := true
	out := runner(Hook{Event: SessionStart, Command: hook}).Fire(context.Background(), Event{Event: SessionStart, SessionID: "sess-1", Repository: repo, Agent: "main", Resumed: &resumed})
	if len(out.Warnings) != 0 {
		t.Fatalf("warnings = %q", out.Warnings)
	}
	data, err := os.ReadFile(payloadFile)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("payload is not one JSON object: %v (%s)", err, data)
	}
	if payload["version"] != float64(1) || payload["event"] != "session_start" || payload["session_id"] != "sess-1" || payload["repository"] != repo || payload["agent"] != "main" || payload["resumed"] != true {
		t.Fatalf("payload = %v", payload)
	}
	env, _ := os.ReadFile(envFile)
	text := string(env)
	if strings.Contains(text, "sk-secret") {
		t.Fatal("provider credentials leaked into the hook environment")
	}
	for _, want := range []string{"LIKHA_HOOK_EVENT=session_start", "LIKHA_SESSION_ID=sess-1", "LIKHA_HOOK_KEEP=kept"} {
		if !strings.Contains(text, want) {
			t.Fatalf("environment lacks %s", want)
		}
	}
	realRepo, _ := filepath.EvalSymlinks(repo)
	if !strings.Contains(text, realRepo) {
		t.Fatalf("hook did not run in the repository root %s", realRepo)
	}
}

func TestFireBoundsOutputAndReason(t *testing.T) {
	hook := script(t, `cat >/dev/null; head -c 1048576 /dev/zero | tr '\0' 'x' >&2; exit 2`)
	out := runner(Hook{Event: PreTool, Command: hook}).Fire(context.Background(), toolEvent("read", `{}`))
	if out.Block == nil {
		t.Fatalf("outcome = %+v", out)
	}
	if len(out.Block.Reason) > ReasonLimit+len("…") {
		t.Fatalf("reason is %d bytes, want at most %d", len(out.Block.Reason), ReasonLimit)
	}
}

func TestFireCancelledContextBlocksNothing(t *testing.T) {
	hook := script(t, `sleep 30`)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()
	out := runner(Hook{Event: PreTool, Command: hook}).Fire(ctx, toolEvent("read", `{}`))
	if !out.Cancelled || out.Block != nil {
		t.Fatalf("outcome = %+v", out)
	}
}

func TestNilRunnerIsInert(t *testing.T) {
	var r *Runner
	if r.Has(PreTool) || r.Status() != nil || r.Path() != "" {
		t.Fatal("nil runner must report no hooks")
	}
	if out := r.Fire(context.Background(), toolEvent("read", `{}`)); out.Block != nil || len(out.Warnings) != 0 {
		t.Fatalf("nil runner fired: %+v", out)
	}
}

func TestBoundContent(t *testing.T) {
	short, cut := BoundContent("ok")
	if short != "ok" || cut {
		t.Fatal("short content must pass unchanged")
	}
	long, cut := BoundContent(strings.Repeat("é", ResultLimit))
	if !cut || len(long) > ResultLimit {
		t.Fatalf("long content = %d bytes, cut = %v", len(long), cut)
	}
}
