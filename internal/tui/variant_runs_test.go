package tui

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"likha/internal/model"
	"likha/internal/providers"
	"likha/internal/repository"
	"likha/internal/session"
	"likha/internal/transcript"
)

type variantUITransport struct {
	target *url.URL
	next   http.RoundTripper
}

func (v variantUITransport) RoundTrip(req *http.Request) (*http.Response, error) {
	copy := req.Clone(req.Context())
	copy.URL.Scheme, copy.URL.Host, copy.Host = v.target.Scheme, v.target.Host, ""
	return v.next.RoundTrip(copy)
}

func TestVariantQueueHTTPIsolationCancelAndCapturedFlush(t *testing.T) {
	started := make(chan struct{})
	var mu sync.Mutex
	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodGet {
			fmt.Fprint(w, `{"data":[{"id":"gpt-5.5"}]}`)
			return
		}
		data, err := io.ReadAll(req.Body)
		if err != nil {
			t.Error(err)
		}
		mu.Lock()
		bodies = append(bodies, string(data))
		first := len(bodies) == 1
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		if first {
			close(started)
			<-req.Context().Done()
			return
		}
		fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"id\":\"msg_1\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"done\"}]}]}}\n\n")
	}))
	defer server.Close()
	p, _ := model.LookupProvider("openai")
	client, err := model.New(p.BaseURL, "gpt-5.5", "test-key")
	if err != nil {
		t.Fatal(err)
	}
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client, err = client.ForkWithTransport(func(next http.RoundTripper) http.RoundTripper { return variantUITransport{target, next} })
	if err != nil {
		t.Fatal(err)
	}
	root, state := t.TempDir(), t.TempDir()
	repo, err := repository.New(root)
	if err != nil {
		t.Fatal(err)
	}
	store, err := session.Open(state, root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	snapshot, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	m := NewUI(root, repo, client, "gpt-5.5", providers.Connection{Provider: p.DisplayName, ProviderCanonical: p.Name, Verified: true}, state, store, snapshot)
	m.nameTried = true // naming is separately tested; this test counts user runs.
	d := newStrictDriver(t, m)
	d.send(tea.WindowSizeMsg{Width: 100, Height: 24})
	m.selectVariant("high")
	m.input = []rune("first")
	d.key(key("enter"))
	d.until(func() bool {
		select {
		case <-started:
			return true
		default:
			return false
		}
	}, "first request")
	d.key(tea.KeyMsg{Type: tea.KeyCtrlT}) // high -> xhigh
	m.input = []rune("second")
	d.key(key("enter"))
	d.key(tea.KeyMsg{Type: tea.KeyCtrlT}) // xhigh -> Default
	m.input = []rune("third")
	d.key(key("enter"))
	if m.snapshot.Selection.Variant != "high" || m.runClient.Variant() != "high" {
		t.Fatal("draft cycling changed committed execution")
	}
	d.key(key("esc"))
	d.until(func() bool { return !m.working }, "cancel acknowledgement")
	if len(m.queue) != 2 || len(m.queueSelections) != 2 {
		t.Fatalf("cancel lost held queue: %+v", m.queue)
	}
	d.key(key("enter"))
	if m.runClient.Variant() != "xhigh" || m.client.Variant() != "" || len(m.queue) != 1 {
		t.Fatal("flush grouped different variants or followed new draft")
	}
	d.key(tea.KeyMsg{Type: tea.KeyCtrlT}) // Default -> none while xhigh executes
	d.until(func() bool { return !m.working }, "second run")
	d.key(key("enter"))
	d.until(func() bool { return !m.working }, "third run")
	mu.Lock()
	captured := append([]string(nil), bodies...)
	mu.Unlock()
	if len(captured) != 3 || !strings.Contains(captured[0], `"effort":"high"`) || !strings.Contains(captured[1], `"effort":"xhigh"`) || strings.Contains(captured[2], `"effort"`) {
		t.Fatalf("request snapshots: %+v", captured)
	}
	saved, err := store.Load(snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Selection == nil || saved.Selection.Variant != "" || m.client.Variant() != "none" {
		t.Fatal("completion committed the newer composer instead of its snapshot")
	}
	var variants []string
	for _, entry := range saved.Entries {
		if entry.Role == transcript.TurnRole {
			info, ok := transcript.DecodeTurn(entry.Content)
			if !ok {
				t.Fatal("invalid footer")
			}
			variants = append(variants, info.Variant)
		}
	}
	if strings.Join(variants, ",") != "high,xhigh," {
		t.Fatalf("execution footers: %v", variants)
	}
}
