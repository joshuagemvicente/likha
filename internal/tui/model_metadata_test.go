package tui

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"likha/internal/agent"
	"likha/internal/explore"
	"likha/internal/model"
	"likha/internal/providers"
	"likha/internal/repository"
	"likha/internal/session"
)

// spendTestUI is a main-screen UI with a live run already open, so tests can
// feed run events straight into Update. The session is not fresh, so no
// auto-naming request is ever priced behind the test's back.
func spendTestUI(t *testing.T, provider, modelID string) *ui {
	t.Helper()
	m := NewUI("/sample", nil, nil, modelID, providers.Connection{Provider: provider, ProviderCanonical: provider, Verified: true}, t.TempDir(), nil, session.Snapshot{ID: "spend", Entries: []session.Entry{{Role: "You", Content: "seed"}}})
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 24})
	m.working, m.runID = true, 1
	m.runProvider, m.runModel = provider, modelID
	m.cancel = func() {}
	m.events = make(chan agent.TurnEvent, 1)
	return m
}

func usageEvent(runID uint64, u model.RequestUsage) agent.TurnEvent {
	return agent.TurnEvent{RunID: runID, Kind: "usage", Usage: &u}
}

func approxEqual(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// Every model request of a multi-round tool run reaches spend and the token
// totals exactly once: the tool round and the final answer both count.
func TestSpendCountsEveryRequestOfAMultiRoundRun(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "known.txt"), []byte("the answer is 42"), 0600); err != nil {
		t.Fatal(err)
	}
	repo, err := repository.New(root)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveModels(t, w, r) {
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if calls.Add(1) == 1 {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"read\",\"arguments\":\"{\\\"path\\\":\\\"known.txt\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n")
			fmt.Fprint(w, "data: {\"usage\":{\"prompt_tokens\":100000,\"completion_tokens\":2000}}\n\ndata: [DONE]\n\n")
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"It is 42.\"},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: {\"usage\":{\"prompt_tokens\":120000,\"completion_tokens\":1000}}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	client, err := model.New(server.URL+"/v1", "gpt-4o", "")
	if err != nil {
		t.Fatal(err)
	}
	m := NewUI(root, repo, client, "gpt-4o", providers.Connection{Provider: "OpenAI", ProviderCanonical: "openai", Verified: true}, t.TempDir(), nil, session.Snapshot{ID: "multi", Entries: []session.Entry{{Role: "You", Content: "seed"}}})
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 24})
	t.Cleanup(func() {
		if m.cancel != nil {
			m.cancel()
		}
	})
	m.startTurn("find the answer", nil)
	steerE2Edrive(t, m)

	if got := calls.Load(); got != 2 {
		t.Fatalf("model requests = %d, want 2", got)
	}
	if !m.usageSeen || m.usagePrompt != 220_000 || m.usageCompletion != 3000 || m.lastPromptTokens != 120_000 {
		t.Fatalf("token totals = prompt %d completion %d last %d (seen=%t), want 220000/3000/120000", m.usagePrompt, m.usageCompletion, m.lastPromptTokens, m.usageSeen)
	}
	// openai/gpt-4o at $2.5/$10 per 1M: round 1 $0.27, round 2 $0.31.
	if !approxEqual(m.spend, 0.58) || !m.spendKnown || !m.spendEstimated {
		t.Fatalf("spend = %v (known=%t estimated=%t), want ~0.58", m.spend, m.spendKnown, m.spendEstimated)
	}
	if got := m.spendSegment(); got != "~$0.58" {
		t.Fatalf("spend segment = %q, want ~$0.58", got)
	}
}

// Several usage events of one run all add up; the run end adds nothing more.
func TestSpendAddsEveryUsageEventOfTheRun(t *testing.T) {
	m := spendTestUI(t, "openai", "gpt-4o")
	for range 3 {
		m.Update(usageEvent(1, model.RequestUsage{Prompt: 40_000, Completion: 1000, PromptSeen: true, CompletionSeen: true}))
	}
	m.Update(agent.TurnEvent{RunID: 1, Kind: "done"})
	if m.usagePrompt != 120_000 || m.usageCompletion != 3000 {
		t.Fatalf("tokens = %d/%d, want 120000/3000", m.usagePrompt, m.usageCompletion)
	}
	if !approxEqual(m.spend, 0.33) || m.spendSegment() != "~$0.33" {
		t.Fatalf("spend = %v %q, want ~$0.33", m.spend, m.spendSegment())
	}
}

