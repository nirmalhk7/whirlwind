package memorybridge

import (
	"context"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"os"
)

func Run(ctx context.Context) error {
	address, token := os.Getenv("WHIRLWIND_MEMORY_URL"), os.Getenv("WHIRLWIND_MEMORY_TOKEN")
	return Server(func(ctx context.Context, query string) (string, error) { return Search(ctx, address, token, query) }).Run(ctx, &mcp.StdioTransport{})
}

func Server(search Searcher) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "whirlwind-memory", Version: "1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "search_lessons", Description: "Search this project's lessons for your current role when a new problem appears. Two searches per operation; compact results only.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, func(ctx context.Context, _ *mcp.CallToolRequest, input Query) (*mcp.CallToolResult, any, error) {
		result, err := search(ctx, input.Query)
		if err != nil {
			return nil, nil, err
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: result}}}, nil, nil
	})
	return server
}
