package effortprobe

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"likha/internal/model"
)

func provider(t *testing.T, name string) model.Provider {
	t.Helper()
	p, ok := model.LookupProvider(name)
	if !ok {
		t.Fatalf("unknown provider %s", name)
	}
	return p
}

func shapeNames(shapes []Shape) []string {
	names := make([]string, 0, len(shapes))
	for _, s := range shapes {
		names = append(names, s.Label())
	}
	return names
}

func levelNames(levels []Level) []string {
	names := make([]string, 0, len(levels))
	for _, l := range levels {
		name := l.Name
		if !l.Published {
			name += "*"
		}
		names = append(names, name)
	}
	return names
}

func TestNewPlan(t *testing.T) {
	cases := []struct {
		name         string
		provider     model.Provider
		model        string
		live         []string
		opts         Options
		wantErr      string
		wantShapes   []string
		wantDoc      string
		wantLevels   []string // unpublished marked with *
		wantSource   string
		wantOverride bool
		wantRequests int
	}{
		{
			name: "documented shape", provider: provider(t, "openai"), model: "gpt-5.5",
			wantShapes: []string{"reasoning_effort"}, wantDoc: "reasoning_effort",
			wantLevels: gpt55Levels, wantSource: SourceCatalog, wantRequests: 7,
		},
		{
			name: "openrouter documents the object", provider: provider(t, "openrouter"), model: "any/model",
			live:       []string{"low", "high"},
			wantShapes: []string{"reasoning.effort"}, wantDoc: "reasoning.effort",
			wantLevels: []string{"low", "high"}, wantSource: SourceLive, wantRequests: 4,
		},
		{
			name: "relay probes both shapes", provider: provider(t, "opencode-go"), model: "deepseek-v4-flash",
			wantShapes: []string{"reasoning_effort", "reasoning.effort"},
			wantLevels: []string{"low", "high", "max"}, wantSource: SourceCatalog, wantRequests: 9,
		},
		{
			name: "probe-wire overrides the documented shape", provider: provider(t, "openai"), model: "gpt-5.5",
			opts:       Options{Wire: "object"},
			wantShapes: []string{"reasoning.effort"}, wantLevels: gpt55Levels, wantSource: SourceCatalog, wantRequests: 7,
		},
		{
			name: "probe-wire both keeps the documented shape for the recommendation", provider: provider(t, "openai"), model: "gpt-5.5",
			opts:       Options{Wire: "both"},
			wantShapes: []string{"reasoning_effort", "reasoning.effort"}, wantDoc: "reasoning_effort",
			wantLevels: gpt55Levels, wantSource: SourceCatalog, wantRequests: 13,
		},
		{
			name: "chatgpt has one shape", provider: provider(t, "chatgpt"), model: "gpt-5.5",
			opts:    Options{Wire: "top", Levels: []string{"low"}},
			wantErr: "--probe-wire does not apply to chatgpt",
		},
		{
			name: "chatgpt probes Responses with candidate levels", provider: provider(t, "chatgpt"), model: "gpt-5.5",
			opts:       Options{Levels: []string{"low", "high"}},
			wantShapes: []string{"reasoning.effort (Responses)"}, wantDoc: "reasoning.effort (Responses)",
			wantLevels: []string{"low*", "high*"}, wantSource: SourceFlag, wantRequests: 4,
		},
		{
			name: "probe-levels keeps published order and puts unpublished last", provider: provider(t, "openai"), model: "gpt-5.5",
			opts:       Options{Levels: []string{"max", "high", "low"}},
			wantShapes: []string{"reasoning_effort"}, wantDoc: "reasoning_effort",
			wantLevels: []string{"low", "high", "max*"}, wantSource: SourceFlag, wantRequests: 5,
		},
		{
			name: "probe-levels follows the catalog's level rules", provider: provider(t, "openai"), model: "gpt-5.5",
			opts: Options{Levels: []string{"High"}}, wantErr: "lower-case",
		},
		{
			name: "probe-levels refuses default", provider: provider(t, "openai"), model: "gpt-5.5",
			opts: Options{Levels: []string{"default"}}, wantErr: "reserved",
		},
		{
			name: "probe-levels refuses the control level", provider: provider(t, "openai"), model: "gpt-5.5",
			opts: Options{Levels: []string{ControlLevel}}, wantErr: "control level",
		},
		{
			name: "no levels", provider: provider(t, "dialagram"), model: "some-model",
			wantErr: "some-model publishes no effort levels on dialagram; nothing to probe (use --probe-levels to test candidate levels)",
		},
		{
			name: "no provider", provider: model.Provider{}, model: "m",
			wantErr: "custom endpoints cannot be probed",
		},
		{
			name: "no model", provider: provider(t, "openai"),
			wantErr: "a model is required",
		},
		{
			name: "endpoint override", provider: provider(t, "openai"), model: "gpt-5.5",
			opts:       Options{Endpoint: "http://127.0.0.1:9/v1"},
			wantShapes: []string{"reasoning_effort"}, wantDoc: "reasoning_effort",
			wantLevels: gpt55Levels, wantSource: SourceCatalog, wantOverride: true, wantRequests: 7,
		},
		{
			name: "provider's own endpoint is no override", provider: provider(t, "openai"), model: "gpt-5.5",
			opts:       Options{Endpoint: "https://api.openai.com/v1/"},
			wantShapes: []string{"reasoning_effort"}, wantDoc: "reasoning_effort",
			wantLevels: gpt55Levels, wantSource: SourceCatalog, wantRequests: 7,
		},
		{
			name: "plan above 17 requests", provider: provider(t, "openai"), model: "gpt-5.5",
			opts:    Options{Wire: "both", Levels: []string{"none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra"}},
			wantErr: "the plan needs 19 requests, above the cap of 17",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := NewPlan(tc.provider, tc.model, tc.live, tc.opts)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := shapeNames(plan.Shapes); !slices.Equal(got, tc.wantShapes) {
				t.Fatalf("shapes = %v, want %v", got, tc.wantShapes)
			}
			doc := ""
			if plan.Documented != nil {
				doc = plan.Documented.Label()
			}
			if doc != tc.wantDoc {
				t.Fatalf("documented = %q, want %q", doc, tc.wantDoc)
			}
			if got := levelNames(plan.Levels); !slices.Equal(got, tc.wantLevels) {
				t.Fatalf("levels = %v, want %v", got, tc.wantLevels)
			}
			if plan.LevelSource != tc.wantSource || plan.EndpointOverride != tc.wantOverride {
				t.Fatalf("source = %q override = %t", plan.LevelSource, plan.EndpointOverride)
			}
			if got := len(plan.Requests()); got != tc.wantRequests {
				t.Fatalf("requests = %d, want %d", got, tc.wantRequests)
			}
		})
	}
}

