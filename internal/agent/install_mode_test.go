package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"likha/internal/cmdpolicy"
	"likha/internal/model"
	"likha/internal/tools"
)

const (
	installAskCommand  = "brew install nvm"
	installReadCommand = "uname -sm"
	installPlanArgs    = `{"steps":[{"id":"1","title":"brew install nvm","status":"pending"},{"id":"2","title":"Append nvm init to ~/.zshrc","status":"pending"}]}`
)

func installRegistry(t *testing.T, options RunOptions, reviewer *fakeReviewer) (*tools.Registry, string) {
	t.Helper()
	if options.PlanApply == nil {
		options.PlanApply = func(context.Context, []tools.PlanStep) error { return nil }
	}
	if options.Ask == nil {
		options.Ask = func(context.Context, string, tools.AskRequest) (tools.AskAnswer, error) {
			return tools.AskAnswer{}, errors.New("no questions in this test")
		}
	}
	return initRegistry(t, options, reviewer)
}

func invoke(registry *tools.Registry, name, args string) (tools.Result, error) {
	return registry.Invoke(context.Background(), mainScope(tools.Scope{}), toolCall(name, args))
}

func commandArgs(command string) string {
	data, _ := json.Marshal(map[string]string{"command": command})
	return string(data)
}

func exactEditArgs(path, content string) string {
	data, _ := json.Marshal(map[string]any{"operations": []map[string]string{{"kind": "create", "path": path, "content": content}}})
	return string(data)
}

var fixedInstallEnv = InstallEnv{OS: "darwin", Arch: "arm64", Shell: "/bin/zsh", StartupFiles: []string{"~/.zshrc", "~/.zprofile"}, PackageManagers: []string{"brew"}}

func TestInstallPromptContainsTheInstallRules(t *testing.T) {
	prompt := installPrompt("nvm posix", fixedInstallEnv)
	if prompt != strings.TrimSpace(prompt) {
		t.Fatalf("install prompt is untrimmed: %q", prompt)
	}
	for _, want := range []string{
		`Trust the "Environment" section`, "do not probe for those again", "which <tool>", "--version",
		"one `ask_user` call using the `questions` form", "one to four",
		"recommended option first", "`recommended`", "one-line reason",
		"If nothing is ambiguous, ask nothing", "follow-up questionnaire only when an answer opens a new choice",
		"use the recommended option and say so", "skips the whole questionnaire, stop without making any",
		"Never ask for passwords, API keys, or tokens",
		"`plan_update`", "every command you will run", "`~/.zshrc`", "assumptions",
		"normal approval", "declines", "confined to the repository",
		"zsh -lc 'nvm --version'",
		"what was installed", "skipped or declined", "opening a new terminal",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("install prompt missing %q", want)
		}
	}
	// The six phases appear in the required order.
	last := -1
	for _, heading := range []string{"## 1. Detect", "## 2. Ask only the gaps", "## 3. Post the plan", "## 4. Execute", "## 5. Verify", "## 6. Summarize", "## Environment (detected by Likha)", "## Install request"} {
		i := strings.Index(prompt, heading)
		if i <= last {
			t.Fatalf("heading %q at %d, after previous at %d", heading, i, last)
		}
		last = i
	}
}

func TestInstallPromptRendersEnvironmentThenTrimmedRequest(t *testing.T) {
	prompt := installPrompt("  postgres 16 for local dev\n\t", fixedInstallEnv)
	want := "\n\n## Environment (detected by Likha)\n\n" +
		"OS: darwin/arm64\n" +
		"Shell: /bin/zsh\n" +
		"Startup files: ~/.zshrc, ~/.zprofile\n" +
		"Package managers on PATH: brew\n\n" +
		"## Install request\n\npostgres 16 for local dev"
	if prompt != strings.TrimSpace(installInstructions)+want {
		t.Fatalf("prompt tail = %q, want %q", prompt[strings.Index(prompt, "\n\n## Environment"):], want)
	}
	if strings.Count(prompt, "## Install request") != 1 || strings.Count(prompt, "## Environment") != 1 {
		t.Fatal("install prompt repeats a section")
	}
}

