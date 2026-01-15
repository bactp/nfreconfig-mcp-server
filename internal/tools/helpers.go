package tools

import (
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func toolOK[T any](payload T) *mcp.CallToolResultFor[T] {
	// Marshal payload to JSON for text content so agents can read it
	jsonBytes, err := json.MarshalIndent(payload, "", "  ")
	var textContent string
	if err != nil {
		textContent = fmt.Sprintf("Result: %+v", payload)
	} else {
		textContent = string(jsonBytes)
	}

	return &mcp.CallToolResultFor[T]{
		Content: []mcp.Content{
			&mcp.TextContent{Text: textContent},
		},
		StructuredContent: payload,
	}
}

func toolErr[T any](err error) (*mcp.CallToolResultFor[T], error) {
	return nil, fmt.Errorf("tool error: %w", err)
}
