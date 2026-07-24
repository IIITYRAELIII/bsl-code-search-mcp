package main

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func registerTools(server *mcp.Server, service *SearchService) {
	mcp.AddTool(
		server,
		&mcp.Tool{
			Name: "search_code",
			Description: "Search indexed 1C/BSL source code with Zoekt query syntax. " +
				"Use repo:^NAME$ to select a configuration, file: to filter paths, " +
				"case:yes for exact case, and regex: for regular expressions. " +
				"Start with broad identifier terms before exact punctuation or regex. " +
				"A zero result proves only that this lexical query found no match; broaden or rephrase it before inferring absence. " +
				"This tool is read-only and searches only user-indexed local dumps.",
			Annotations: &mcp.ToolAnnotations{
				ReadOnlyHint:   true,
				IdempotentHint: true,
			},
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, input SearchRequest) (*mcp.CallToolResult, *SearchResponse, error) {
			output, err := service.Search(ctx, input)
			return nil, output, err
		},
	)
	mcp.AddTool(
		server,
		&mcp.Tool{
			Name: "list_corpora",
			Description: "List the local configuration dumps currently attached to this read-only code-search MCP, " +
				"including document and index statistics. Paths and source contents are not returned.",
			Annotations: &mcp.ToolAnnotations{
				ReadOnlyHint:   true,
				IdempotentHint: true,
			},
		},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, *CorpusListResponse, error) {
			output, err := service.ListCorpora(ctx)
			return nil, output, err
		},
	)
}
