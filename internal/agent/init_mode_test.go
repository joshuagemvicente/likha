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
	"strings"
	"testing"

	"likha/internal/actions"
	"likha/internal/model"
	"likha/internal/repository"
	"likha/internal/tools"
)

func initRepo(t *testing.T) (*repository.Repository, string) {
	t.Helper()
	repo, err := repository.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return repo, repo.Root()
}

func initRegistry(t *testing.T, options RunOptions, reviewer *fakeReviewer) (*tools.Registry, string) {
	t.Helper()
	repo, root := initRepo(t)
	if options.EditJournal == nil {
		options.EditJournal = func(*actions.EditProposal) error { return nil }
	}
	registry, warnings := newToolRegistry(repo, root, nil, options, reviewer.emit)
	if len(warnings) != 0 {
		t.Fatalf("registration warnings: %v", warnings)
	}
	return registry, root
}

// modelError is the builtin refusal text the model sees: the authorizer's
// cause, not the registry's generic wrapper.
func modelError(err error) string {
	var callErr *tools.CallError
	if errors.As(err, &callErr) && callErr.Cause != nil {
		return callErr.Cause.Error()
	}
	if err == nil {
		return ""
	}
	return err.Error()
}

func editFileArgs(t *testing.T, path, content string) string {
	t.Helper()
	data, err := json.Marshal(map[string]string{"path": path, "content": content})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestInitPromptWithoutGuidance(t *testing.T) {
	prompt := InitPrompt("")
	if prompt != strings.TrimSpace(prompt) || !strings.Contains(prompt, "AGENTS.md") || !strings.Contains(prompt, "edit_file") {
		t.Fatalf("survey prompt missing essentials or untrimmed: %q", prompt)
	}
	if strings.Contains(prompt, "## User guidance") {
		t.Fatalf("prompt without guidance has a guidance section: %q", prompt)
	}
	if InitPrompt("  \n\t ") != prompt {
		t.Fatal("whitespace-only guidance changed the prompt")
	}
}

func TestInitPromptAppendsGuidance(t *testing.T) {
	prompt := InitPrompt("  focus on the test layout\n")
	want := InitPrompt("") + "\n\n## User guidance\n\nfocus on the test layout"
	if prompt != want {
		t.Fatalf("guidance prompt = %q, want suffix %q", prompt[len(prompt)-60:], want[len(want)-60:])
	}
}

func TestInitModeGatesExecAndOtherWriteTools(t *testing.T) {
	reviewer := &fakeReviewer{approve: true}
	registry, _ := initRegistry(t, RunOptions{InitMode: true}, reviewer)
	exposed := map[string]bool{}
	for _, definition := range registry.Definitions(mainScope(tools.Scope{})) {
		exposed[definition.Name] = true
	}
	for _, name := range []string{"read", "glob", "grep", "edit_file"} {
		if !exposed[name] {
			t.Errorf("%s hidden from the /init model request", name)
		}
	}
	for _, name := range []string{"run_command", "edit"} {
		if exposed[name] {
			t.Errorf("%s exposed during /init", name)
		}
		_, err := registry.Invoke(context.Background(), mainScope(tools.Scope{}), toolCall(name, `{}`))
		if err == nil || !strings.Contains(err.Error(), "refused (/init): "+name+" is unavailable during the /init survey") {
			t.Errorf("%s refusal = %v", name, err)
		}
	}
	if len(reviewer.requests) != 0 {
		t.Fatalf("gated tools started %d approval flows", len(reviewer.requests))
	}
	repo, root := initRepo(t)
	catalog := ToolCatalog(repo, root, nil, RunOptions{InitMode: true})
	found := false
	for _, entry := range catalog {
		if entry.Name == "run_command" {
			found = true
			if entry.Available || !strings.Contains(entry.Reason, "refused (/init)") {
				t.Fatalf("catalog run_command = %+v", entry)
			}
		}
		if (entry.Name == "ask_user" || entry.Name == "read" || entry.Name == "task") && strings.Contains(entry.Reason, "/init") {
			t.Fatalf("read-only tool gated by /init: %+v", entry)
		}
	}
	if !found {
		t.Fatal("run_command missing from /init catalog")
	}
}

func TestInitModeGatesClassification(t *testing.T) {
	cases := []struct {
		tool tools.Tool
		gate bool
	}{
		{tools.Tool{Definition: model.ToolDefinition{Name: "remote"}, Source: tools.Source{Kind: "mcp"}, Effects: []tools.Effect{tools.Read}}, true},
		{tools.Tool{Definition: model.ToolDefinition{Name: "run_command"}, Effects: []tools.Effect{tools.Exec}}, true},
		{tools.Tool{Definition: model.ToolDefinition{Name: "edit"}, Effects: []tools.Effect{tools.Write}}, true},
		{tools.Tool{Definition: model.ToolDefinition{Name: "edit_file"}, Effects: []tools.Effect{tools.Write}}, false},
		{tools.Tool{Definition: model.ToolDefinition{Name: "read"}, Effects: []tools.Effect{tools.Read}}, false},
		{tools.Tool{Definition: model.ToolDefinition{Name: "web_fetch"}, Effects: []tools.Effect{tools.Network}}, false},
	}
	for _, c := range cases {
		if got := initModeGates(c.tool); got != c.gate {
			t.Errorf("initModeGates(%s/%s) = %v, want %v", c.tool.Source.Kind, c.tool.Definition.Name, got, c.gate)
		}
	}
}

func TestPlanModeGateWinsOverInitMode(t *testing.T) {
	reviewer := &fakeReviewer{approve: true}
	registry, _ := initRegistry(t, RunOptions{PlanMode: true, InitMode: true}, reviewer)
	for _, name := range []string{"run_command", "edit_file"} {
		_, err := registry.Invoke(context.Background(), mainScope(tools.Scope{}), toolCall(name, `{}`))
		if err == nil || !strings.Contains(err.Error(), "plan mode") {
			t.Errorf("%s with plan+init = %v", name, err)
		}
	}
}

func TestInitModeRefusesEditFileOutsideRootAgents(t *testing.T) {
	reviewer := &fakeReviewer{approve: true}
	registry, root := initRegistry(t, RunOptions{InitMode: true}, reviewer)
	for _, path := range []string{"README.md", "docs/AGENTS.md", "agents.md", "AGENTS.md/x", filepath.Join(root, "other", "AGENTS.md")} {
		_, err := registry.Invoke(context.Background(), mainScope(tools.Scope{}), toolCall("edit_file", editFileArgs(t, path, "# x\n")))
		want := "refused (/init): edit_file may only propose the repository-root AGENTS.md during /init; got " + path
		if modelError(err) != want {
			t.Errorf("edit_file %q in /init = %v", path, err)
		}
	}
	if len(reviewer.requests) != 0 {
		t.Fatalf("refused /init edits emitted %d approvals", len(reviewer.requests))
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("refused edits touched the repository: %v %v", entries, err)
	}
}

func TestInitModeEditFileProposesRootAgents(t *testing.T) {
	for _, form := range []string{"AGENTS.md", "./AGENTS.md", "absolute"} {
		t.Run(form, func(t *testing.T) {
			reviewer := &fakeReviewer{approve: true}
			registry, root := initRegistry(t, RunOptions{InitMode: true}, reviewer)
			path := form
			if form == "absolute" {
				path = filepath.Join(root, "AGENTS.md")
			}
			_, err := registry.Invoke(context.Background(), mainScope(tools.Scope{}), toolCall("edit_file", editFileArgs(t, path, "# Agents\n")))
			if err != nil {
				t.Fatal(err)
			}
			if len(reviewer.requests) != 1 || reviewer.requests[0].Kind != "edit" || reviewer.requests[0].Title != "Edit: AGENTS.md" || reviewer.requests[0].Warning != "" {
				t.Fatalf("approval requests = %+v", reviewer.requests)
			}
			content, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
			if err != nil || string(content) != "# Agents\n" {
				t.Fatalf("approved AGENTS.md = %q, %v", content, err)
			}
		})
	}
}

func TestInitModeRejectedAgentsProposalWritesNothing(t *testing.T) {
	reviewer := &fakeReviewer{approve: false}
	registry, root := initRegistry(t, RunOptions{InitMode: true}, reviewer)
	_, err := registry.Invoke(context.Background(), mainScope(tools.Scope{}), toolCall("edit_file", editFileArgs(t, "AGENTS.md", "# Agents\n")))
	if modelError(err) != "edit rejected by user" || len(reviewer.requests) != 1 {
		t.Fatalf("rejected proposal: err=%v requests=%d", err, len(reviewer.requests))
	}
	if _, err := os.Stat(filepath.Join(root, "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatalf("rejected proposal wrote AGENTS.md: %v", err)
	}
}

func TestOversizedRootAgentsProposalWarns(t *testing.T) {
	for _, mode := range []RunOptions{{InitMode: true}, {}} {
		reviewer := &fakeReviewer{approve: false}
		registry, _ := initRegistry(t, mode, reviewer)
		content := strings.Repeat("a\n", 16385) // 32770 bytes
		registry.Invoke(context.Background(), mainScope(tools.Scope{}), toolCall("edit_file", editFileArgs(t, "AGENTS.md", content)))
		want := "AGENTS.md is 32770 bytes; the harness ignores root instructions over 32 KiB (32768 bytes), so this file would not be loaded."
		if len(reviewer.requests) != 1 || reviewer.requests[0].Warning != want {
			t.Fatalf("init=%v oversized warning = %+v", mode.InitMode, reviewer.requests)
		}
	}
}

func TestRootAgentsAtLimitHasNoWarning(t *testing.T) {
	reviewer := &fakeReviewer{approve: false}
	registry, _ := initRegistry(t, RunOptions{InitMode: true}, reviewer)
	content := strings.Repeat("a\n", 16384) // exactly 32768 bytes
	registry.Invoke(context.Background(), mainScope(tools.Scope{}), toolCall("edit_file", editFileArgs(t, "AGENTS.md", content)))
	if len(reviewer.requests) != 1 || reviewer.requests[0].Warning != "" {
		t.Fatalf("at-limit proposal = %+v", reviewer.requests)
	}
}

func TestOrdinaryEditsUnaffectedOutsideInit(t *testing.T) {
	reviewer := &fakeReviewer{approve: true}
	registry, root := initRegistry(t, RunOptions{}, reviewer)
	large := strings.Repeat("a\n", 20000)
	for _, path := range []string{"README.md", "docs/AGENTS.md"} {
		if err := os.MkdirAll(filepath.Join(root, filepath.Dir(path)), 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := registry.Invoke(context.Background(), mainScope(tools.Scope{}), toolCall("edit_file", editFileArgs(t, path, large))); err != nil {
			t.Fatalf("edit_file %s outside /init: %v", path, err)
		}
	}
	if len(reviewer.requests) != 2 || reviewer.requests[0].Warning != "" || reviewer.requests[1].Warning != "" {
		t.Fatalf("non-root edits carried warnings: %+v", reviewer.requests)
	}
	if _, err := registry.Invoke(context.Background(), mainScope(tools.Scope{}), toolCall("run_command", `{"command":"git status"}`)); err != nil && strings.Contains(err.Error(), "/init") {
		t.Fatalf("run_command refused by /init outside /init: %v", err)
	}
}

func TestExactEditWarnsWhenRootAgentsGrowsPastLimit(t *testing.T) {
	reviewer := &fakeReviewer{approve: false}
	registry, root := initRegistry(t, RunOptions{}, reviewer)
	base := "# Agents\nmarker\n"
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}
	invoke := func(newText string) {
		ops := []actions.EditOperation{{Kind: "replace", Path: "AGENTS.md", OldText: "marker\n", NewText: newText}}
		data, _ := json.Marshal(map[string]any{"operations": ops})
		registry.Invoke(context.Background(), mainScope(tools.Scope{}), toolCall("edit", string(data)))
	}
	invoke("small\n")
	grown := strings.Repeat("b", 32768)
	invoke(grown)
	if len(reviewer.requests) != 2 {
		t.Fatalf("exact edit approvals = %d", len(reviewer.requests))
	}
	if reviewer.requests[0].Warning != "" {
		t.Fatalf("small exact edit warned: %q", reviewer.requests[0].Warning)
	}
	size := len(base) - len("marker\n") + len(grown)
	want := fmt.Sprintf("AGENTS.md is %d bytes; the harness ignores root instructions over 32 KiB (32768 bytes), so this file would not be loaded.", size)
	if reviewer.requests[1].Warning != want {
		t.Fatalf("grown exact edit warning = %q, want %q", reviewer.requests[1].Warning, want)
	}
}

// One /init turn sends the init instructions in the system layer and hides
// run_command from the model; the next ordinary turn has neither.
func TestInitModeTurnRequestCarriesLimits(t *testing.T) {
	type captured struct {
		system string
		tools  []string
	}
	var requests []captured
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveModels(t, w, r) {
			return
		}
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
			Tools []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode: %v", err)
		}
		var c captured
		if len(body.Messages) > 0 && body.Messages[0].Role == "system" {
			c.system = body.Messages[0].Content
		}
		for _, tool := range body.Tools {
			c.tools = append(c.tools, tool.Function.Name)
		}
		requests = append(requests, c)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	client, err := model.New(server.URL+"/v1", "local-test", "")
	if err != nil {
		t.Fatal(err)
	}
	repo, root := initRepo(t)
	for _, init := range []bool{true, false} {
		RunTurnWithOptions(context.Background(), client, repo, root, nil, InitPrompt(""), nil, nil, RunOptions{InitMode: init}, func(TurnEvent) {})
	}
	if len(requests) != 2 {
		t.Fatalf("requests = %d", len(requests))
	}
	has := func(names []string, name string) bool {
		for _, n := range names {
			if n == name {
				return true
			}
		}
		return false
	}
	if !strings.Contains(requests[0].system, "## /init survey") || has(requests[0].tools, "run_command") || !has(requests[0].tools, "edit_file") {
		t.Fatalf("/init request: tools=%v system has init=%v", requests[0].tools, strings.Contains(requests[0].system, "## /init survey"))
	}
	if strings.Contains(requests[1].system, "## /init survey") || !has(requests[1].tools, "run_command") {
		t.Fatalf("ordinary request kept /init limits: tools=%v", requests[1].tools)
	}
}
