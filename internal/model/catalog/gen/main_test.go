package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"likha/internal/model"
	"likha/internal/model/catalog"
)

const fixtureUpstream = `{
  "anthropic": {"id": "anthropic", "models": {
    "claude-x": {"id": "claude-x", "name": "Claude X", "tool_call": true, "reasoning": true,
      "last_updated": "2026-09-22", "modalities": {"input": ["text", "image"]},
      "limit": {"context": 1000000, "output": 128000},
      "cost": {"input": 4, "output": 20, "cache_read": 0.2, "cache_write": 5}},
    "embed-only": {"id": "embed-only", "name": "Embed", "tool_call": false,
      "limit": {"context": 8000, "output": 0}, "cost": {"input": 0.1, "output": 0}}
  }},
  "openai": {"id": "openai", "models": {
    "gpt-t": {"id": "gpt-t", "name": "GPT T", "tool_call": true, "status": "deprecated",
      "limit": {"context": 1050000, "input": 922000, "output": 128000},
      "cost": {"input": 5, "output": 30, "cache_read": 0.5,
        "tiers": [{"input": 10, "output": 45, "cache_read": 1, "tier": {"type": "context", "size": 272000}}],
        "context_over_200k": {"input": 10, "output": 45}}},
    "gpt-legacy-tier": {"id": "gpt-legacy-tier", "name": "Legacy tier", "tool_call": true,
      "limit": {"context": 400000, "output": 1000},
      "cost": {"input": 1, "output": 2, "context_over_200k": {"input": 2, "output": 4}}},
    "gpt-unpriced": {"id": "gpt-unpriced", "name": "Unpriced", "tool_call": true,
      "limit": {"context": 1000, "output": 100}}
  }}
}`

var fixtureProviders = []model.Provider{
	{Name: "claude", DefaultModel: "claude-x"},
	{Name: "openai", DefaultModel: "gpt-missing"},
	{Name: "chatgpt"},
}

var fixtureMapping = map[string]string{"claude": "anthropic", "openai": "openai", "chatgpt": ""}

func fixture(t *testing.T) map[string]upstreamProvider {
	t.Helper()
	var upstream map[string]upstreamProvider
	if err := json.Unmarshal([]byte(fixtureUpstream), &upstream); err != nil {
		t.Fatal(err)
	}
	return upstream
}

