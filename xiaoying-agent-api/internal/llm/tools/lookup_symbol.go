package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/auramate/auramate/internal/lsp"
	"github.com/auramate/auramate/internal/lsp/protocol"
)

const (
	LookupSymbolToolName    = "lookup_symbol"
	lookupSymbolDescription = `Searches for symbols (functions, classes, variables) in the codebase.
Usage:
- query: The name of the symbol to search for.
- file_path: (Optional) Limit search to a specific file (if supported).
- limit: (Optional) Max results (default: 10).
Use this tool to find where a function or class is defined without grepping everything.
`
)

type LookupSymbolParams struct {
	Query    string `json:"query"`
	FilePath string `json:"file_path"`
	Limit    int    `json:"limit"`
}

type lookupSymbolTool struct {
	lspRegistry *lsp.Registry
}

func NewLookupSymbolTool(lspRegistry *lsp.Registry) BaseTool {
	return &lookupSymbolTool{
		lspRegistry: lspRegistry,
	}
}

func (t *lookupSymbolTool) Info() ToolInfo {
	return ToolInfo{
		Name:        LookupSymbolToolName,
		Description: lookupSymbolDescription,
		Parameters: map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "The name of the symbol to search for",
			},
			"file_path": map[string]any{
				"type":        "string",
				"description": "Limit search to this file (optional)",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Maximum number of results",
			},
		},
		Required: []string{"query"},
	}
}

func (t *lookupSymbolTool) Run(ctx context.Context, call ToolCall) (ToolResponse, error) {
	var params LookupSymbolParams
	if err := json.Unmarshal([]byte(call.Input), &params); err != nil {
		return NewTextErrorResponse("invalid parameters"), nil
	}

	if params.Query == "" {
		return NewTextErrorResponse("query is required"), nil
	}

	if params.Limit <= 0 {
		params.Limit = 10
	}

	var allResults []string
	seen := make(map[string]bool)
	readyClients := readyLSPClientsSnapshot(t.lspRegistry)
	if len(readyClients) == 0 {
		return NewTextErrorResponse(lspUnavailableSummary(t.lspRegistry)), nil
	}

	// Iterate all active LSP clients
	for _, client := range readyClients {
		orRes, err := client.Symbol(ctx, protocol.WorkspaceSymbolParams{
			Query: params.Query,
		})
		if err != nil {
			continue
		}

		res, err := orRes.Results()
		if err != nil {
			continue
		}

		for _, r := range res {
			loc := r.GetLocation()
			uri := string(loc.URI)
			path := strings.TrimPrefix(uri, "file://")

			// Filter by file path if provided
			if params.FilePath != "" {
				// Normalize paths for comparison
				// Simple contains check for now
				if !strings.Contains(path, params.FilePath) {
					continue
				}
			}

			// Get Kind
			var kind protocol.SymbolKind
			switch v := r.(type) {
			case *protocol.SymbolInformation:
				kind = v.Kind
			case *protocol.WorkspaceSymbol:
				kind = v.Kind
			}

			kindStr := protocol.TableKindMap[kind]
			if kindStr == "" {
				kindStr = fmt.Sprintf("Kind(%d)", kind)
			}

			line := loc.Range.Start.Line + 1
			entry := fmt.Sprintf("%s:%d %s (%s)", path, line, r.GetName(), kindStr)

			if !seen[entry] {
				allResults = append(allResults, entry)
				seen[entry] = true
			}
		}
	}

	if len(allResults) == 0 {
		return NewTextResponse("No symbols found matching query: " + params.Query), nil
	}

	// Sort results
	// ... simple string sort
	// We might want to sort by relevance, but simple sort is fine
	// If too many results, truncate
	if len(allResults) > params.Limit {
		allResults = allResults[:params.Limit]
		allResults = append(allResults, fmt.Sprintf("... (%d more results)", len(allResults)-params.Limit))
	}

	return NewTextResponse(strings.Join(allResults, "\n")), nil
}