// Usage from a stale run never reaches the session totals.
func TestSpendIgnoresUsageFromAnotherRun(t *testing.T) {
	m := spendTestUI(t, "openai", "gpt-4o")
	m.Update(usageEvent(0, model.RequestUsage{Prompt: 40_000, Completion: 1000, PromptSeen: true, CompletionSeen: true}))
	if m.usageSeen || m.spendKnown || m.usagePrompt != 0 {
		t.Fatalf("stale run usage counted: seen=%t known=%t prompt=%d", m.usageSeen, m.spendKnown, m.usagePrompt)
	}
}

// A provider-reported cost is used exactly and keeps spend unmarked; one
// catalog estimate afterwards marks the whole session "~".
func TestSpendReportedCostIsExactAndMixedIsMarked(t *testing.T) {
	m := spendTestUI(t, "openrouter", "openai/gpt-4o")
	m.Update(usageEvent(1, model.RequestUsage{Prompt: 40_000, Completion: 1000, PromptSeen: true, CompletionSeen: true, Cost: 0.4242, CostSeen: true}))
	if !approxEqual(m.spend, 0.4242) || m.spendEstimated {
		t.Fatalf("reported cost = %v (estimated=%t), want exactly 0.4242", m.spend, m.spendEstimated)
	}
	if got := m.spendSegment(); got != "$0.42" {
		t.Fatalf("exact spend segment = %q, want $0.42", got)
	}
	if row := stripANSI(m.statusLineRows(1, 1)[0]); !strings.Contains(row, "$0.42") || strings.Contains(row, "~$") {
		t.Fatalf("exact spend missing or marked in the row: %q", row)
	}
	// The same pair without a reported cost is priced from the catalog
	// ($2.5/$10): +$0.11, an estimate.
	m.Update(usageEvent(1, model.RequestUsage{Prompt: 40_000, Completion: 1000, PromptSeen: true, CompletionSeen: true}))
	if !approxEqual(m.spend, 0.5342) || !m.spendEstimated {
		t.Fatalf("mixed spend = %v (estimated=%t), want 0.5342 estimated", m.spend, m.spendEstimated)
	}
	if got := m.spendSegment(); got != "~$0.53" {
		t.Fatalf("mixed spend segment = %q, want ~$0.53", got)
	}
	if row := stripANSI(m.statusLineRows(1, 1)[0]); !strings.Contains(row, "~$0.53") {
		t.Fatalf("mixed spend missing from the row: %q", row)
	}
}

// Cached prompt tokens are priced at the cache-read rate, not as input.
func TestSpendPricesCachedTokensAtCacheRate(t *testing.T) {
	m := spendTestUI(t, "openai", "gpt-4o")
	// 40k prompt of which 32k cached: 8k*$2.5 + 32k*$1.25 + 1k*$10 per 1M.
	m.Update(usageEvent(1, model.RequestUsage{Prompt: 40_000, Completion: 1000, PromptSeen: true, CompletionSeen: true, CacheRead: 32_000}))
	if !approxEqual(m.spend, 0.07) {
		t.Fatalf("cached spend = %v, want 0.07", m.spend)
	}
}

func TestSpendSegmentMarkers(t *testing.T) {
	m := spendTestUI(t, "openai", "gpt-4o")
	for _, tc := range []struct {
		known, estimated bool
		spend            float64
		want             string
	}{
		{false, false, 0, ""},
		{false, true, 1, ""},
		{true, false, 0.42, "$0.42"},
		{true, true, 0.42, "~$0.42"},
		{true, false, 0, "$0.00"},
	} {
		m.spendKnown, m.spendEstimated, m.spend = tc.known, tc.estimated, tc.spend
		if got := m.spendSegment(); got != tc.want {
			t.Fatalf("spend segment (known=%t estimated=%t %v) = %q, want %q", tc.known, tc.estimated, tc.spend, got, tc.want)
		}
	}
}

