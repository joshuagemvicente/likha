package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"likha/internal/model"
	"likha/internal/model/catalog"
)

func TestModelMetadataLogWritesContextFieldsPrivately(t *testing.T) {
	stateDir := t.TempDir()
	debugLog, err := openModelMetadataLog(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	debugLog.observe("openrouter", "startup /models", 15*time.Millisecond, []model.ModelDetails{
		{ID: "anthropic/claude-example", ContextWindow: 200_000, ContextWindowSource: "context_length"},
		{ID: "unknown-model"},
	}, nil)
	debugLog.close()

	info, err := os.Stat(filepath.Join(stateDir, modelMetadataLogName))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("model metadata log permissions = %04o, want 0600", info.Mode().Perm())
	}
	contents, err := os.ReadFile(filepath.Join(stateDir, modelMetadataLogName))
	if err != nil {
		t.Fatal(err)
	}
	text := string(contents)
	for _, expected := range []string{
		`provider="openrouter"`,
		`id="anthropic/claude-example" context_window=200000 field="context_length"`,
		`id="unknown-model" context_window=unknown`,
		`API keys, request headers, and prompts are not logged`,
	} {
		if !strings.Contains(text, expected) {
			t.Errorf("model metadata log missing %q:\n%s", expected, text)
		}
	}
}

// Each logged model names its bundled catalog row's provenance when the
// exact provider+model pair has one, and nothing for an unknown pair.
func TestModelMetadataLogRecordsCatalogProvenance(t *testing.T) {
	stateDir := t.TempDir()
	debugLog, err := openModelMetadataLog(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	debugLog.observe("claude", "startup /models", time.Millisecond, []model.ModelDetails{
		{ID: "claude-opus-5-5", ContextWindow: 1_000_000, ContextWindowSource: "max_input_tokens"},
		{ID: "claude-unlisted"},
	}, nil)
	debugLog.observe("chatgpt", "first-run setup /models", time.Millisecond, []model.ModelDetails{{ID: "gpt-5.5"}}, nil)
	debugLog.observe("groq", "startup /models", time.Millisecond, []model.ModelDetails{{ID: "claude-opus-5-5"}}, nil)
	debugLog.observe("claude", "startup /models", time.Millisecond, []model.ModelDetails{{ID: "claude-sonnet-4-5"}}, nil)
	debugLog.close()

	contents, err := os.ReadFile(filepath.Join(stateDir, modelMetadataLogName))
	if err != nil {
		t.Fatal(err)
	}
	text := string(contents)
	opus, ok := catalog.Lookup("claude", "claude-opus-5-5")
	if !ok || opus.Updated == "" {
		t.Fatal("catalog lacks claude/claude-opus-5-5 with an updated date")
	}
	gpt, ok := catalog.Lookup("chatgpt", "gpt-5.5")
	if !ok || gpt.Verified == "" {
		t.Fatal("catalog lacks the chatgpt/gpt-5.5 override with a verified date")
	}
	sonnet, ok := catalog.Lookup("claude", "claude-sonnet-4-5")
	if !ok || sonnet.Verified == "" {
		t.Fatal("catalog lacks the claude/claude-sonnet-4-5 context override")
	}
	for _, expected := range []string{
		// A field-level override names the fields its date vouches for.
		`provider="claude" id="claude-sonnet-4-5" context_window=unknown catalog_source="override" catalog_updated="` + sonnet.Updated + `" catalog_verified="` + sonnet.Verified + `" catalog_overridden="context"`,
		`provider="claude" id="claude-opus-5-5" context_window=1000000 field="max_input_tokens" catalog_source="models.dev" catalog_updated="` + opus.Updated + `"`,
		`provider="chatgpt" id="gpt-5.5" context_window=unknown catalog_source="override" catalog_verified="` + gpt.Verified + `"`,
	} {
		if !strings.Contains(text, expected) {
			t.Errorf("model metadata log missing %q:\n%s", expected, text)
		}
	}
	for _, line := range strings.Split(text, "\n") {
		if (strings.Contains(line, `id="claude-unlisted"`) || strings.Contains(line, `provider="groq"`) && strings.Contains(line, "id=")) && strings.Contains(line, "catalog_") {
			t.Errorf("unknown pair logged catalog provenance: %q", line)
		}
	}
}
