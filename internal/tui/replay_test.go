package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"likha/internal/agent"
	"likha/internal/model"
	"likha/internal/providers"
	"likha/internal/session"
)

func TestRejectedReplayTurnErrorCarriesHint(t *testing.T) {
	const providerError = "model HTTP 400 Bad Request: missing reasoning_content"
	replay := &model.ReplayRejectedError{Missing: 1, Err: errString(providerError)}
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"replay alone", replay, providerError + "\n" + replayRejectedHint},
		{
			"replay and effort",
			&model.EffortRejectedError{Level: "max", Err: replay},
			providerError + "\nSent with effort max; /effort default sends none.\n" + replayRejectedHint,
		},
		{"neither", errString(providerError), providerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := NewUI("/sample", nil, nil, "", providers.Connection{Provider: "DeepSeek", Verified: true}, t.TempDir(), nil, session.Snapshot{})
			m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
			m.working, m.runID = true, 7
			m.events = make(chan agent.TurnEvent, 1)
			m.cancel = func() {}
			m.Update(agent.TurnEvent{Kind: "error", RunID: 7, Text: tc.err.Error(), Err: tc.err})
			var errorsShown []string
			for _, e := range m.entries {
				if e.role == "Error" {
					errorsShown = append(errorsShown, e.content)
				}
			}
			// Exact match: the hint shows once, after the effort hint.
			if len(errorsShown) != 1 || errorsShown[0] != tc.want {
				t.Fatalf("error entries = %q, want one %q", errorsShown, tc.want)
			}
		})
	}
}
