package tui

import (
	"testing"

	"lisa/internal/agent"
	"lisa/internal/model"
	"lisa/internal/providers"
	"lisa/internal/session"
)

func TestContextWindowResolutionUsesOverrideMetadataThenCatalog(t *testing.T) {
	tests := []struct {
		name       string
		modelID    string
		metadata   int64
		override   int64
		wantWindow int64
		wantKnown  bool
	}{
		{name: "override wins", modelID: "custom-model", metadata: 200_000, override: 300_000, wantWindow: 300_000, wantKnown: true},
		{name: "provider metadata", modelID: "custom-model", metadata: 200_000, wantWindow: 200_000, wantKnown: true},
		{name: "documented catalog", modelID: "gpt-4o-mini", wantWindow: 128_000, wantKnown: true},
		{name: "unknown model", modelID: "custom-model"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn := providers.Connection{ProviderCanonical: "openai"}
			if tt.metadata > 0 {
				conn.ContextWindows = map[string]int64{tt.modelID: tt.metadata}
			}
			if tt.override > 0 {
				conn.ContextWindowOverrides = map[string]map[string]int64{"openai": {tt.modelID: tt.override}}
			}
			m := NewUI("/sample", nil, nil, tt.modelID, conn, "", nil, session.Snapshot{})
			if m.contextWindow != tt.wantWindow || m.contextWindowKnown != tt.wantKnown {
				t.Fatalf("resolved context window = (%d, %t), want (%d, %t)", m.contextWindow, m.contextWindowKnown, tt.wantWindow, tt.wantKnown)
			}
		})
	}
}

func TestContextEventReplacesAndClearsUsageState(t *testing.T) {
	m := NewUI("/sample", nil, nil, "custom-model", providers.Connection{}, "", nil, session.Snapshot{})
	m.working, m.runID = true, 4
	m.contextTokens, m.contextSeen, m.contextEstimated = 77, true, true

	m.Update(agent.TurnEvent{RunID: 4, Kind: "context", ContextTokens: 123, ContextKnown: true, ContextEstimated: true})
	if m.contextTokens != 123 || !m.contextSeen || !m.contextEstimated {
		t.Fatalf("estimated event state = (%d, %t, %t), want (123, true, true)", m.contextTokens, m.contextSeen, m.contextEstimated)
	}

	m.Update(agent.TurnEvent{RunID: 4, Kind: "context", ContextTokens: 101, ContextKnown: true, ContextEstimated: false})
	if m.contextTokens != 101 || !m.contextSeen || m.contextEstimated {
		t.Fatalf("measured event state = (%d, %t, %t), want (101, true, false)", m.contextTokens, m.contextSeen, m.contextEstimated)
	}

	m.Update(agent.TurnEvent{RunID: 4, Kind: "context"})
	if m.contextTokens != 0 || m.contextSeen || m.contextEstimated {
		t.Fatalf("unknown event did not clear state: (%d, %t, %t)", m.contextTokens, m.contextSeen, m.contextEstimated)
	}
}

func TestContextEstimateRecalculatedOnResumeAndCompaction(t *testing.T) {
	root, base := t.TempDir(), t.TempDir()
	store, err := session.Open(base, root)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	snapshot.History = []model.Message{{Role: "user", Content: "restored history"}}
	if err := store.Save(snapshot); err != nil {
		t.Fatal(err)
	}

	m := NewUI(root, nil, nil, "custom-model", providers.Connection{}, "", store, session.Snapshot{})
	m.contextTokens, m.contextSeen, m.contextEstimated = 999, true, false // prior request usage must not survive resume
	m.resumeSession(snapshot.ID)
	wantResume, ok := model.EstimateInputTokens(m.modelName, snapshot.History, nil)
	if !ok || m.contextTokens != wantResume || !m.contextSeen || !m.contextEstimated {
		t.Fatalf("resumed estimate = (%d, %t, %t), want (%d, true, true)", m.contextTokens, m.contextSeen, m.contextEstimated, wantResume)
	}

	replacement := []model.Message{{Role: "user", Content: "compact brief"}}
	m.working, m.runID = true, 8
	m.cancel = func() {}
	m.contextTokens, m.contextSeen, m.contextEstimated = 456, true, false // summarize-call usage must not survive compaction
	m.Update(agent.TurnEvent{RunID: 8, Kind: "compacted", Text: "brief", History: replacement})
	wantCompact, ok := model.EstimateInputTokens(m.modelName, replacement, nil)
	if !ok || m.contextTokens != wantCompact || !m.contextSeen || !m.contextEstimated {
		t.Fatalf("compacted estimate = (%d, %t, %t), want (%d, true, true)", m.contextTokens, m.contextSeen, m.contextEstimated, wantCompact)
	}
}

func TestModelsSelectionUpdatesSelectedContextMetadata(t *testing.T) {
	client, err := model.New("http://127.0.0.1:1/v1", "old-model", "")
	if err != nil {
		t.Fatal(err)
	}
	conn := providers.Connection{
		Provider:               "OpenAI",
		ProviderCanonical:      "openai",
		ContextWindows:         map[string]int64{"old-model": 200_000},
		ContextWindowOverrides: map[string]map[string]int64{"openai": {"new-model": 250_000}},
	}
	m := NewUI("/sample", nil, client, "old-model", conn, t.TempDir(), nil, session.Snapshot{})
	m.dialog = dialogState{kind: dialogModels, open: true, cursor: 0}
	m.dialogModelRows = []modelsRow{{provider: model.Provider{Name: "openai", DisplayName: "OpenAI"}, model: "new-model", contextWindow: 300_000}}

	m.confirmDialog([]int{0})
	if m.modelName != "new-model" {
		t.Fatalf("selected model = %q, want new-model", m.modelName)
	}
	if got := m.conn.ContextWindows["new-model"]; got != 300_000 {
		t.Fatalf("selected model metadata = %d, want 300000", got)
	}
	if m.contextWindow != 250_000 || !m.contextWindowKnown {
		t.Fatalf("selected model resolved window = (%d, %t), want (250000, true)", m.contextWindow, m.contextWindowKnown)
	}
}
