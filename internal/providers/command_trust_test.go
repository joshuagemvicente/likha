package providers

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCommandTrustRoundTripPreservesConfig(t *testing.T) {
	stateDir, repo := t.TempDir(), t.TempDir()
	if err := SaveStoredConfig(stateDir, StoredProviderConfig{Provider: "openai", Model: "gpt-4o-mini", Theme: "nord"}); err != nil {
		t.Fatal(err)
	}
	if CommandTrusted(stateDir, repo, "script:test", "abc") {
		t.Fatal("untrusted repository reported trusted")
	}
	if err := TrustCommandChecks(stateDir, repo, map[string]string{"script:test": "abc", "go": ""}); err != nil {
		t.Fatal(err)
	}
	if !CommandTrusted(stateDir, repo, "script:test", "abc") || !CommandTrusted(stateDir, repo, "go", "") {
		t.Fatal("trusted checks not recognized")
	}
	if CommandTrusted(stateDir, repo, "script:test", "changed") || CommandTrusted(stateDir, repo, "make", "") {
		t.Fatal("changed fingerprint or unrecorded check reported trusted")
	}
	if CommandTrusted(stateDir, t.TempDir(), "go", "") {
		t.Fatal("trust leaked to another repository")
	}
	// A symlinked spelling of the same repository shares the record.
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(repo, link); err != nil {
		t.Fatal(err)
	}
	if !CommandTrusted(stateDir, link, "go", "") {
		t.Fatal("symlinked repository path did not share trust")
	}
	cfg, err := LoadStoredConfig(stateDir)
	if err != nil || cfg.Provider != "openai" || cfg.Theme != "nord" {
		t.Fatalf("trust write lost existing config: %+v, %v", cfg, err)
	}
}

func TestCommandTrustWithoutStateDir(t *testing.T) {
	if CommandTrusted("", t.TempDir(), "go", "") {
		t.Fatal("no state directory must mean untrusted")
	}
}
