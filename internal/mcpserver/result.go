// Package mcpserver registers engram's brain_* tools on an MCP server.
package mcpserver

import (
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// jsonResult converts a value into an indented-JSON text result. Handlers
// return (*mcp.CallToolResult, any, error); structured output is deliberately
// unused in favour of one human-readable text block, because the store's
// answers are already shaped for reading.
func jsonResult(v any) (*mcp.CallToolResult, any, error) {
	var text string
	if s, ok := v.(string); ok {
		text = s
	} else {
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return fail(fmt.Sprintf("failed to serialize result: %v", err))
		}
		text = string(b)
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil, nil
}

// fail builds a tool result with isError=true — an actionable message handed
// straight to the model, not a protocol error.
func fail(msg string) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
	}, nil, nil
}

func ptr[T any](v T) *T { return &v }