func TestNewPlanNoLevelsIsTyped(t *testing.T) {
	_, err := NewPlan(provider(t, "dialagram"), "some-model", nil, Options{})
	if !errors.Is(err, ErrNoLevels) {
		t.Fatalf("err = %v, want ErrNoLevels", err)
	}
}

func TestRequestsOrder(t *testing.T) {
	plan, err := NewPlan(provider(t, "opencode-go"), "deepseek-v4-flash", nil, Options{Levels: []string{"high", "turbo"}})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range plan.Requests() {
		got = append(got, string(r.Kind)+":"+r.Shape.Name+":"+r.Level)
	}
	want := []string{
		"default::",
		"level:reasoning_effort:high", "level:reasoning_effort:turbo", "control:reasoning_effort:" + ControlLevel,
		"level:reasoning.effort:high", "level:reasoning.effort:turbo", "control:reasoning.effort:" + ControlLevel,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("requests = %v, want %v", got, want)
	}
}

func TestNewPlanLeavesProviderTableAlone(t *testing.T) {
	before := slices.Clone(model.Providers)
	if _, err := NewPlan(provider(t, "openai"), "gpt-5.5", nil, Options{}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(before, model.Providers) {
		t.Fatal("NewPlan changed the provider table")
	}
	if p, _ := model.LookupProvider("openai"); p.Effort != model.EffortUnsupported {
		t.Fatal("openai's row now carries effort")
	}
}
