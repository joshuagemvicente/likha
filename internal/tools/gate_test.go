package tools

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"likha/internal/model"
)

// recordingGate blocks the calls named in block and records what it saw.
type recordingGate struct {
	mu     sync.Mutex
	block  map[string]error
	before []GateCall
	after  []Result
}

func (g *recordingGate) BeforeTool(_ context.Context, _ Scope, call GateCall) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.before = append(g.before, call)
	return g.block[call.Name]
}

func (g *recordingGate) AfterTool(_ context.Context, _ Scope, _ GateCall, result Result) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.after = append(g.after, result)
}

// gatedTool registers a write tool whose authorizer and handler count calls.
func gatedTool(t *testing.T, registry *Registry, name string, authorized, ran *int, status Status) {
	t.Helper()
	err := registry.Register(Tool{
		Definition: model.ToolDefinition{Name: name, Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`)},
		Source:     Source{Kind: "builtin"}, Effects: []Effect{Write}, Target: "repository",
		Authorize: func(context.Context, json.RawMessage) error {
			*authorized++
			return nil
		},
		Run: func(context.Context, json.RawMessage) (Result, error) {
			*ran++
			return Result{Status: status, Content: "done"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestGateBlockSkipsAuthorizeAndRun(t *testing.T) {
	registry := New()
	var authorized, ran int
	gatedTool(t, registry, "edit_file", &authorized, &ran, Succeeded)
	cause := errors.New(`Blocked by hook "guard": not here`)
	gate := &recordingGate{block: map[string]error{"edit_file": cause}}
	registry.SetGate(gate)
	result, err := registry.Invoke(context.Background(), Scope{Agent: "main"}, model.ToolCall{ID: "c1", Name: "edit_file", Arguments: `{"path":"a.go"}`})
	if authorized != 0 || ran != 0 {
		t.Fatalf("blocked call reached authorize=%d run=%d", authorized, ran)
	}
	var callErr *CallError
	if !errors.As(err, &callErr) || callErr.Code != "hook_blocked" || !errors.Is(err, cause) {
		t.Fatalf("err = %v", err)
	}
	if result.Status != Refused || result.Content != `hook_blocked: Blocked by hook "guard": not here` || result.Executed {
		t.Fatalf("result = %+v", result)
	}
	if len(gate.after) != 0 {
		t.Fatal("a blocked call must not be reported as run to AfterTool")
	}
	if len(gate.before) != 1 || gate.before[0].Name != "edit_file" || string(gate.before[0].Arguments) != `{"path":"a.go"}` || gate.before[0].Effects[0] != Write {
		t.Fatalf("gate saw %+v", gate.before)
	}
}

func TestGateSeesOnlyValidatedCalls(t *testing.T) {
	registry := New()
	var authorized, ran int
	gatedTool(t, registry, "edit_file", &authorized, &ran, Succeeded)
	gate := &recordingGate{}
	registry.SetGate(gate)
	_, err := registry.Invoke(context.Background(), Scope{Agent: "main"}, model.ToolCall{ID: "c1", Name: "edit_file", Arguments: `{"nope":1}`})
	var callErr *CallError
	if !errors.As(err, &callErr) || callErr.Code != "invalid_arguments" {
		t.Fatalf("err = %v", err)
	}
	if len(gate.before) != 0 || len(gate.after) != 0 {
		t.Fatal("invalid arguments must be refused before the gate runs")
	}
}

func TestGateAllowsAndObservesFinalStatus(t *testing.T) {
	registry := New()
	var authorized, ran int
	gatedTool(t, registry, "edit_file", &authorized, &ran, Limited)
	gate := &recordingGate{}
	registry.SetGate(gate)
	result, err := registry.Invoke(context.Background(), Scope{Agent: "main"}, model.ToolCall{ID: "c1", Name: "edit_file", Arguments: `{"path":"a.go"}`})
	if err != nil || authorized != 1 || ran != 1 {
		t.Fatalf("err=%v authorized=%d ran=%d", err, authorized, ran)
	}
	if len(gate.after) != 1 || gate.after[0].Status != Limited || gate.after[0].Status != result.Status || !gate.after[0].Executed {
		t.Fatalf("AfterTool saw %+v, Invoke returned %+v", gate.after, result)
	}
}

func TestNilGateLeavesDispatchUnchanged(t *testing.T) {
	invoke := func(gate Gate) (Result, error, int, int) {
		registry := New()
		var authorized, ran int
		gatedTool(t, registry, "edit_file", &authorized, &ran, Succeeded)
		if gate != nil {
			registry.SetGate(gate)
		}
		result, err := registry.Invoke(context.Background(), Scope{Agent: "main"}, model.ToolCall{ID: "c1", Name: "edit_file", Arguments: `{"path":"a.go"}`})
		return result, err, authorized, ran
	}
	plain, plainErr, plainAuth, plainRan := invoke(nil)
	gated, gatedErr, gatedAuth, gatedRan := invoke(&recordingGate{})
	if plainErr != nil || gatedErr != nil {
		t.Fatalf("errors: %v / %v", plainErr, gatedErr)
	}
	if plain.Status != gated.Status || plain.Content != gated.Content || plainAuth != gatedAuth || plainRan != gatedRan {
		t.Fatalf("a passing gate changed dispatch: %+v vs %+v", plain, gated)
	}
}