// ChatGPT plan usage cannot establish a charge without provider-reported cost;
// an unpriced model keeps spend hidden while its tokens still count.
func TestSpendSubscriptionAndUnknownPricing(t *testing.T) {
	m := spendTestUI(t, "chatgpt", "gpt-5.5")
	m.Update(usageEvent(1, model.RequestUsage{Prompt: 40_000, Completion: 1000, PromptSeen: true, CompletionSeen: true}))
	if got := m.spendSegment(); got != "" || m.spendKnown {
		t.Fatalf("subscription inferred a charge = %q (known=%t)", got, m.spendKnown)
	}
	m.Update(usageEvent(1, model.RequestUsage{Prompt: 100, Completion: 10, PromptSeen: true, CompletionSeen: true, Cost: 0.25, CostSeen: true}))
	if got := m.spendSegment(); got != "$0.25" || m.spendEstimated {
		t.Fatalf("subscription reported credits = %q (estimated=%t), want $0.25", got, m.spendEstimated)
	}

	m = spendTestUI(t, "openai", "mystery-model")
	m.Update(usageEvent(1, model.RequestUsage{Prompt: 40_000, Completion: 1000, PromptSeen: true, CompletionSeen: true}))
	if m.spendKnown || m.spendSegment() != "" {
		t.Fatalf("unknown pricing fabricated spend: %q", m.spendSegment())
	}
	if !m.usageSeen || m.usagePrompt != 40_000 {
		t.Fatalf("unknown pricing dropped tokens: seen=%t prompt=%d", m.usageSeen, m.usagePrompt)
	}
	if row := stripANSI(m.statusLineRows(1, 1)[0]); strings.Contains(row, "$") {
		t.Fatalf("unknown pricing rendered spend: %q", row)
	}

	// The same model ID on another provider never borrows a price.
	m = spendTestUI(t, "groq", "claude-opus-5-5")
	m.Update(usageEvent(1, model.RequestUsage{Prompt: 40_000, Completion: 1000, PromptSeen: true, CompletionSeen: true}))
	if m.spendKnown {
		t.Fatalf("claude-opus-5-5 on groq borrowed a price: %q", m.spendSegment())
	}
}

// A run is priced with the provider and model it started with, even if
// the live selection changes before its usage arrives.
func TestSpendPricesWithTheRunsModel(t *testing.T) {
	m := spendTestUI(t, "openai", "gpt-4o")
	m.conn.ProviderCanonical, m.modelName = "openai", "mystery-model"
	m.Update(usageEvent(1, model.RequestUsage{Prompt: 40_000, Completion: 1000, PromptSeen: true, CompletionSeen: true}))
	if !approxEqual(m.spend, 0.11) {
		t.Fatalf("spend = %v, want 0.11 priced as openai/gpt-4o", m.spend)
	}
}

// The compaction summarize request is billed, and so is one whose summary
// was rejected after the request completed.
func TestCompactionUsageIsCounted(t *testing.T) {
	for _, tc := range []struct {
		name, content string
		wantErr       bool
	}{
		{"accepted", "A short brief.", false},
		{"rejected blank summary", "   ", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if serveModels(t, w, r) {
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				if tc.content != "" {
					fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", tc.content)
				}
				fmt.Fprint(w, "data: {\"usage\":{\"prompt_tokens\":40000,\"completion_tokens\":1000}}\n\ndata: [DONE]\n\n")
			}))
			defer server.Close()
			client, err := model.New(server.URL+"/v1", "gpt-4o", "")
			if err != nil {
				t.Fatal(err)
			}
			m := NewUI("/sample", nil, client, "gpt-4o", providers.Connection{Provider: "OpenAI", ProviderCanonical: "openai", Verified: true}, t.TempDir(), nil, session.Snapshot{ID: "compact", Entries: []session.Entry{{Role: "You", Content: "seed"}}})
			m.Update(tea.WindowSizeMsg{Width: 160, Height: 24})
			m.history = []model.Message{{Role: "user", Content: "hello"}, {Role: "assistant", Content: "hi"}}
			m.startCompaction("")
			steerE2Edrive(t, m)
			if failed := m.status == "Error"; failed != tc.wantErr {
				t.Fatalf("compaction status = %q, want error=%t", m.status, tc.wantErr)
			}
			if m.usagePrompt != 40_000 || m.usageCompletion != 1000 || m.spendSegment() != "~$0.11" {
				t.Fatalf("compaction usage = %d/%d spend %q, want 40000/1000 ~$0.11", m.usagePrompt, m.usageCompletion, m.spendSegment())
			}
		})
	}
}

