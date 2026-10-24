package model

import (
	"slices"
	"strings"
	"testing"

	"likha/internal/model/catalog"
)

// Every shipped (provider, model) pair must resolve its generated variant list
// and select Default; one bad generated control must never make a model
// unusable.
func TestEveryCatalogPairResolvesVariantsAndDefault(t *testing.T) {
	file, err := catalog.Default()
	if err != nil {
		t.Fatal(err)
	}
	providers := make([]string, 0, len(file.Providers))
	for name := range file.Providers {
		providers = append(providers, name)
	}
	slices.Sort(providers)
	for _, name := range providers {
		p, ok := LookupProvider(name)
		if !ok {
			continue
		}
		models := make([]string, 0, len(file.Providers[name].Models))
		for id := range file.Providers[name].Models {
			models = append(models, id)
		}
		slices.Sort(models)
		for _, id := range models {
			t.Run(name+"/"+id, func(t *testing.T) {
				variants, err := ModelVariants(p, id, nil, nil)
				if err != nil {
					t.Fatalf("ModelVariants: %v", err)
				}
				client := sweepClient(t, p, id)
				if err := client.SetVariant(p, nil, VariantConfig{}, ""); err != nil {
					t.Fatalf("SetVariant(Default): %v", err)
				}
				for _, variant := range variants {
					if err := client.SetVariant(p, nil, VariantConfig{}, variant.ID); err != nil {
						t.Errorf("SetVariant(%s): %v", variant.ID, err)
					}
				}
			})
		}
	}
}

func sweepClient(t *testing.T, p Provider, id string) *Client {
	t.Helper()
	var client *Client
	var err error
	if p.Auth == AuthOAuth {
		client, err = NewOAuth(ChatGPTResource, id, ChatGPTIssuer, "issued-client-test", validOAuthCredentials())
	} else {
		client, err = New(p.BaseURL, id, "test-key")
	}
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestGeneratedVariantsThatFailValidationAreNotOffered(t *testing.T) {
	adaptive := true
	cases := []struct {
		name     string
		provider string
		id       string
		live     *ReasoningCapabilities
		want     []string
	}{
		// The catalog row publishes only a toggle, so Gemini's dynamic
		// budget (-1) cannot represent "on"; only "none" is generated.
		{"gemini toggle-only row", "gemini", "gemma-4-31b-it", nil, []string{"none"}},
		// Live metadata says adaptive while the row publishes manual budgets:
		// the budget presets cannot be sent, so they are dropped, not fatal.
		{"claude live adaptive over manual budgets", "claude", "claude-haiku-4-5", &ReasoningCapabilities{Adaptive: &adaptive}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := LookupProvider(tc.provider)
			variants, err := ModelVariants(p, tc.id, tc.live, nil)
			if err != nil {
				t.Fatalf("ModelVariants: %v", err)
			}
			var got []string
			for _, variant := range variants {
				got = append(got, variant.ID)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("variants = %v, want %v", got, tc.want)
			}
			client := sweepClient(t, p, tc.id)
			if err := client.SetVariant(p, tc.live, VariantConfig{}, ""); err != nil {
				t.Fatalf("SetVariant(Default): %v", err)
			}
		})
	}
}

func TestConfiguredVariantThatFailsValidationStillErrors(t *testing.T) {
	p, _ := LookupProvider("gemini")
	presets := []Variant{{ID: "on", Overlay: Overlay{Settings: map[string]any{"thinkingConfig": map[string]any{"thinkingBudget": int64(-1)}}}}}
	if _, err := ModelVariants(p, "gemma-4-31b-it", nil, presets); err == nil {
		t.Fatal("ModelVariants accepted a configured variant the model cannot carry")
	}
}

func TestVariantPayloadPreservesNumberLiterals(t *testing.T) {
	p, _ := LookupProvider("openai")
	client, err := New(p.BaseURL, "gpt-5.5", "test-key")
	if err != nil {
		t.Fatal(err)
	}
	if err := client.SetVariant(p, nil, VariantConfig{}, "high"); err != nil {
		t.Fatal(err)
	}
	payload := `{"model":"gpt-5.5","seed":9007199254740993,"temperature":1.0,"input":[{"tokens":1e2}]}`
	got, err := client.variantPayload([]byte(payload))
	if err != nil {
		t.Fatal(err)
	}
	for _, literal := range []string{`"seed":9007199254740993`, `"temperature":1.0`, `"tokens":1e2`, `"effort":"high"`} {
		if !strings.Contains(string(got), literal) {
			t.Errorf("variant payload lost %s: %s", literal, got)
		}
	}
}
