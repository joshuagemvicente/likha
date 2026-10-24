package integration_test

import (
	"strings"
	"testing"
)

func TestCLIVariantSuffixAndCustomRouteSafety(t *testing.T) {
	for _, tc := range []struct {
		suffix string
		code   int
	}{
		{"#default", 0}, {"#high", 2}, {"#", 2}, {"#high#low", 2},
	} {
		t.Run(tc.suffix, func(t *testing.T) {
			fake := newScriptedProvider(t, "text:done")
			state, root := t.TempDir(), t.TempDir()
			args := []string{"--provider", "openai", "--endpoint", fake.server.URL + "/v1", "--model", "test-model" + tc.suffix, "--api-key", headlessKey, "--json", "-p", "test", root}
			code, stdout, stderr := runLikha(t, likhaCommand(state, args...))
			if code != tc.code || strings.Contains(stdout+stderr, headlessKey) {
				t.Fatalf("exit=%d stdout=%s stderr=%s", code, stdout, stderr)
			}
			if tc.code != 0 {
				if fake.postCount() != 0 {
					t.Fatal("invalid variant reached inference")
				}
				return
			}
			events := jsonEvents(t, stdout)
			if events[0]["variant"] != "default" || events[len(events)-1]["variant"] != "default" || fake.postCount() != 1 {
				t.Fatalf("default selection events=%v", events)
			}
		})
	}
}
