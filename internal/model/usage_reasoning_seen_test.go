package model

import (
	"encoding/json"
	"testing"
)

// The effort probe tells a reported zero from an absent reasoning count
// (specs/effort-probe tasks T1), on both usage parsers.
func TestReasoningSeenSeparatesZeroFromAbsent(t *testing.T) {
	cases := []struct {
		name          string
		parse         func(json.RawMessage) usageReport
		raw           string
		wantReasoning int64
		wantSeen      bool
	}{
		{"chat count present", parseFinalUsageDetailed, `{"prompt_tokens":5,"completion_tokens":50,"completion_tokens_details":{"reasoning_tokens":40}}`, 40, true},
		{"chat count zero", parseFinalUsageDetailed, `{"prompt_tokens":5,"completion_tokens":50,"completion_tokens_details":{"reasoning_tokens":0}}`, 0, true},
		{"chat field absent", parseFinalUsageDetailed, `{"prompt_tokens":5,"completion_tokens":50,"completion_tokens_details":{}}`, 0, false},
		{"chat malformed count", parseFinalUsageDetailed, `{"prompt_tokens":5,"completion_tokens":50,"completion_tokens_details":{"reasoning_tokens":"40"}}`, 0, false},
		{"responses count present", parseCodexUsageDetailed, `{"input_tokens":5,"output_tokens":50,"output_tokens_details":{"reasoning_tokens":40}}`, 40, true},
		{"responses count zero", parseCodexUsageDetailed, `{"input_tokens":5,"output_tokens":50,"output_tokens_details":{"reasoning_tokens":0}}`, 0, true},
		{"responses field absent", parseCodexUsageDetailed, `{"input_tokens":5,"output_tokens":50}`, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.parse(json.RawMessage(tc.raw)).request()
			if got.Reasoning != tc.wantReasoning || got.ReasoningSeen != tc.wantSeen {
				t.Fatalf("reasoning = %d seen %t, want %d seen %t", got.Reasoning, got.ReasoningSeen, tc.wantReasoning, tc.wantSeen)
			}
		})
	}
}
