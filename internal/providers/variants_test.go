package providers

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"

	"likha/internal/model"
)

func TestVariantPreferencesAtomicConcurrentAndUnknownSettings(t *testing.T) {
	dir := t.TempDir()
	path := ConfigFilePath(dir)
	if err := os.WriteFile(path, []byte(`{"provider":"openai","model":"gpt-5.5","future":{"nested":[1,2]},"theme":"nord"}`), 0600); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := UpdateStoredConfig(context.Background(), dir, func(cfg *StoredProviderConfig) error {
				cfg.SetStoredVariant("openai", string(rune('a'+i)), "high")
				return nil
			})
			if err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	cfg, err := LoadStoredConfig(dir)
	if err != nil || len(cfg.Variants["openai"]) != 30 || cfg.Theme != "nord" {
		t.Fatalf("lost concurrent data: %+v, %v", cfg, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["future"]) != `{"nested":[1,2]}` {
		t.Fatal("unknown configuration dropped")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("config not private")
	}
	files, err := filepath.Glob(filepath.Join(dir, ".config.json-*"))
	if err != nil || len(files) != 0 {
		t.Fatal("temporary files leaked")
	}
}

func TestConfigRefusesCorruptionCancellationAndSymlink(t *testing.T) {
	dir := t.TempDir()
	path := ConfigFilePath(dir)
	if err := os.WriteFile(path, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := UpdateStoredConfig(context.Background(), dir, func(cfg *StoredProviderConfig) error { cfg.Theme = "new"; return nil }); err == nil {
		t.Fatal("overwrote corrupt config")
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != "broken" {
		t.Fatal("corrupt file changed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := UpdateStoredConfig(ctx, dir, func(*StoredProviderConfig) error { t.Fatal("cancelled update ran"); return nil }); err == nil {
		t.Fatal("cancellation ignored")
	}
	other := filepath.Join(dir, "other")
	if err := os.WriteFile(other, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, path); err != nil {
		t.Fatal(err)
	}
	if err := UpdateStoredConfig(context.Background(), dir, func(*StoredProviderConfig) error { return nil }); err == nil {
		t.Fatal("followed config symlink")
	}
}

func TestVariantPreferenceMigrationAndExplicitPrecedence(t *testing.T) {
	dir := t.TempDir()
	p, _ := model.LookupProvider("openai")
	client, err := model.New(p.BaseURL, "gpt-5.5", "unused")
	if err != nil {
		t.Fatal(err)
	}
	cfg := StoredProviderConfig{Theme: "nord", DefaultVariants: map[string]map[string]string{"openai": {"gpt-5.5": "high"}}}
	cfg.SetStoredEffort(p.Name, client.Model(), "low")
	if err := SaveStoredConfig(dir, cfg); err != nil {
		t.Fatal(err)
	}
	if note, err := ApplyVariant(context.Background(), dir, client, p, nil, nil); err != nil || note != "" || client.Variant() != "low" {
		t.Fatalf("migration = %q, %v", note, err)
	}
	loaded, err := LoadStoredConfig(dir)
	choice, exists := loaded.StoredVariant(p.Name, client.Model())
	if err != nil || !exists || choice != "low" || loaded.Theme != "nord" {
		t.Fatal("migration lost preference or settings")
	}
	loaded.SetStoredVariant(p.Name, client.Model(), "")
	if err := SaveStoredConfig(dir, loaded); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyVariant(context.Background(), dir, client, p, nil, nil); err != nil || client.Variant() != "" {
		t.Fatal("explicit Default lost to legacy or configured default")
	}
	unknown := "gone"
	if _, err := ApplyVariant(context.Background(), dir, client, p, nil, &unknown); err == nil {
		t.Fatal("explicit unknown variant downgraded")
	}
	if note, err := ApplySavedVariant(context.Background(), dir, client, p, nil, unknown); err != nil || note == "" || client.Variant() != "" {
		t.Fatal("stale session choice did not fall back visibly")
	}
}

func TestMalformedLiveReasoningIsNoticedAndDefaultStillApplies(t *testing.T) {
	dir := t.TempDir()
	p, _ := model.LookupProvider("openrouter")
	client, err := model.New(p.BaseURL, "openai/gpt-5.5", "unused")
	if err != nil {
		t.Fatal(err)
	}
	live := &model.ReasoningCapabilities{Invalid: "mandatory must be a boolean"}
	cases := []struct {
		name  string
		apply func() (string, error)
	}{
		{"startup", func() (string, error) { return ApplyVariant(context.Background(), dir, client, p, live, nil) }},
		{"resume", func() (string, error) { return ApplySavedVariant(context.Background(), dir, client, p, live, "") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			note, err := tc.apply()
			if err != nil {
				t.Fatalf("Default was not applied: %v", err)
			}
			if !strings.Contains(note, "mandatory must be a boolean") || client.Variant() != "" {
				t.Fatalf("notice = %q, variant = %q", note, client.Variant())
			}
		})
	}
}

func TestSymlinkedConfigIsRefusedWithAClearError(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "dotfiles-config.json")
	if err := os.WriteFile(target, []byte(`{"theme":"nord"}`), 0600); err != nil {
		t.Fatal(err)
	}
	path := ConfigFilePath(dir)
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	_, err := LoadStoredConfig(dir)
	if !errors.Is(err, syscall.ELOOP) {
		t.Fatalf("LoadStoredConfig error = %v, want the symlink refusal", err)
	}
	if want := "config.json must not be a symlink: " + path; !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %q, want it to contain %q", err, want)
	}
}
