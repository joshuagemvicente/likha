package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"likha/internal/model"
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