// The auto-naming request after the first turn is billed with the model it
// was sent to, and a rejected name still counts once its request completed.
func TestSessionNamingUsageIsCounted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveModels(t, w, r) {
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"answered\"}}]}\n\ndata: {\"usage\":{\"prompt_tokens\":40000,\"completion_tokens\":1000}}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	client, err := model.New(server.URL+"/v1", "gpt-4o", "")
	if err != nil {
		t.Fatal(err)
	}
	m := NewUI("/sample", nil, client, "gpt-4o", providers.Connection{Provider: "OpenAI", ProviderCanonical: "openai", Verified: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 24})
	m.startTurn("first question", nil)
	defer m.cancel()
	defer close(m.abandon)
	driveTurn(m) // runs the turn, then the auto-naming command
	if !m.nameTried {
		t.Fatal("auto-naming request was not launched")
	}
	if m.usagePrompt != 80_000 || m.spendSegment() != "~$0.22" {
		t.Fatalf("turn + naming = %d prompt tokens, spend %q; want 80000 and ~$0.22", m.usagePrompt, m.spendSegment())
	}

	m.Update(nameGeneratedMsg{err: errors.New("model returned an empty session name"), usageOK: true, provider: "openai", model: "gpt-4o",
		usage: model.RequestUsage{Prompt: 40_000, Completion: 1000, PromptSeen: true, CompletionSeen: true}})
	if m.spendSegment() != "~$0.33" {
		t.Fatalf("rejected naming request not billed: %q", m.spendSegment())
	}
	m.Update(nameGeneratedMsg{err: errors.New("timeout")})
	if m.spendSegment() != "~$0.33" || m.usagePrompt != 120_000 {
		t.Fatalf("naming request without usage changed totals: %q %d", m.spendSegment(), m.usagePrompt)
	}
}

func TestCompactPrice(t *testing.T) {
	for in, want := range map[float64]string{4: "4", 20: "20", 0.15: "0.15", 2.5: "2.5", 0.075: "0.075", 0.6: "0.6", 1.23456: "1.2346", 0: "0"} {
		if got := compactPrice(in); got != want {
			t.Errorf("compactPrice(%v) = %q, want %q", in, got, want)
		}
	}
	if got := modelPriceLabel("claude", "claude-opus-5-5"); got != "$4/$20" {
		t.Fatalf("claude-opus-5-5 price = %q, want $4/$20", got)
	}
	for _, pair := range [][2]string{{"chatgpt", "gpt-5.5"}, {"openai", "mystery-model"}, {"groq", "claude-opus-5-5"}} {
		if got := modelPriceLabel(pair[0], pair[1]); got != "" {
			t.Fatalf("%s/%s price = %q, want none", pair[0], pair[1], got)
		}
	}
}

