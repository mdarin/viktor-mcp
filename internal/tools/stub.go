package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var errNotImplemented = fmt.Errorf("not implemented yet: REST handlers land with task #223")

func stub[In any](_ context.Context, _ *mcp.CallToolRequest, _ In) (*mcp.CallToolResult, any, error) {
	return nil, nil, errNotImplemented
}

func add[In any](r *Registry, name, description string, handlers ...mcp.ToolHandlerFor[In, any]) {
	h := stub[In]
	if len(handlers) > 0 {
		h = handlers[0]
	}
	Add(r, name, description, h)
}
