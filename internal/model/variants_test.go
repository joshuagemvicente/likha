package model

import (
	"encoding/json"
	"reflect"
	"testing"

	"likha/internal/model/catalog"
)

func TestVariantCycleDefinitionOrder(t *testing.T) {
	choices := []Variant{{ID: "high"}, {ID: "low"}}
	for _, tc := range []struct{ current, want string }{{"", "high"}, {"high", "low"}, {"low", ""}, {"gone", ""}, {"default", "high"}} {
		if got := CycleVariant(tc.current, choices); got != tc.want {
			t.Errorf("CycleVariant(%q) = %q, want %q", tc.current, got, tc.want)
		}
	}
	if CycleVariant("", nil) != "" || CycleVariant("one", []Variant{{ID: "one"}}) != "" {
		t.Fatal("zero/one variant wrap changed")
	}
}

func TestVariantReferencesPreserveSlashModelIDs(t *testing.T) {
	want := Selection{Provider: "openrouter", Model: "anthropic/claude-opus-5.5", Variant: "high"}
	got, err := ParseModelReference(want.String())
	if err != nil || got != want {
		t.Fatalf("reference = %+v, %v", got, err)
	}
	for _, bad := range []string{"model", "/model", "p/", "p/m#", "p/m#x#y"} {
		if _, err := ParseModelReference(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	id, choice, err := SplitModelVariant("anthropic/claude-opus-5.5#default")
	if err != nil || id != want.Model || choice == nil || *choice != "" {
		t.Fatalf("explicit Default = %q, %v, %v", id, choice, err)
	}
}

func TestPublishedVariantsAndImpossibleBudgets(t *testing.T) {
	p, _ := LookupProvider("claude")
	choices, err := ModelVariants(p, "claude-haiku-4-5", nil, nil)
	if err != nil || len(choices) != 2 || choices[0].ID != "high" || choices[1].ID != "max" {
		t.Fatalf("manual budget variants = %+v, %v", choices, err)
	}
	minimum := int64(1024)
	live := &ReasoningCapabilities{ControlsKnown: true, Controls: []catalog.ReasoningOption{{Type: catalog.OptionBudgetTokens, Min: &minimum}}, Output: 512}
	choices, err = ModelVariants(p, "claude-haiku-4-5", live, nil)
	if err != nil || len(choices) != 0 {
		t.Fatalf("impossible budget = %+v, %v", choices, err)
	}
	unknown, _ := LookupProvider("dialagram")
	if choices, err = ModelVariants(unknown, "openai/gpt-5.5", nil, nil); err != nil || len(choices) != 0 {
		t.Fatalf("undocumented route = %+v, %v", choices, err)
	}
}

func TestLiveReasoningPresenceAndMandatory(t *testing.T) {
	p, _ := LookupProvider("openrouter")
	for _, tc := range []struct {
		raw   string
		count int
		off   bool
	}{
		{`{"mandatory":false,"supported_efforts":[]}`, 2, true},
		{`{"mandatory":true,"supported_efforts":[]}`, 0, false},
		{`{"mandatory":true,"supported_efforts":null}`, 6, false},
		{`{"mandatory":true,"supported_efforts":["high","low"]}`, 2, false},
	} {
		live := decodeReasoningCapabilities(json.RawMessage(tc.raw))
		choices, err := ModelVariants(p, "acme/live-only", live, nil)
		if err != nil || len(choices) != tc.count {
			t.Errorf("%s: variants=%+v, err=%v", tc.raw, choices, err)
			continue
		}
		if HasVariant(choices, "none") != tc.off {
			t.Errorf("%s: thinking-off availability wrong", tc.raw)
		}
	}
}

func TestVariantOverlayMergeAndProtection(t *testing.T) {
	base := Overlay{Body: map[string]any{"reasoning": map[string]any{"effort": "low", "enabled": true}}, Headers: map[string]string{"X-Mode": "old"}}
	other := Overlay{Body: map[string]any{"reasoning": map[string]any{"effort": "high"}}, Headers: map[string]string{"x-mode": "new"}}
	merged := MergeOverlay(base, other)
	if !reflect.DeepEqual(merged.Body["reasoning"], map[string]any{"effort": "high", "enabled": true}) || merged.Headers["x-mode"] != "new" {
		t.Fatalf("merge = %+v", merged)
	}
	merged.Body["reasoning"].(map[string]any)["effort"] = "changed"
	if base.Body["reasoning"].(map[string]any)["effort"] != "low" {
		t.Fatal("merge mutated baseline")
	}
	for _, bad := range []Overlay{{Body: map[string]any{"tools": []any{}}}, {Headers: map[string]string{"AUTHORIZATION": "secret"}}, {Settings: map[string]any{"invented": true}}} {
		if err := ValidateOverlay(bad); err == nil {
			t.Fatalf("accepted protected/unknown overlay: %+v", bad)
		}
	}
}

func TestMalformedLiveReasoningFallsBackToCatalog(t *testing.T) {
	p, _ := LookupProvider("openrouter")
	const id = "openai/gpt-5.5"
	documented, err := ModelVariants(p, id, nil, nil)
	if err != nil || len(documented) == 0 {
		t.Fatalf("catalog variants = %+v, %v", documented, err)
	}
	for _, raw := range []string{
		`{"mandatory":null}`,
		`"not an object"`,
		`{"mandatory":true,"supported_efforts":"high"}`,
		`{"supported_efforts":["high","high"]}`,
		`{"supports_max_tokens":true,"default_effort":7}`,
	} {
		t.Run(raw, func(t *testing.T) {
			live := decodeReasoningCapabilities(json.RawMessage(raw))
			if live == nil || live.Invalid == "" {
				t.Fatalf("decode(%s) did not record why the metadata was ignored: %+v", raw, live)
			}
			got, err := ModelVariants(p, id, live, nil)
			if err != nil {
				t.Fatalf("ModelVariants: %v", err)
			}
			if !reflect.DeepEqual(got, documented) {
				t.Fatalf("variants = %+v, want catalog %+v", got, documented)
			}
			client, err := New(p.BaseURL, id, "test-key")
			if err != nil {
				t.Fatal(err)
			}
			if err := client.SetVariant(p, live, VariantConfig{}, ""); err != nil {
				t.Fatalf("SetVariant(Default): %v", err)
			}
			if err := client.SetVariant(p, live, VariantConfig{}, "high"); err != nil {
				t.Fatalf("SetVariant(high): %v", err)
			}
		})
	}
}
