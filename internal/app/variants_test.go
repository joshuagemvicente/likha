package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"likha/internal/model"
	"likha/internal/providers"
	"likha/internal/session"
)

// The public identity stays first-party, but every network request is routed to
// a local fixture. Unexpected destinations fail closed instead of going live.
type variantAppTransport struct {
	target *url.URL
	next   http.RoundTripper
}

func (v variantAppTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != "api.openai.com" {
		return nil, fmt.Errorf("unexpected test destination %s", req.URL.Host)
	}
	copy := req.Clone(req.Context())
	copy.URL.Scheme, copy.URL.Host, copy.Host = v.target.Scheme, v.target.Host, ""
	return v.next.RoundTrip(copy)
}

func TestHeadlessVariantSelectionPrecedenceAndDurableSession(t *testing.T) {
	for _, tc := range []struct {
		name, saved, explicit, want string
		stale                       bool
	}{
		{name: "session beats preference", saved: "low", want: "low"},
		{name: "explicit Default", saved: "low", explicit: "gpt-5.5#default"},
		{name: "explicit named", saved: "low", explicit: "gpt-5.5#xhigh", want: "xhigh"},
		{name: "explicit bare uses preference", saved: "low", explicit: "gpt-5.5", want: "high"},
		{name: "stale session noticed", saved: "gone", stale: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := headlessEnv(t)
			t.Setenv("LIKHA_OPENAI_API_KEY", headlessTestKey)
			root := t.TempDir()
			store, err := session.Open(state, root)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			snapshot, err := store.Create()
			if err != nil {
				t.Fatal(err)
			}
			snapshot.Selection = &model.Selection{Provider: "openai", Model: "gpt-5.5", Variant: tc.saved}
			if err := store.Save(snapshot); err != nil {
				t.Fatal(err)
			}
			cfg := providers.StoredProviderConfig{Provider: "openai", Model: "gpt-5.5"}
			cfg.SetStoredVariant("openai", "gpt-5.5", "high")
			if err := providers.SaveStoredConfig(state, cfg); err != nil {
				t.Fatal(err)
			}
			var mu sync.Mutex
			var bodies []map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.Method == http.MethodGet {
					fmt.Fprint(w, `{"data":[{"id":"gpt-5.5"}]}`)
					return
				}
				var body map[string]any
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				mu.Lock()
				bodies = append(bodies, body)
				mu.Unlock()
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"id\":\"msg_1\",\"status\":\"completed\",\"role\":\"assistant\",\"phase\":\"final_answer\",\"content\":[{\"type\":\"output_text\",\"text\":\"done\"}]}]}}\n\n")
			}))
			defer server.Close()
			target, err := url.Parse(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			original := http.DefaultTransport
			http.DefaultTransport = variantAppTransport{target, original}
			defer func() { http.DefaultTransport = original }()
			args := []string{"--resume", snapshot.ID, "--json", "-p", "next"}
			if tc.explicit != "" {
				args = append(args, "--model", tc.explicit)
			}
			code, stdout, stderr := runApp(append(args, root)...)
			if code != 0 || stderr != "" {
				t.Fatalf("exit=%d stderr=%s stdout=%s", code, stderr, stdout)
			}
			mu.Lock()
			captured := append([]map[string]any(nil), bodies...)
			mu.Unlock()
			if len(captured) != 1 {
				t.Fatalf("POSTs=%d", len(captured))
			}
			reasoning, _ := captured[0]["reasoning"].(map[string]any)
			got, _ := reasoning["effort"].(string)
			if got != tc.want {
				t.Fatalf("effort=%q want %q", got, tc.want)
			}
			wantMeta := tc.want
			if wantMeta == "" {
				wantMeta = "default"
			}
			if !strings.Contains(stdout, `"variant":"`+wantMeta+`"`) {
				t.Fatalf("variant metadata missing: %s", stdout)
			}
			if tc.stale && strings.Count(stdout, "Saved variant gone") != 1 {
				t.Fatalf("stale notice: %s", stdout)
			}
			loaded, err := store.Load(snapshot.ID)
			if err != nil {
				t.Fatal(err)
			}
			if loaded.Selection == nil || loaded.Selection.Variant != tc.want || len(loaded.History) != 2 || loaded.History[1].Phase != "final_answer" {
				t.Fatalf("durable session: %+v", loaded)
			}
			stored, err := providers.LoadStoredConfig(state)
			if choice, exists := stored.StoredVariant("openai", "gpt-5.5"); err != nil || !exists || choice != "high" {
				t.Fatal("session or launch override overwrote preference")
			}
		})
	}
}

func TestExplicitUnknownVariantFailsBeforeInference(t *testing.T) {
	state := headlessEnv(t)
	t.Setenv("LIKHA_OPENAI_API_KEY", headlessTestKey)
	if err := providers.SaveStoredConfig(state, providers.StoredProviderConfig{Provider: "openai", Model: "gpt-5.5"}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			t.Error("unknown explicit variant reached inference")
			w.WriteHeader(500)
			return
		}
		fmt.Fprint(w, `{"data":[{"id":"gpt-5.5"}]}`)
	}))
	defer server.Close()
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	original := http.DefaultTransport
	http.DefaultTransport = variantAppTransport{target, original}
	defer func() { http.DefaultTransport = original }()
	code, stdout, stderr := runApp("--model", "openai/gpt-5.5#gone", "-p", "test", t.TempDir())
	if code != 2 || stdout != "" || !strings.Contains(stderr, "not available") || strings.Contains(stderr, headlessTestKey) {
		t.Fatalf("exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
}

func TestVariantSuffixUsageErrorsSendNothing(t *testing.T) {
	for _, ref := range []string{"gpt-5.5#", "gpt-5.5#high#low", "gpt-5.5#high/low"} {
		t.Run(ref, func(t *testing.T) {
			headlessEnv(t)
			fake := newHeadlessProvider(t, http.StatusOK)
			code, _, stderr := runApp("--endpoint", fake.server.URL+"/v1", "--model", ref, "-p", "test", t.TempDir())
			if code != 2 || !strings.Contains(stderr, "variant suffix") || fake.posts.Load()+fake.gets.Load() != 0 {
				t.Fatalf("exit=%d stderr=%s", code, stderr)
			}
		})
	}
}