func modelsDialogUI(t *testing.T, width int) *ui {
	t.Helper()
	m := NewUI("/sample", nil, nil, "gpt-4o", providers.Connection{Provider: "OpenAI", ProviderCanonical: "openai", Verified: true}, "", nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
	openai := model.Provider{Name: "openai", DisplayName: "OpenAI"}
	claude := model.Provider{Name: "claude", DisplayName: "Anthropic Claude"}
	chatgpt := model.Provider{Name: "chatgpt", DisplayName: "ChatGPT (Plus/Pro)"}
	m.dialog = dialogState{kind: dialogModels, open: true, cursor: 1}
	m.dialogModelRows = []modelsRow{
		{provider: openai, model: "gpt-4o"},
		{provider: openai, model: "gpt-4o-mini", contextWindow: 300_000}, // live metadata wins over the catalog
		{provider: openai, model: "mystery-model"},
		{provider: claude, model: "claude-opus-5-5"},
		{provider: chatgpt, model: "gpt-5.5"},
	}
	m.rebuild()
	return m
}

func dialogRowWith(t *testing.T, view, needle string) string {
	t.Helper()
	for _, row := range strings.Split(stripANSI(view), "\n") {
		if strings.Contains(row, needle) {
			return row
		}
	}
	t.Fatalf("no dialog row contains %q:\n%s", needle, stripANSI(view))
	return ""
}

// /models rows show known context and price per 1M right-aligned after the
// model; unknown parts are omitted.
func TestModelsDialogRowsShowContextAndPrice(t *testing.T) {
	m := modelsDialogUI(t, 120)
	view := m.dialogView()
	for needle, want := range map[string]string{
		"gpt-4o (current)": "128k ctx · $2.5/$10",
		"gpt-4o-mini":      "300k ctx · $0.15/$0.6",
		"claude-opus-5-5":  "1M ctx · $4/$20",
	} {
		row := dialogRowWith(t, view, needle)
		if !strings.Contains(row, want) {
			t.Fatalf("row %q lacks %q: %q", needle, want, row)
		}
		// Right-aligned: the details close the row's content, right before
		// the box border.
		if !strings.HasSuffix(strings.TrimRight(strings.TrimSuffix(strings.TrimRight(row, " "), "│"), " "), want) {
			t.Fatalf("details of %q are not right-aligned: %q", needle, row)
		}
	}
	for _, needle := range []string{"mystery-model", "gpt-5.5"} {
		if row := dialogRowWith(t, view, needle); strings.Contains(row, "ctx") || strings.Contains(row, "$") {
			t.Fatalf("unknown metadata rendered for %q: %q", needle, row)
		}
	}
	for i, row := range strings.Split(view, "\n") {
		if w := runewidth.StringWidth(stripANSI(row)); w > 120 {
			t.Fatalf("row %d is %d cells wide at 120 columns", i, w)
		}
	}
}

// At narrow widths the details give way before the model name, and no row
// overflows the viewport.
func TestModelsDialogRowsDegradeAtNarrowWidths(t *testing.T) {
	for _, width := range []int{24, 30, 36, 44, 60} {
		m := modelsDialogUI(t, width)
		view := m.dialogView()
		for i, row := range strings.Split(view, "\n") {
			if w := runewidth.StringWidth(stripANSI(row)); w > width {
				t.Fatalf("width %d: row %d is %d cells wide: %q", width, i, w, stripANSI(row))
			}
		}
		row := dialogRowWith(t, view, "> ")
		if !strings.Contains(row, "gpt-4o-mi") {
			t.Fatalf("width %d: cursor row lost its model name: %q", width, row)
		}
	}
	// 36 columns: the price still fits after the context gives way; at
	// 26 both are gone and the model name stays whole.
	row := dialogRowWith(t, modelsDialogUI(t, 36).dialogView(), "claude-opus-5-5")
	if strings.Contains(row, "ctx") || !strings.Contains(row, "$4/$20") {
		t.Fatalf("36 columns: want the price alone after the context gave way: %q", row)
	}
	row = dialogRowWith(t, modelsDialogUI(t, 26).dialogView(), "claude-opus-5-5")
	if strings.Contains(row, "ctx") || strings.Contains(row, "$") {
		t.Fatalf("26 columns: details kept over the model name: %q", row)
	}
}

func setupModelUI(t *testing.T, width int, providerName string, models []string, windows map[string]int64) *ui {
	t.Helper()
	m := NewUI("/sample", nil, nil, "", providers.Connection{Setup: true}, t.TempDir(), nil, session.Snapshot{})
	m.Update(tea.WindowSizeMsg{Width: width, Height: 30})
	m.setup.cursor = -1
	for i, p := range model.Providers {
		if p.Name == providerName {
			m.setup.cursor = i
		}
	}
	if m.setup.cursor < 0 {
		t.Fatalf("provider %q not found", providerName)
	}
	m.setup.stage = setupModel
	m.setup.models = models
	m.setup.contextWindows = windows
	return m
}

// Setup model rows show context (live, else catalog), price, and the
// recommended marker; details drop from the front on narrow rows.
func TestSetupModelRowsShowContextAndPrice(t *testing.T) {
	m := setupModelUI(t, 120, "claude", []string{"claude-opus-5-5", "claude-haiku-4-5", "claude-mystery"}, map[string]int64{"claude-haiku-4-5": 150_000})
	view := stripANSI(m.View())
	assertViewport(t, m.View(), 120, 30)
	opus := dialogRowWith(t, view, "claude-opus-5-5")
	for _, want := range []string{"1M context", "$4/$20", "recommended"} {
		if !strings.Contains(opus, want) {
			t.Fatalf("opus row lacks %q: %q", want, opus)
		}
	}
	if !(strings.Index(opus, "1M context") < strings.Index(opus, "$4/$20") && strings.Index(opus, "$4/$20") < strings.Index(opus, "recommended")) {
		t.Fatalf("opus details out of order: %q", opus)
	}
	haiku := dialogRowWith(t, view, "claude-haiku-4-5")
	if !strings.Contains(haiku, "150k context") || !strings.Contains(haiku, "$1/$5") {
		t.Fatalf("haiku row lacks live context or price: %q", haiku)
	}
	if mystery := dialogRowWith(t, view, "claude-mystery"); strings.Contains(mystery, "context") || strings.Contains(mystery, "$") {
		t.Fatalf("unknown model rendered metadata: %q", mystery)
	}

	for _, width := range []int{40, 50, 60} {
		m := setupModelUI(t, width, "claude", []string{"claude-opus-5-5"}, nil)
		assertViewport(t, m.View(), width, 30)
		row := dialogRowWith(t, stripANSI(m.View()), "claude-opus")
		if strings.Contains(row, "1M context") && !strings.Contains(row, "recommended") {
			t.Fatalf("width %d: the back detail dropped before the front one: %q", width, row)
		}
	}
}

// childTaskRecords runs one explore child on provider/modelID that makes the
// given requests and returns every record update the runtime emitted, in
// order, as the agent loop forwards them to the UI as "task" events.
func childTaskRecords(t *testing.T, sessionID, provider, modelID string, requests ...model.RequestUsage) []explore.Record {
	t.Helper()
	var records []explore.Record
	manager, err := explore.New(context.Background(), explore.Config{
		SessionID: sessionID, Root: t.TempDir(), Provider: provider, Model: modelID,
		Runner: func(_ context.Context, n *explore.Node) explore.Outcome {
			for _, u := range requests {
				if err := n.BeginRequest(); err != nil {
					return explore.Outcome{Status: explore.Failed}
				}
				n.RecordRequest(u, true)
			}
			return explore.Outcome{Findings: "done"}
		},
		OnUpdate: func(record explore.Record) error {
			records = append(records, record)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Spawn(context.Background(), "", "call_task", explore.Spec{Agent: "explore", Description: "measure", Prompt: "measure"}); err != nil {
		t.Fatal(err)
	}
	manager.Wait()
	return records
}

func childTaskEvent(runID uint64, record explore.Record) agent.TurnEvent {
	return agent.TurnEvent{RunID: runID, Kind: "task", Task: &record}
}

// A run that delegates counts the child's requests too: the parent's
// request plus both of the child's priced requests reach spend and the
// token totals exactly once, however many record updates carry them and in
// whatever order stale versions arrive.
func TestSpendCountsExploreChildRequests(t *testing.T) {
	m := spendTestUI(t, "openai", "gpt-4o")
	m.Update(usageEvent(1, model.RequestUsage{Prompt: 40_000, Completion: 1000, PromptSeen: true, CompletionSeen: true}))
	// The child on OpenRouter reports each request's exact cost.
	records := childTaskRecords(t, "spend", "openrouter", "openai/gpt-4o",
		model.RequestUsage{Prompt: 10_000, Completion: 500, PromptSeen: true, CompletionSeen: true, Cost: 0.02, CostSeen: true},
		model.RequestUsage{Prompt: 20_000, Completion: 700, PromptSeen: true, CompletionSeen: true, Cost: 0.03, CostSeen: true})
	if len(records) < 3 {
		t.Fatalf("child emitted %d record updates, want several", len(records))
	}
	for _, record := range records {
		m.Update(childTaskEvent(1, record))
	}
	// Redelivered and stale versions add nothing.
	for _, record := range records {
		m.Update(childTaskEvent(1, record))
	}
	if m.usagePrompt != 70_000 || m.usageCompletion != 2200 {
		t.Fatalf("tokens = %d/%d, want parent 40000/1000 plus child 30000/1200", m.usagePrompt, m.usageCompletion)
	}
	// Parent ~$0.11 (gpt-4o estimate) + child $0.05 exact: marked "~".
	if !approxEqual(m.spend, 0.16) || !m.spendKnown || !m.spendEstimated || m.spendSegment() != "~$0.16" {
		t.Fatalf("spend = %v %q (known=%t estimated=%t), want ~$0.16", m.spend, m.spendSegment(), m.spendKnown, m.spendEstimated)
	}
}

// A child's exact costs keep spend unmarked; a child's catalog estimate
// marks it; a child whose pricing is unknown leaves spend hidden; a record
// restored from storage is the baseline, not new usage.
func TestSpendFromExploreChildFollowsItsCostSource(t *testing.T) {
	exact := spendTestUI(t, "openrouter", "openai/gpt-4o")
	for _, record := range childTaskRecords(t, "spend", "openrouter", "openai/gpt-4o",
		model.RequestUsage{Prompt: 10, Completion: 1, PromptSeen: true, CompletionSeen: true, Cost: 0.25, CostSeen: true}) {
		exact.Update(childTaskEvent(1, record))
	}
	if exact.spendSegment() != "$0.25" {
		t.Fatalf("exact child spend = %q, want $0.25", exact.spendSegment())
	}

	estimated := spendTestUI(t, "openrouter", "openai/gpt-4o")
	for _, record := range childTaskRecords(t, "spend", "openai", "gpt-4o",
		model.RequestUsage{Prompt: 40_000, Completion: 1000, PromptSeen: true, CompletionSeen: true}) {
		estimated.Update(childTaskEvent(1, record))
	}
	if estimated.spendSegment() != "~$0.11" {
		t.Fatalf("estimated child spend = %q, want ~$0.11", estimated.spendSegment())
	}

	unknown := spendTestUI(t, "groq", "mystery")
	for _, record := range childTaskRecords(t, "spend", "groq", "mystery",
		model.RequestUsage{Prompt: 40_000, Completion: 1000, PromptSeen: true, CompletionSeen: true}) {
		unknown.Update(childTaskEvent(1, record))
	}
	if unknown.spendKnown || unknown.spendSegment() != "" || unknown.usagePrompt != 40_000 {
		t.Fatalf("unknown child: spend %q known=%t prompt=%d, want hidden spend and counted tokens", unknown.spendSegment(), unknown.spendKnown, unknown.usagePrompt)
	}

	subscription := spendTestUI(t, "chatgpt", "gpt-5.5")
	for _, record := range childTaskRecords(t, "spend", "chatgpt", "gpt-5.5",
		model.RequestUsage{Prompt: 40_000, Completion: 1000, PromptSeen: true, CompletionSeen: true}) {
		subscription.Update(childTaskEvent(1, record))
	}
	if subscription.spendSegment() != "" || subscription.spendKnown {
		t.Fatalf("subscription child fabricated spend = %q", subscription.spendSegment())
	}

	restored := spendTestUI(t, "openrouter", "openai/gpt-4o")
	records := childTaskRecords(t, "spend", "openrouter", "openai/gpt-4o",
		model.RequestUsage{Prompt: 10, Completion: 1, PromptSeen: true, CompletionSeen: true, Cost: 0.25, CostSeen: true})
	final := records[len(records)-1]
	restored.taskRecords = append(restored.taskRecords, final)
	restored.Update(childTaskEvent(1, final))
	if restored.spendKnown || restored.usagePrompt != 0 {
		t.Fatalf("restored record counted again: spend %v prompt %d", restored.spend, restored.usagePrompt)
	}
}

// Agent detail cost follows the task's provider and marks catalog
// estimates with "~".
func TestAgentUsageSummaryMarksEstimates(t *testing.T) {
	record := func(provider, modelID string, usage explore.Usage) explore.Record {
		return explore.Record{ID: "t1", Provider: provider, Model: modelID, Rounds: 1, Usage: usage}
	}
	measured := explore.Usage{PromptTokens: 40_000, CompletionTokens: 1000, PromptKnown: true, CompletionKnown: true, ReportedRequests: 1}

	estimated := measured
	estimated.Cost, estimated.CostKnown, estimated.CostEstimated = 0.11, true, true
	if got := AgentUsageSummary(record("openai", "gpt-4o", estimated)); !strings.Contains(got, "cost: ~$0.1100") {
		t.Fatalf("estimated cost not marked: %q", got)
	}

	exact := measured
	exact.Cost, exact.CostKnown = 0.1234, true
	if got := AgentUsageSummary(record("openrouter", "openai/gpt-4o", exact)); !strings.Contains(got, "cost: $0.1234") || strings.Contains(got, "~") {
		t.Fatalf("exact cost marked or missing: %q", got)
	}

	// No stored cost but complete tokens: the catalog fallback is an estimate.
	if got := AgentUsageSummary(record("openai", "gpt-4o", measured)); !strings.Contains(got, "cost: ~$0.1100") {
		t.Fatalf("fallback estimate missing or unmarked: %q", got)
	}
	// The fallback never borrows another provider's price.
	if got := AgentUsageSummary(record("groq", "claude-opus-5-5", measured)); !strings.Contains(got, "cost: unknown") {
		t.Fatalf("fallback borrowed a price: %q", got)
	}
	// ChatGPT allowance does not establish a charge. A reported charge must
	// not be rewritten to a subscription zero, including after resume.
	if got := AgentUsageSummary(record("ChatGPT", "gpt-5.5", explore.Usage{})); !strings.Contains(got, "cost: unknown") {
		t.Fatalf("subscription fabricated zero: %q", got)
	}
	if got := AgentUsageSummary(record("ChatGPT", "gpt-5.5", exact)); !strings.Contains(got, "cost: $0.1234") {
		t.Fatalf("subscription dropped reported credits: %q", got)
	}
	// A stored $0 for a subscription-only model ID on a token-billed
	// provider was never a charge.
	zero := measured
	zero.CostKnown = true
	if got := AgentUsageSummary(record("ChatGPT", "gpt-5.5", zero)); !strings.Contains(got, "cost: unknown") {
		t.Fatalf("legacy subscription zero presented as confirmed: %q", got)
	}
	if got := AgentUsageSummary(record("dialagram", "gpt-5.5", zero)); !strings.Contains(got, "cost: unknown") {
		t.Fatalf("subscription-only zero on a token provider kept: %q", got)
	}
	// The old table priced these IDs at $0 by ID alone on every provider,
	// even where the catalog now prices the pair: the stored zero is never
	// shown as a known $0. A one-request record falls back to the
	// estimate; a multi-request record stays unknown.
	for _, pair := range [][2]string{{"openai", "gpt-5.5"}, {"openrouter", "openai/gpt-5.5"}, {"opencode-zen", "gpt-5.5"}} {
		got := AgentUsageSummary(record(pair[0], pair[1], zero))
		if strings.Contains(got, "$0.0000") || !strings.Contains(got, "cost: ~$") {
			t.Fatalf("%s/%s legacy zero = %q, want a marked estimate", pair[0], pair[1], got)
		}
		multi := explore.Record{ID: "t1", Provider: pair[0], Model: pair[1], Rounds: 2,
			Usage: explore.Usage{PromptTokens: 80_000, CompletionTokens: 2000, PromptKnown: true, CompletionKnown: true, ReportedRequests: 2, CostKnown: true}}
		if got := AgentUsageSummary(multi); !strings.Contains(got, "cost: unknown") {
			t.Fatalf("%s/%s multi-request legacy zero = %q, want unknown", pair[0], pair[1], got)
		}
	}
	// A stored known $0 for any other model is kept.
	if got := AgentUsageSummary(record("openrouter", "openai/gpt-4o", zero)); !strings.Contains(got, "cost: $0.0000") {
		t.Fatalf("reported zero dropped: %q", got)
	}

	// Several requests are never repriced from their summed totals: three
	// 100K-prompt requests on gemini-2.5-pro would cross its 200K tier as
	// one 300K request ($0.795 instead of $0.405). Without a stored cost
	// the task's cost stays unknown.
	summed := explore.Record{ID: "t1", Provider: "gemini", Model: "gemini-2.5-pro", Rounds: 3,
		Usage: explore.Usage{PromptTokens: 300_000, CompletionTokens: 3000, PromptKnown: true, CompletionKnown: true, ReportedRequests: 3}}
	if got := AgentUsageSummary(summed); !strings.Contains(got, "cost: unknown") {
		t.Fatalf("aggregate totals repriced: %q", got)
	}

	total := TotalAgentUsage([]explore.Record{record("openrouter", "openai/gpt-4o", exact), {ID: "t2", Provider: "openai", Model: "gpt-4o", Rounds: 1, Usage: estimated}})
	if !total.CostKnown || !total.CostEstimated || !approxEqual(total.Cost, 0.2334) {
		t.Fatalf("total = %+v, want known estimated 0.2334", total)
	}
}
