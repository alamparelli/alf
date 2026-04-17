package tooling

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// SearchNativeTool provides cross-resource search.
type SearchNativeTool struct {
	Service SearchService
}

func (SearchNativeTool) ToolName() string { return "search" }

func (SearchNativeTool) Schema() ToolSchema {
	return ToolSchema{
		Name:        "search",
		Description: "Search across apps, workspace files, and documentation. Returns matching results grouped by type.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Search query.",
				},
				"types": map[string]any{
					"type":        []string{"string", "null"},
					"description": "Comma-separated result types to include: apps, files, docs (default: all).",
				},
			},
			"required":             []string{"query"},
			"additionalProperties": false,
		},
	}
}

func (t SearchNativeTool) Run(_ context.Context, argsJSON string) (string, error) {
	var args struct {
		Query string `json:"query"`
		Types string `json:"types"`
	}
	if err := parseArgs(argsJSON, &args); err != nil {
		return "", err
	}
	if args.Query == "" {
		return "", fmt.Errorf("query is required")
	}

	var types []string
	if args.Types != "" {
		for _, t := range strings.Split(args.Types, ",") {
			types = append(types, strings.TrimSpace(t))
		}
	}

	results, err := t.Service.Search(args.Query, types)
	if err != nil {
		return "", err
	}
	if len(results) == 0 {
		return fmt.Sprintf("No results for %q.", args.Query), nil
	}
	data, _ := json.MarshalIndent(results, "", "  ")
	return string(data), nil
}