func TestInstallPromptRendersEmptyListsAsNoneFound(t *testing.T) {
	prompt := installPrompt("nvm", InstallEnv{OS: "linux", Arch: "amd64", Shell: "unknown"})
	for _, want := range []string{"OS: linux/amd64\n", "Shell: unknown\n", "Startup files: none found\n", "Package managers on PATH: none found\n\n## Install request"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

func TestDetectInstallEnvChecksOnlyListedNames(t *testing.T) {
	home := t.TempDir()
	for _, name := range []string{".zshrc", ".profile", ".config/fish/config.fish", ".vimrc"} {
		path := filepath.Join(home, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("secret\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// A directory with a startup-file name is not a startup file.
	if err := os.Mkdir(filepath.Join(home, ".bashrc"), 0o755); err != nil {
		t.Fatal(err)
	}
	var looked []string
	lookPath := func(name string) (string, error) {
		looked = append(looked, name)
		if name == "brew" || name == "nix" {
			return "/usr/bin/" + name, nil
		}
		return "", errors.New("not found")
	}
	env := detectInstallEnv("darwin", "arm64", home, func(string) string { return "" }, lookPath)
	if env.OS != "darwin" || env.Arch != "arm64" || env.Shell != "unknown" {
		t.Fatalf("env identity = %+v", env)
	}
	if got := strings.Join(env.StartupFiles, ","); got != "~/.zshrc,~/.profile,~/.config/fish/config.fish" {
		t.Fatalf("startup files = %s", got)
	}
	if got := strings.Join(env.PackageManagers, ","); got != "brew,nix" {
		t.Fatalf("package managers = %s", got)
	}
	if strings.Join(looked, ",") != strings.Join(installPackageManagers, ",") {
		t.Fatalf("looked up %v, want exactly the fixed list", looked)
	}
	if strings.Contains(env.render(), "secret") {
		t.Fatal("rendered environment leaked file contents")
	}
	shell := detectInstallEnv("linux", "amd64", "", func(k string) string {
		if k == "SHELL" {
			return "/bin/bash"
		}
		return ""
	}, lookPath)
	if shell.Shell != "/bin/bash" || len(shell.StartupFiles) != 0 {
		t.Fatalf("no-home env = %+v", shell)
	}
}

func TestDetectInstallEnvReportsRuntimePlatform(t *testing.T) {
	env := DetectInstallEnv()
	if env.OS != runtime.GOOS || env.Arch != runtime.GOARCH || env.Shell == "" {
		t.Fatalf("DetectInstallEnv = %+v, want %s/%s", env, runtime.GOOS, runtime.GOARCH)
	}
	if !strings.Contains(InstallPrompt("nvm"), "OS: "+runtime.GOOS+"/"+runtime.GOARCH+"\n") {
		t.Fatal("InstallPrompt does not carry the detected environment")
	}
}

func TestInstallGateRefusesChangesBeforePlanWithoutReview(t *testing.T) {
	reviewer := &fakeReviewer{approve: true}
	registry, root := installRegistry(t, RunOptions{InstallMode: true}, reviewer)
	if tier := cmdpolicy.Classify(installAskCommand, root).Tier; tier != cmdpolicy.Ask {
		t.Fatalf("%q tier = %v, test needs an Ask-tier command", installAskCommand, tier)
	}
	cases := []struct{ name, args string }{
		{"run_command", commandArgs(installAskCommand)},
		{"run_command", commandArgs("rm build.log")},
		{"edit_file", editFileArgs(t, "notes.txt", "x\n")},
		{"edit", exactEditArgs("notes.txt", "x\n")},
	}
	for _, c := range cases {
		result, err := invoke(registry, c.name, c.args)
		if modelError(err) != installPlanFirst(c.name) || result.Executed {
			t.Errorf("%s %s before plan: executed=%v err=%v", c.name, c.args, result.Executed, err)
		}
		if !strings.Contains(modelError(err), "/install") || !strings.Contains(modelError(err), "plan_update") {
			t.Errorf("refusal does not name /install and plan_update: %q", modelError(err))
		}
	}
	if len(reviewer.requests) != 0 {
		t.Fatalf("gated calls started %d approval flows", len(reviewer.requests))
	}
	if _, err := os.Stat(filepath.Join(root, "notes.txt")); !os.IsNotExist(err) {
		t.Fatalf("refused edit wrote a file: %v", err)
	}
	// Gated tools stay advertised so the plan can open them mid-turn.
	exposed := map[string]bool{}
	for _, definition := range registry.Definitions(mainScope(tools.Scope{})) {
		exposed[definition.Name] = true
	}
	for _, name := range []string{"run_command", "edit_file", "edit", "plan_update", "ask_user"} {
		if !exposed[name] {
			t.Errorf("%s hidden from the /install model request", name)
		}
	}
}

func TestInstallGateAllowsReadsAndReadOnlyCommandsBeforePlan(t *testing.T) {
	reviewer := &fakeReviewer{approve: true}
	registry, root := installRegistry(t, RunOptions{InstallMode: true}, reviewer)
	if tier := cmdpolicy.Classify(installReadCommand, root).Tier; tier != cmdpolicy.ReadOnly {
		t.Fatalf("%q tier = %v", installReadCommand, tier)
	}
	result, err := invoke(registry, "run_command", commandArgs(installReadCommand))
	if err != nil || !result.Executed || !strings.Contains(result.Content, "auto-approved (read-only inspection)") {
		t.Fatalf("read-only command before plan: %+v %v", result, err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if result, err := invoke(registry, "read", `{"path":"README.md"}`); err != nil || !strings.Contains(result.Content, "hi") {
		t.Fatalf("read before plan: %+v %v", result, err)
	}
	if len(reviewer.requests) != 0 {
		t.Fatalf("read-only work prompted %d times", len(reviewer.requests))
	}
}

func TestInstallGateOpensAfterSuccessfulPlanUpdate(t *testing.T) {
	reviewer := &fakeReviewer{approve: false}
	registry, root := installRegistry(t, RunOptions{InstallMode: true}, reviewer)
	if result, err := invoke(registry, "plan_update", installPlanArgs); err != nil || result.Status != tools.Succeeded {
		t.Fatalf("plan_update: %+v %v", result, err)
	}
	for i, c := range []struct{ name, args, rejected string }{
		{"run_command", commandArgs(installAskCommand), "command rejected by user"},
		{"edit_file", editFileArgs(t, "notes.txt", "x\n"), "edit rejected by user"},
		{"edit", exactEditArgs("notes.txt", "x\n"), "edit rejected by user"},
	} {
		_, err := invoke(registry, c.name, c.args)
		if modelError(err) != c.rejected {
			t.Errorf("%s after plan = %v, want the ordinary review outcome", c.name, err)
		}
		if len(reviewer.requests) != i+1 {
			t.Fatalf("%s after plan: %d approval requests, want %d", c.name, len(reviewer.requests), i+1)
		}
	}
	if kinds := []string{reviewer.requests[0].Kind, reviewer.requests[1].Kind, reviewer.requests[2].Kind}; kinds[0] != "command" || kinds[1] != "edit" || kinds[2] != "edit" {
		t.Fatalf("approval kinds = %v", kinds)
	}
	if _, err := os.Stat(filepath.Join(root, "notes.txt")); !os.IsNotExist(err) {
		t.Fatalf("declined edit wrote a file: %v", err)
	}
}

func TestInstallGateStaysClosedAfterUnsuccessfulPlanUpdate(t *testing.T) {
	failing := func(context.Context, []tools.PlanStep) error { return errors.New("disk full") }
	cases := []struct {
		name, args string
		apply      func(context.Context, []tools.PlanStep) error
	}{
		{"save failure", installPlanArgs, failing},
		{"cleared plan", `{"steps":[]}`, nil},
		{"invalid steps", `{"steps":[{"id":"1","title":"x","status":"done"}]}`, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reviewer := &fakeReviewer{approve: true}
			registry, _ := installRegistry(t, RunOptions{InstallMode: true, PlanApply: c.apply}, reviewer)
			invoke(registry, "plan_update", c.args)
			_, err := invoke(registry, "run_command", commandArgs(installAskCommand))
			if modelError(err) != installPlanFirst("run_command") || len(reviewer.requests) != 0 {
				t.Fatalf("after %s: err=%v approvals=%d", c.name, err, len(reviewer.requests))
			}
		})
	}
}

func TestInstallGateInactiveOutsideInstallMode(t *testing.T) {
	reviewer := &fakeReviewer{approve: false}
	registry, _ := installRegistry(t, RunOptions{}, reviewer)
	if _, err := invoke(registry, "run_command", commandArgs(installAskCommand)); modelError(err) != "command rejected by user" {
		t.Fatalf("Ask-tier command outside /install = %v", err)
	}
	if _, err := invoke(registry, "edit_file", editFileArgs(t, "notes.txt", "x\n")); modelError(err) != "edit rejected by user" {
		t.Fatalf("edit outside /install = %v", err)
	}
	if len(reviewer.requests) != 2 {
		t.Fatalf("approval requests outside /install = %d, want 2", len(reviewer.requests))
	}
}

func TestPlanModeGateWinsOverInstallMode(t *testing.T) {
	reviewer := &fakeReviewer{approve: true}
	registry, _ := installRegistry(t, RunOptions{PlanMode: true, InstallMode: true}, reviewer)
	invoke(registry, "plan_update", installPlanArgs)
	for _, name := range []string{"run_command", "edit_file"} {
		_, err := invoke(registry, name, `{}`)
		if err == nil || !strings.Contains(err.Error(), "plan mode") {
			t.Errorf("%s with plan+install = %v", name, err)
		}
	}
	if len(reviewer.requests) != 0 {
		t.Fatalf("plan mode with install started %d approvals", len(reviewer.requests))
	}
}

func TestInstallGateLeavesMCPToolsUngated(t *testing.T) {
	gate := &installGate{}
	cases := []struct {
		tool tools.Tool
		gate bool
	}{
		{tools.Tool{Definition: model.ToolDefinition{Name: "remote"}, Source: tools.Source{Kind: "mcp"}, Effects: []tools.Effect{tools.Write, tools.Exec}}, false},
		{tools.Tool{Definition: model.ToolDefinition{Name: "edit"}, Source: tools.Source{Kind: "builtin"}, Effects: []tools.Effect{tools.Write}}, true},
		{tools.Tool{Definition: model.ToolDefinition{Name: "edit_file"}, Source: tools.Source{Kind: "builtin"}, Effects: []tools.Effect{tools.Write}}, true},
		{tools.Tool{Definition: model.ToolDefinition{Name: "run_command"}, Source: tools.Source{Kind: "builtin"}, Effects: []tools.Effect{tools.Exec}}, false},
		{tools.Tool{Definition: model.ToolDefinition{Name: "plan_update"}, Source: tools.Source{Kind: "builtin"}}, false},
		{tools.Tool{Definition: model.ToolDefinition{Name: "read"}, Source: tools.Source{Kind: "builtin"}, Effects: []tools.Effect{tools.Read}}, false},
	}
	for _, c := range cases {
		if got := gate.gates(c.tool); got != c.gate {
			t.Errorf("gates(%s/%s) = %v, want %v", c.tool.Source.Kind, c.tool.Definition.Name, got, c.gate)
		}
	}
}

// The plan flag is read while parallel-safe reads run; posting it must be
// race-free (meaningful under go test -race).
func TestInstallGatePlanFlagIsRaceFree(t *testing.T) {
	gate := &installGate{}
	run := gate.wrapPlanUpdate(func(context.Context, json.RawMessage) (tools.Result, error) {
		return tools.Result{Status: tools.Succeeded}, nil
	})
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			gate.commandAllowed(installAskCommand, t.TempDir())
		}()
	}
	if _, err := run(context.Background(), json.RawMessage(installPlanArgs)); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if !gate.commandAllowed(installAskCommand, t.TempDir()) {
		t.Fatal("gate closed after a successful plan")
	}
}

// One /install turn sends the install instructions in the system layer; the
// next ordinary turn does not.
func TestInstallModeTurnRequestCarriesInstructions(t *testing.T) {
	var systems []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveModels(t, w, r) {
			return
		}
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode: %v", err)
		}
		system := ""
		if len(body.Messages) > 0 && body.Messages[0].Role == "system" {
			system = body.Messages[0].Content
		}
		systems = append(systems, system)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	client, err := model.New(server.URL+"/v1", "local-test", "")
	if err != nil {
		t.Fatal(err)
	}
	repo, root := initRepo(t)
	for _, install := range []bool{true, false} {
		RunTurnWithOptions(context.Background(), client, repo, root, nil, InstallPrompt("nvm posix"), nil, nil, RunOptions{InstallMode: install}, func(TurnEvent) {})
	}
	if len(systems) != 2 {
		t.Fatalf("requests = %d", len(systems))
	}
	if !strings.Contains(systems[0], "## /install turn") || !strings.Contains(systems[0], "plan_update") {
		t.Fatalf("/install request lacks install instructions")
	}
	if strings.Contains(systems[1], "## /install turn") {
		t.Fatal("ordinary request kept /install instructions")
	}
}

func TestHarnessPromptCarriesAskingRules(t *testing.T) {
	start := strings.Index(harnessPrompt, "## Asking the user")
	if start < 0 {
		t.Fatal("prompt.md has no Asking the user section")
	}
	section := harnessPrompt[start:]
	if end := strings.Index(section[1:], "\n## "); end >= 0 {
		section = section[:end+1]
	}
	section = strings.Join(strings.Fields(section), " ")
	for _, want := range []string{
		"more than one reasonable reading", "real rework", "hard to undo",
		"Do not ask when the code, project conventions, or a sensible default answer it",
		"state the assumption", "read tools before asking", "one questionnaire of at most four questions",
		"recommended option first", "one-line reason",
		"Never use a question to get permission", "passwords, API keys, or tokens",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("asking rules missing %q", want)
		}
	}
}
