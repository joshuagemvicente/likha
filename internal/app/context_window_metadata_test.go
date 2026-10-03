package app

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"likha/internal/model"
)

func withModelDetailsFunc(t *testing.T, fn func(context.Context, string, string) ([]model.ModelDetails, error)) {
	t.Helper()
	previous := listModelDetailsFunc
	listModelDetailsFunc = fn
	t.Cleanup(func() { listModelDetailsFunc = previous })
}

func TestDiscoverContextWindowUsesConfiguredModelPositiveMetadata(t *testing.T) {
	var called bool
	withModelDetailsFunc(t, func(ctx context.Context, endpoint, apiKey string) ([]model.ModelDetails, error) {
		called = true
		if endpoint != "https://provider.example/v1" || apiKey != "provider-key" {
			t.Fatalf("metadata request = (%q, %q), want configured endpoint and key", endpoint, apiKey)
		}
		return []model.ModelDetails{
			{ID: "other-model", ContextWindow: 90_000},
			{ID: "selected-model", ContextWindow: 128_000},
		}, nil
	})

	got := discoverContextWindow("selected-model", "https://provider.example/v1", "provider-key")
	if !called {
		t.Fatal("metadata request was not made")
	}
	want := map[string]int64{"selected-model": 128_000}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("context windows = %#v, want %#v", got, want)
	}
}

func TestDiscoverContextWindowReportsEveryModelToObserver(t *testing.T) {
	wantDetails := []model.ModelDetails{
		{ID: "selected-model", ContextWindow: 128_000, ContextWindowSource: "context_length"},
		{ID: "other-model"},
	}
	withModelDetailsFunc(t, func(context.Context, string, string) ([]model.ModelDetails, error) {
		return wantDetails, nil
	})

	var observed bool
	got := discoverContextWindowWithObserver("selected-model", "https://provider.example/v1", "key", "openrouter", func(provider, operation string, elapsed time.Duration, details []model.ModelDetails, err error) {
		observed = true
		if provider != "openrouter" || operation != "startup /models" || elapsed < 0 || err != nil {
			t.Fatalf("observer event = (%q, %q, %s, %v), want successful startup model metadata", provider, operation, elapsed, err)
		}
		if !reflect.DeepEqual(details, wantDetails) {
			t.Fatalf("observed models = %#v, want %#v", details, wantDetails)
		}
	})
	if !observed {
		t.Fatal("model metadata observer was not called")
	}
	if got["selected-model"] != 128_000 {
		t.Fatalf("context windows = %#v, want selected-model:128000", got)
	}
}

func TestDiscoverContextWindowTreatsMissingOrInvalidValuesAsUnknown(t *testing.T) {
	for _, tc := range []struct {
		name    string
		details []model.ModelDetails
	}{
		{name: "missing model", details: []model.ModelDetails{{ID: "other", ContextWindow: 128_000}}},
		{name: "zero window", details: []model.ModelDetails{{ID: "selected-model"}}},
		{name: "negative window", details: []model.ModelDetails{{ID: "selected-model", ContextWindow: -1}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withModelDetailsFunc(t, func(context.Context, string, string) ([]model.ModelDetails, error) {
				return tc.details, nil
			})
			if got := discoverContextWindow("selected-model", "https://provider.example/v1", "key"); got != nil {
				t.Fatalf("context windows = %#v, want unknown", got)
			}
		})
	}
}

func TestDiscoverContextWindowFailureIsNonFatalAndBounded(t *testing.T) {
	withModelDetailsFunc(t, func(ctx context.Context, _, _ string) ([]model.ModelDetails, error) {
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Fatal("metadata request has no deadline")
		}
		remaining := time.Until(deadline)
		if remaining <= 0 || remaining > contextWindowMetadataTimeout {
			t.Fatalf("metadata deadline remaining = %s, want within %s", remaining, contextWindowMetadataTimeout)
		}
		return nil, errors.New("provider unavailable")
	})
	if got := discoverContextWindow("selected-model", "https://provider.example/v1", "key"); got != nil {
		t.Fatalf("failed metadata request produced context windows: %#v", got)
	}
}

func TestDiscoverContextWindowSkipsMissingConfiguration(t *testing.T) {
	called := false
	withModelDetailsFunc(t, func(context.Context, string, string) ([]model.ModelDetails, error) {
		called = true
		return nil, nil
	})
	for _, args := range [][3]string{
		{"", "https://provider.example/v1", "key"},
		{"selected-model", "", "key"},
	} {
		if got := discoverContextWindow(args[0], args[1], args[2]); got != nil {
			t.Fatalf("missing configuration produced metadata: %#v", got)
		}
	}
	if called {
		t.Fatal("metadata request ran without a configured model or endpoint")
	}
}
