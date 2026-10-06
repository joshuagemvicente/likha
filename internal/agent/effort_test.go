package agent

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"likha/internal/model"
)

// TestBackgroundRequestsSendNoEffort pins specs/model-effort decision 9:
// compaction and session naming use the provider's default effort even when
// the session runs at a higher level, and the session keeps its level.
func TestBackgroundRequestsSendNoEffort(t *testing.T) {
	var bodies []string
	reply := ""
	client := nameTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
		}
		bodies = append(bodies, string(raw))
		sendEvents(w, nameBody(reply), `[DONE]`)
	})
	client.SetEffort(model.EffortTopLevel, "high")

	reply = "Effort test"
	if _, _, _, err := GenerateSessionNameUsage(context.Background(), client, sampleHistory()); err != nil {
		t.Fatal(err)
	}
	reply = "A summary"
	if _, _, _, _, err := CompactHistoryUsage(context.Background(), client, sampleHistory(), "", nil); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 {
		t.Fatalf("requests = %d, want 2", len(bodies))
	}
	for i, body := range bodies {
		if strings.Contains(body, "reasoning") {
			t.Fatalf("background request %d carried effort: %s", i, body)
		}
	}
	if client.Effort() != "high" {
		t.Fatalf("session effort changed to %q", client.Effort())
	}
}
