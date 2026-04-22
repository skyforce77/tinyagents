package tool

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/skyforce77/tinyagents/pkg/llm"
)

// TestFuncImplementsTool constructs a Func{}, asserts Name/Description/Schema/Invoke round-trip a small call.
func TestFuncImplementsTool(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"message":{"type":"string"}}}`)
	f := Func{
		ToolName:   "echo",
		ToolDesc:   "echoes back the message",
		ToolSchema: schema,
		Fn: func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
			return args, nil
		},
	}

	if f.Name() != "echo" {
		t.Fatalf("expected Name() = 'echo', got %q", f.Name())
	}

	if f.Description() != "echoes back the message" {
		t.Fatalf("expected Description() = 'echoes back the message', got %q", f.Description())
	}

	if !json.Valid(f.Schema()) {
		t.Fatal("Schema() did not return valid JSON")
	}

	ctx := context.Background()
	input := json.RawMessage(`{"message":"hello"}`)
	result, err := f.Invoke(ctx, input)
	if err != nil {
		t.Fatalf("Invoke failed: %v", err)
	}

	if string(result) != string(input) {
		t.Fatalf("expected result %s, got %s", string(input), string(result))
	}
}

// TestRegistryRegisterGetList registers two tools, Gets each, Lists returns both, Get of an unknown name returns ok=false.
func TestRegistryRegisterGetList(t *testing.T) {
	reg := NewRegistry()

	tool1 := Func{
		ToolName:   "tool1",
		ToolDesc:   "description 1",
		ToolSchema: json.RawMessage(`{}`),
		Fn: func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{"result":"tool1"}`), nil
		},
	}

	tool2 := Func{
		ToolName:   "tool2",
		ToolDesc:   "description 2",
		ToolSchema: json.RawMessage(`{}`),
		Fn: func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{"result":"tool2"}`), nil
		},
	}

	reg.Register(tool1)
	reg.Register(tool2)

	// Get tool1
	got1, ok := reg.Get("tool1")
	if !ok {
		t.Fatal("Get('tool1') returned ok=false")
	}
	if got1.Name() != "tool1" {
		t.Fatalf("expected Name() = 'tool1', got %q", got1.Name())
	}

	// Get tool2
	got2, ok := reg.Get("tool2")
	if !ok {
		t.Fatal("Get('tool2') returned ok=false")
	}
	if got2.Name() != "tool2" {
		t.Fatalf("expected Name() = 'tool2', got %q", got2.Name())
	}

	// Get unknown tool
	_, ok = reg.Get("unknown")
	if ok {
		t.Fatal("Get('unknown') returned ok=true, expected false")
	}

	// List should return both tools
	list := reg.List()
	if len(list) != 2 {
		t.Fatalf("List() returned %d tools, expected 2", len(list))
	}

	names := make(map[string]bool)
	for _, tool := range list {
		names[tool.Name()] = true
	}

	if !names["tool1"] || !names["tool2"] {
		t.Fatalf("List() did not contain both tools: %v", names)
	}
}

// TestRegistryInvoke invokes a registered tool and checks the result; Invoke of an unknown name returns ErrUnknownTool.
func TestRegistryInvoke(t *testing.T) {
	reg := NewRegistry()

	testTool := Func{
		ToolName:   "addone",
		ToolDesc:   "adds one to the input",
		ToolSchema: json.RawMessage(`{}`),
		Fn: func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
			// Parse input, add one, return result
			var input map[string]interface{}
			if err := json.Unmarshal(args, &input); err != nil {
				return nil, err
			}

			output := map[string]interface{}{"result": 42}
			return json.Marshal(output)
		},
	}

	reg.Register(testTool)

	ctx := context.Background()

	// Invoke a registered tool
	result, err := reg.Invoke(ctx, "addone", json.RawMessage(`{"value":41}`))
	if err != nil {
		t.Fatalf("Invoke('addone') failed: %v", err)
	}

	var resultData map[string]interface{}
	if err := json.Unmarshal(result, &resultData); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}

	if resultData["result"] != float64(42) {
		t.Fatalf("expected result 42, got %v", resultData["result"])
	}

	// Invoke an unknown tool
	_, err = reg.Invoke(ctx, "unknown", json.RawMessage(`{}`))
	if err != ErrUnknownTool {
		t.Fatalf("expected ErrUnknownTool, got %v", err)
	}
}

// TestSpecConversion builds a Tool via Func, calls Spec, asserts the returned llm.ToolSpec has the right fields.
func TestSpecConversion(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}}}`)

	tool := Func{
		ToolName:   "mytool",
		ToolDesc:   "my tool description",
		ToolSchema: schema,
		Fn: func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
			return nil, nil
		},
	}

	spec := Spec(tool)

	if spec.Name != "mytool" {
		t.Fatalf("expected Name = 'mytool', got %q", spec.Name)
	}

	if spec.Description != "my tool description" {
		t.Fatalf("expected Description = 'my tool description', got %q", spec.Description)
	}

	if string(spec.Schema) != string(schema) {
		t.Fatalf("expected Schema = %s, got %s", string(schema), string(spec.Schema))
	}

	// Verify it's the correct type
	if _, ok := interface{}(spec).(llm.ToolSpec); !ok {
		t.Fatal("spec is not an llm.ToolSpec")
	}
}

// TestSpecsConversion tests the Specs helper function.
func TestSpecsConversion(t *testing.T) {
	schema1 := json.RawMessage(`{"type":"object"}`)
	schema2 := json.RawMessage(`{"type":"array"}`)

	tool1 := Func{
		ToolName:   "tool1",
		ToolDesc:   "description 1",
		ToolSchema: schema1,
		Fn:         func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) { return nil, nil },
	}

	tool2 := Func{
		ToolName:   "tool2",
		ToolDesc:   "description 2",
		ToolSchema: schema2,
		Fn:         func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) { return nil, nil },
	}

	tools := []Tool{tool1, tool2}
	specs := Specs(tools)

	if len(specs) != 2 {
		t.Fatalf("expected 2 specs, got %d", len(specs))
	}

	if specs[0].Name != "tool1" || specs[1].Name != "tool2" {
		t.Fatalf("spec names don't match: %q, %q", specs[0].Name, specs[1].Name)
	}

	if string(specs[0].Schema) != string(schema1) || string(specs[1].Schema) != string(schema2) {
		t.Fatal("spec schemas don't match")
	}
}
