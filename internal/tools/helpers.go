package tools

import (
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func toolOK[T any](payload T) *mcp.CallToolResultFor[T] {
	return &mcp.CallToolResultFor[T]{
		Content: []mcp.Content{
			&mcp.TextContent{Text: "ok"},
		},
		StructuredContent: payload,
	}
}

func toolErr[T any](err error) (*mcp.CallToolResultFor[T], error) {
	return nil, fmt.Errorf("tool error: %w", err)
}
