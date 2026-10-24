package model

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// Likha sends store:false, so the API keeps no reasoning items: a replayed
// reasoning item is usable only with the encrypted_content it was issued with.
func TestResponsesReplayRequiresEncryptedContent(t *testing.T) {
	cases := []struct {
		name   string
		item   string
		replay bool
	}{
		{"encrypted", `{"type":"reasoning","id":"rs_1","encrypted_content":"opaque","summary":[]}`, true},
		{"id only", `{"type":"reasoning","id":"rs_1","summary":[]}`, false},
		{"empty encrypted", `{"type":"reasoning","id":"rs_1","encrypted_content":"","summary":[]}`, false},
		{"null encrypted", `{"type":"reasoning","id":"rs_1","encrypted_content":null,"summary":[]}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := validResponsesReplay(json.RawMessage("[" + tc.item + "]")); got != tc.replay {
				t.Fatalf("validResponsesReplay = %v, want %v", got, tc.replay)
			}
			sse := codexFrame("response.completed", `{"response":{"status":"completed","output":[`+tc.item+`,{"type":"message","content":[{"type":"output_text","text":"ok"}]}]}}`)
			message, _, err := consumeCodexStreamDetailed(context.Background(), strings.NewReader(sse), nil, nil)
			if err != nil {
				t.Fatalf("completed turn failed: %v", err)
			}
			if (message.Replay != nil) != tc.replay {
				t.Fatalf("captured replay = %+v, want replay %v", message.Replay, tc.replay)
			}
		})
	}
}
