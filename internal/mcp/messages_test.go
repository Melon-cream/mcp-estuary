package mcp

import (
	"encoding/json"
	"testing"
)

func TestCallToolParamsMarshalIncludesEmptyArguments(t *testing.T) {
	payload, err := json.Marshal(CallToolParams{
		Name:      "memory__read_graph",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("marshal call tool params: %v", err)
	}

	if string(payload) != `{"name":"memory__read_graph","arguments":{}}` {
		t.Fatalf("unexpected payload: %s", payload)
	}
}
