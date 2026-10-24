package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"likha/internal/model"
)

func TestPanicDetailReachesCallError(t *testing.T) {
	cases := []struct {
		name      string
		authorize Authorizer
		run       Handler
	}{
		{
			name:      "handler",
			authorize: func(context.Context, json.RawMessage) error { return nil },
			run:       func(context.Context, json.RawMessage) (Result, error) { panic("index 7 out of range") },
		},
		{
			name:      "authorizer",
			authorize: func(context.Context, json.RawMessage) error { panic("index 7 out of range") },
			run:       func(context.Context, json.RawMessage) (Result, error) { return Result{Content: "ran"}, nil },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			registry := New()
			err := registry.Register(Tool{
				Definition: model.ToolDefinition{Name: "boom", Parameters: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)},
				Source:     Source{Kind: "builtin"}, Effects: []Effect{Write}, Target: "repository",
				Authorize: tc.authorize, Run: tc.run,
			})
			if err != nil {
				t.Fatal(err)
			}
			result, err := registry.Invoke(context.Background(), Scope{Agent: "main"}, model.ToolCall{ID: "c1", Name: "boom", Arguments: `{}`})
			var callErr *CallError
			if !errors.As(err, &callErr) || callErr.Cause == nil {
				t.Fatalf("Invoke error = %v (%+v)", err, result)
			}
			if !strings.Contains(callErr.Cause.Error(), "index 7 out of range") {
				t.Fatalf("cause = %q, want the recovered panic value", callErr.Cause)
			}
			if result.Status != Failed && result.Status != Refused {
				t.Fatalf("status = %s", result.Status)
			}
		})
	}
}