func overridesFrom(t *testing.T, raw string) overridesFile {
	t.Helper()
	o, err := parseOverrides([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestBuildConvertsUpstreamRows(t *testing.T) {
	file, err := build(fixture(t), overridesFile{}, fixtureProviders, fixtureMapping, "2026-10-04", DefaultUpstream)
	if err != nil {
		t.Fatal(err)
	}
	claude, ok := file.Lookup("claude", "claude-x")
	if !ok || claude.Name != "Claude X" || claude.Context != 1000000 || claude.Output != 128000 || claude.Source != catalog.SourceUpstream || claude.Updated != "2026-09-22" || !claude.Reasoning {
		t.Fatalf("claude-x row = %+v", claude)
	}
	if c := claude.Cost; c == nil || c.Input != 4 || c.Output != 20 || *c.CacheRead != 0.2 || *c.CacheWrite != 5 || c.Reasoning != nil {
		t.Fatalf("claude-x cost = %+v", claude.Cost)
	}
	if _, ok := file.Lookup("claude", "embed-only"); ok {
		t.Fatal("a model without tool calls was kept")
	}
	gpt, _ := file.Lookup("openai", "gpt-t")
	if gpt.Input != 922000 || gpt.Status != "deprecated" {
		t.Fatalf("input limit or status not kept: %+v", gpt)
	}
	// tiers wins over the duplicate context_over_200k field.
	if tiers := gpt.Cost.Tiers; len(tiers) != 1 || tiers[0].Above != 272000 || tiers[0].Input != 10 || tiers[0].Output != 45 || *tiers[0].CacheRead != 1 {
		t.Fatalf("gpt-t tiers = %+v", gpt.Cost.Tiers)
	}
	// context_over_200k aliases the first context tier, whatever its
	// threshold; without tiers the threshold is unknown, so the row stays
	// unpriced rather than assuming 200K.
	legacy, ok := file.Lookup("openai", "gpt-legacy-tier")
	if !ok || legacy.Cost != nil || legacy.Context != 400000 {
		t.Fatalf("context_over_200k without tiers must leave the row unpriced: %+v", legacy)
	}
	unpriced, ok := file.Lookup("openai", "gpt-unpriced")
	if !ok || unpriced.Cost != nil {
		t.Fatalf("a model without cost must stay unpriced: %+v", unpriced)
	}
	if p := file.Providers["chatgpt"]; p.Upstream != "" || len(p.Models) != 0 {
		t.Fatalf("curated-only provider got upstream rows: %+v", p)
	}
}

func TestBuildAppliesOverrides(t *testing.T) {
	o := overridesFrom(t, `{"providers": {
	  "chatgpt": {"billing": "subscription", "ref": "specs/chatgpt-plus/spec.md", "verified": "2026-10-04",
	    "models": {"gpt-sub": {"ref": "provider.go", "verified": "2026-10-04"}}},
	  "claude": {"models": {
	    "claude-x": {"context": 200000, "ref": "https://example.test/doc", "verified": "2026-10-01", "note": "1M needs a header"}}},
	  "openai": {"models": {"gpt-unpriced": {"drop": true, "ref": "https://example.test/retired", "verified": "2026-10-01"}}}
	}}`)
	file, err := build(fixture(t), o, fixtureProviders, fixtureMapping, "2026-10-04", DefaultUpstream)
	if err != nil {
		t.Fatal(err)
	}
	if file.Providers["chatgpt"].Billing != catalog.BillingSubscription {
		t.Fatal("billing override not applied")
	}
	sub, ok := file.Lookup("chatgpt", "gpt-sub")
	if !ok || sub.Source != catalog.SourceOverride || sub.Ref != "provider.go" {
		t.Fatalf("added override row = %+v", sub)
	}
	claude, _ := file.Lookup("claude", "claude-x")
	if claude.Context != 200000 || claude.Output != 128000 || claude.Cost == nil || claude.Cost.Input != 4 {
		t.Fatalf("override must change only the fields it names: %+v", claude)
	}
	if claude.Source != catalog.SourceOverride || claude.Verified != "2026-10-01" || claude.Note != "1M needs a header" {
		t.Fatalf("override provenance missing: %+v", claude)
	}
	// The ref vouches only for the replaced field, not the upstream prices.
	if len(claude.Overridden) != 1 || claude.Overridden[0] != "context" {
		t.Fatalf("overridden fields = %v, want [context]", claude.Overridden)
	}
	if len(sub.Overridden) != 0 {
		t.Fatalf("a curated row lists overridden fields: %v", sub.Overridden)
	}
	if _, ok := file.Lookup("openai", "gpt-unpriced"); ok {
		t.Fatal("drop override did not remove the model")
	}
}

func TestBuildRejectsBadInput(t *testing.T) {
	cases := map[string]string{
		"override without ref":        `{"providers": {"claude": {"models": {"claude-x": {"context": 1, "verified": "2026-10-01"}}}}}`,
		"override without verified":   `{"providers": {"claude": {"models": {"claude-x": {"context": 1, "ref": "x"}}}}}`,
		"bad verified date":           `{"providers": {"claude": {"models": {"claude-x": {"context": 1, "ref": "x", "verified": "Oct 1"}}}}}`,
		"billing without provenance":  `{"providers": {"chatgpt": {"billing": "subscription"}}}`,
		"unknown billing":             `{"providers": {"chatgpt": {"billing": "free", "ref": "x", "verified": "2026-10-01"}}}`,
		"unknown provider":            `{"providers": {"nope": {"models": {}}}}`,
		"drop of missing model":       `{"providers": {"claude": {"models": {"ghost": {"drop": true, "ref": "x", "verified": "2026-10-01"}}}}}`,
		"negative price":              `{"providers": {"claude": {"models": {"claude-x": {"cost": {"input": -1, "output": 1}, "ref": "x", "verified": "2026-10-01"}}}}}`,
		"cost without input":          `{"providers": {"claude": {"models": {"claude-x": {"cost": {"output": 5}, "ref": "x", "verified": "2026-10-01"}}}}}`,
		"cost without output":         `{"providers": {"claude": {"models": {"claude-x": {"cost": {"input": 1}, "ref": "x", "verified": "2026-10-01"}}}}}`,
		"tier without input":          `{"providers": {"claude": {"models": {"claude-x": {"cost": {"input": 1, "output": 5, "tiers": [{"above": 200000, "output": 10}]}, "ref": "x", "verified": "2026-10-01"}}}}}`,
		"note-only upstream override": `{"providers": {"claude": {"models": {"claude-x": {"note": "looks fine", "ref": "x", "verified": "2026-10-01"}}}}}`,
	}
	for name, raw := range cases {
		if _, err := build(fixture(t), overridesFrom(t, raw), fixtureProviders, fixtureMapping, "2026-10-04", DefaultUpstream); err == nil {
			t.Errorf("%s: build accepted it", name)
		}
	}
	unmapped := append([]model.Provider{}, fixtureProviders...)
	unmapped = append(unmapped, model.Provider{Name: "newcomer"})
	if _, err := build(fixture(t), overridesFile{}, unmapped, fixtureMapping, "2026-10-04", DefaultUpstream); err == nil {
		t.Error("a provider without a mapping row was accepted")
	}
	if _, err := build(fixture(t), overridesFile{}, fixtureProviders[:2], fixtureMapping, "2026-10-04", DefaultUpstream); err == nil {
		t.Error("a mapping row for a non-provider was accepted")
	}
	if _, err := build(map[string]upstreamProvider{}, overridesFile{}, fixtureProviders, fixtureMapping, "2026-10-04", DefaultUpstream); err == nil {
		t.Error("a missing upstream provider was accepted")
	}
}

// Bad upstream rows fail the run instead of being turned into unknowns or
// silently repriced (spec § The generator, step 5).
func TestBuildRejectsBadUpstreamRows(t *testing.T) {
	cases := map[string]string{
		"negative context":    `"limit": {"context": -1000, "output": 100}, "cost": {"input": 1, "output": 2}`,
		"negative output":     `"limit": {"context": 1000, "output": -5}, "cost": {"input": 1, "output": 2}`,
		"negative input":      `"limit": {"context": 1000, "input": -5, "output": 100}, "cost": {"input": 1, "output": 2}`,
		"negative tier size":  `"limit": {"context": 1000}, "cost": {"input": 1, "output": 2, "tiers": [{"input": 2, "output": 4, "tier": {"type": "context", "size": -5}}]}`,
		"zero tier size":      `"limit": {"context": 1000}, "cost": {"input": 1, "output": 2, "tiers": [{"input": 2, "output": 4, "tier": {"type": "context", "size": 0}}]}`,
		"non-context tier":    `"limit": {"context": 1000}, "cost": {"input": 1, "output": 2, "tiers": [{"input": 2, "output": 4, "tier": {"type": "input", "size": 272000}}]}`,
		"disagreeing over200": `"limit": {"context": 1000}, "cost": {"input": 1, "output": 2, "tiers": [{"input": 2, "output": 4, "tier": {"type": "context", "size": 272000}}], "context_over_200k": {"input": 3, "output": 4}}`,
	}
	for name, fields := range cases {
		upstream := fixture(t)
		var bad upstreamModel
		if err := json.Unmarshal([]byte(`{"name": "Bad", "tool_call": true, `+fields+`}`), &bad); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		upstream["openai"].Models["gpt-bad"] = bad
		if file, err := build(upstream, overridesFile{}, fixtureProviders, fixtureMapping, "2026-10-04", DefaultUpstream); err == nil {
			row, _ := file.Lookup("openai", "gpt-bad")
			t.Errorf("%s: build accepted it as %+v", name, row)
		}
	}
	// Zero and absent limits stay unknown.
	upstream := fixture(t)
	upstream["openai"].Models["gpt-zero"] = upstreamModel{Name: "Zero", ToolCall: true}
	file, err := build(upstream, overridesFile{}, fixtureProviders, fixtureMapping, "2026-10-04", DefaultUpstream)
	if err != nil {
		t.Fatal(err)
	}
	if row, ok := file.Lookup("openai", "gpt-zero"); !ok || row.Context != 0 || row.Output != 0 {
		t.Fatalf("zero limits = %+v, want unknown", row)
	}
}

// A misspelled key anywhere in overrides.json fails the run, so a typo can
// never stamp override provenance on an unchanged row or price input at $0.
func TestParseOverridesRejectsUnknownKeys(t *testing.T) {
	for name, raw := range map[string]string{
		"model field":    `{"providers": {"claude": {"models": {"claude-x": {"contxt": 200000, "ref": "x", "verified": "2026-10-04"}}}}}`,
		"provider field": `{"providers": {"chatgpt": {"biling": "subscription", "ref": "x", "verified": "2026-10-04"}}}`,
		"top level":      `{"provders": {}}`,
		"cost field":     `{"providers": {"claude": {"models": {"claude-x": {"cost": {"inptu": 1, "output": 5}, "ref": "x", "verified": "2026-10-04"}}}}}`,
		"tier field":     `{"providers": {"claude": {"models": {"claude-x": {"cost": {"input": 1, "output": 5, "tiers": [{"abve": 1, "input": 2, "output": 6}]}, "ref": "x", "verified": "2026-10-04"}}}}}`,
		"trailing data":  `{"providers": {}} {}`,
	} {
		if _, err := parseOverrides([]byte(raw)); err == nil {
			t.Errorf("%s: parseOverrides accepted %s", name, raw)
		}
	}
	o, err := parseOverrides([]byte(`{"providers": {"claude": {"models": {"claude-x": {"cost": {"input": 1, "output": 5, "cache_read": 0.1,
	  "tiers": [{"above": 200000, "input": 2, "output": 10}]}, "ref": "x", "verified": "2026-10-04"}}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	file, err := build(fixture(t), o, fixtureProviders, fixtureMapping, "2026-10-04", DefaultUpstream)
	if err != nil {
		t.Fatal(err)
	}
	claude, _ := file.Lookup("claude", "claude-x")
	if c := claude.Cost; c == nil || c.Input != 1 || c.Output != 5 || *c.CacheRead != 0.1 || c.CacheWrite != nil || len(c.Tiers) != 1 || c.Tiers[0].Above != 200000 || c.Tiers[0].Input != 2 || c.Tiers[0].Output != 10 {
		t.Fatalf("override cost card = %+v", claude.Cost)
	}
}

func TestReportListsChangesAndDefaultGaps(t *testing.T) {
	old, err := build(fixture(t), overridesFile{}, fixtureProviders, fixtureMapping, "2026-10-01", DefaultUpstream)
	if err != nil {
		t.Fatal(err)
	}
	upstream := fixture(t)
	claude := upstream["anthropic"].Models["claude-x"]
	five := 5.0
	claude.Cost.Input = &five
	upstream["anthropic"].Models["claude-x"] = claude
	delete(upstream["openai"].Models, "gpt-unpriced")
	next, err := build(upstream, overridesFile{}, fixtureProviders, fixtureMapping, "2026-10-04", DefaultUpstream)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	writeReport(&buf, old, next, fixtureProviders)
	report := buf.String()
	for _, want := range []string{
		"~ claude/claude-x [ctx 1000000 in 0 out 128000, $4/$20 cache $0.2 write $5, models.dev] -> [ctx 1000000 in 0 out 128000, $5/$20 cache $0.2 write $5, models.dev]",
		"- openai/gpt-unpriced",
		"default without entry: openai/gpt-missing",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("report missing %q:\n%s", want, report)
		}
	}
}

// Every value RequestCost reads is in the report: a change to only a cache
// write rate, a reasoning rate, a tier's rate or threshold, the status, or
// the source is listed.
func TestReportListsEveryPriceCardChange(t *testing.T) {
	old, err := build(fixture(t), overridesFile{}, fixtureProviders, fixtureMapping, "2026-10-01", DefaultUpstream)
	if err != nil {
		t.Fatal(err)
	}
	f := func(v float64) *float64 { return &v }
	cases := map[string]func(map[string]upstreamProvider){
		"cache write": func(u map[string]upstreamProvider) { u["anthropic"].Models["claude-x"].Cost.CacheWrite = f(50) },
		"reasoning":   func(u map[string]upstreamProvider) { u["anthropic"].Models["claude-x"].Cost.Reasoning = f(40) },
		"tier rate":   func(u map[string]upstreamProvider) { u["openai"].Models["gpt-t"].Cost.Tiers[0].CacheRead = f(9) },
		"tier threshold": func(u map[string]upstreamProvider) {
			u["openai"].Models["gpt-t"].Cost.Tiers[0].Tier.Size = 300000
		},
		"status": func(u map[string]upstreamProvider) {
			m := u["anthropic"].Models["claude-x"]
			m.Status = "deprecated"
			u["anthropic"].Models["claude-x"] = m
		},
	}
	for name, mutate := range cases {
		upstream := fixture(t)
		mutate(upstream)
		next, err := build(upstream, overridesFile{}, fixtureProviders, fixtureMapping, "2026-10-04", DefaultUpstream)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var buf bytes.Buffer
		writeReport(&buf, old, next, fixtureProviders)
		if !strings.Contains(buf.String(), "1 model changes:") {
			t.Errorf("%s: change missing from the report:\n%s", name, buf.String())
		}
	}
	next, err := build(fixture(t), overridesFrom(t, `{"providers": {"claude": {"models": {"claude-x": {"context": 200000, "ref": "x", "verified": "2026-10-04"}}}}}`), fixtureProviders, fixtureMapping, "2026-10-04", DefaultUpstream)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	writeReport(&buf, old, next, fixtureProviders)
	if want := "-> [ctx 200000 in 0 out 128000, $4/$20 cache $0.2 write $5, override (context)]"; !strings.Contains(buf.String(), want) {
		t.Errorf("source change missing %q:\n%s", want, buf.String())
	}
}

// The same upstream input reproduces the file byte for byte, keeping the
// previous generated date when nothing changed; a failed run writes nothing.
func TestRunIsDeterministic(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "upstream.json")
	overrides := filepath.Join(dir, "overrides.json")
	out := filepath.Join(dir, "models.json")
	if err := os.WriteFile(overrides, []byte(`{"providers": {}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// A fixture missing mapped providers fails without writing.
	if err := os.WriteFile(in, []byte(fixtureUpstream), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run(DefaultUpstream, in, overrides, out, "2026-10-01", &bytes.Buffer{}); err == nil {
		t.Fatal("run accepted an upstream file missing mapped providers")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("a failed run wrote the catalog")
	}
	upstream := fixture(t)
	for _, id := range upstreamIDs {
		if _, ok := upstream[id]; id != "" && !ok {
			upstream[id] = upstreamProvider{ID: id, Models: map[string]upstreamModel{}}
		}
	}
	full, err := json.Marshal(upstream)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(in, full, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run(DefaultUpstream, in, overrides, out, "2026-10-01", &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(out)
	var report bytes.Buffer
	if err := run(DefaultUpstream, in, overrides, out, "2026-10-04", &report); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(out)
	if !bytes.Equal(first, second) || !strings.Contains(report.String(), "models.json unchanged") {
		t.Fatalf("same input did not reproduce the file:\n%s", report.String())
	}
	if f := mustParse(t, second); f.Generated != "2026-10-01" {
		t.Fatalf("unchanged data moved the generated date to %s", f.Generated)
	}
	if f := mustParse(t, second); f.Upstream.URL != DefaultUpstream {
		t.Fatalf("an explicit upstream URL was not recorded: %+v", f.Upstream)
	}
	// A local file without a stated download URL is recorded as the file,
	// never as models.dev.
	local := filepath.Join(dir, "local.json")
	report.Reset()
	if err := run("", in, overrides, local, "2026-10-04", &report); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(local)
	if f, want := mustParse(t, data), "file:"+filepath.ToSlash(in); f.Upstream.URL != want || !strings.Contains(report.String(), "from "+want) {
		t.Fatalf("local input recorded as %+v, report:\n%s", f.Upstream, report.String())
	}
}

// The committed file is valid and in the generator's canonical encoding,
// so it was generated, not hand-edited.
func TestCommittedCatalogIsCanonical(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := encode(mustParse(t, data))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, data) {
		t.Fatal("committed models.json is not in the generator's canonical encoding")
	}
}

func mustParse(t *testing.T, data []byte) *catalog.File {
	t.Helper()
	f, err := catalog.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
