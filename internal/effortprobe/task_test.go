package effortprobe

import (
	"context"
	"strings"
	"testing"

	"likha/internal/model"
)

func TestProbeTaskPassesBothRequestBuilders(t *testing.T) {
	if _, err := model.BuildCodexRequest(probeModel, "high", probeMessages(), probeTools()); err != nil {
		t.Fatalf("Responses request: %v", err)
	}
	fake := newFakeProvider(t, honors)
	client, err := model.New(fake.endpoint(), probeModel, testKey)
	if err != nil {
		t.Fatal(err)
	}
	reply, err := client.Stream(context.Background(), probeMessages(), probeTools(), nil, nil)
	if err != nil {
		t.Fatalf("chat-completions request: %v", err)
	}
	if called, answer := toolAnswer(reply); !called || answer == nil || *answer != expectedAnswer {
		t.Fatalf("tool answer = %t %v", called, answer)
	}
	if estimatedInputTokens(probeModel) <= 0 {
		t.Fatal("input estimate must be positive")
	}
}

func TestToolAnswer(t *testing.T) {
	cases := []struct {
		name       string
		calls      []model.ToolCall
		wantCalled bool
		wantAnswer int64 // -1 for no integer answer
	}{
		{"no call", nil, false, -1},
		{"other tool", []model.ToolCall{{Name: "read", Arguments: `{"answer":440}`}}, false, -1},
		{"correct", []model.ToolCall{{Name: answerTool, Arguments: `{"answer":440}`}}, true, 440},
		{"wrong", []model.ToolCall{{Name: answerTool, Arguments: `{"answer":439}`}}, true, 439},
		{"not an integer", []model.ToolCall{{Name: answerTool, Arguments: `{"answer":"440"}`}}, true, -1},
		{"missing", []model.ToolCall{{Name: answerTool, Arguments: `{}`}}, true, -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called, answer := toolAnswer(model.Message{ToolCalls: tc.calls})
			got := int64(-1)
			if answer != nil {
				got = *answer
			}
			if called != tc.wantCalled || got != tc.wantAnswer {
				t.Fatalf("toolAnswer = %t %d, want %t %d", called, got, tc.wantCalled, tc.wantAnswer)
			}
		})
	}
}

func TestEstimate(t *testing.T) {
	cases := []struct {
		name     string
		provider string
		model    string
		levels   []string
		want     CostKind
		text     string
	}{
		{"priced", "openai", probeModel, nil, CostDollars, "assumes up to 3,000 output tokens per request at $30.00/M; input about "},
		{"unpriced", "dialagram", "some-model", []string{"low"}, CostUnknownPrice, "cost unknown: no catalog price for some-model on dialagram"},
		{"subscription", "chatgpt", probeModel, []string{"low"}, CostSubscription, "uses your ChatGPT plan allowance; no per-token charge"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := NewPlan(provider(t, tc.provider), tc.model, nil, Options{Levels: tc.levels})
			if err != nil {
				t.Fatal(err)
			}
			e := Estimate(plan)
			if e.Kind != tc.want || !strings.Contains(e.Text(), tc.text) {
				t.Fatalf("estimate = %+v, text %q", e, e.Text())
			}
		})
	}
}

func TestEstimateDollars(t *testing.T) {
	plan, err := NewPlan(provider(t, "openai"), probeModel, nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	e := Estimate(plan)
	// gpt-5.5 on openai: $30/M output, $5/M input; 7 requests.
	want := 7 * (3_000*30.0 + float64(e.InputTokens)*5.0) / 1_000_000
	if e.Dollars != want || e.Dollars < 0.63 || e.Dollars > 0.64 {
		t.Fatalf("dollars = %v, want %v", e.Dollars, want)
	}
}

func TestFormatCount(t *testing.T) {
	cases := map[int64]string{0: "0", 999: "999", 1000: "1,000", 40000: "40,000", 1234567: "1,234,567", -3000: "-3,000"}
	for in, want := range cases {
		if got := formatCount(in); got != want {
			t.Fatalf("formatCount(%d) = %q, want %q", in, got, want)
		}
	}
}
