package tooling

import (
	"context"
	"encoding/json"
	"fmt"
)

// AppNativeTool manages apps and marketplace.
type AppNativeTool struct {
	Service AppService
}

func (AppNativeTool) ToolName() string { return "app" }

func (AppNativeTool) Schema() ToolSchema {
	return ToolSchema{
		Name:        "app",
		Description: "Manage installed apps and marketplace: list installed, browse catalog, install, update, enable, disable, or uninstall apps.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"enum":        []string{"list", "catalog", "install", "update", "enable", "disable", "uninstall"},
					"description": "Action to perform.",
				},
				"slug": map[string]any{
					"type":        []string{"string", "null"},
					"description": "App slug (required for install/update/enable/disable/uninstall).",
				},
			},
			"required":             []string{"action"},
			"additionalProperties": false,
		},
	}
}

func (t AppNativeTool) Run(_ context.Context, argsJSON string) (string, error) {
	var args struct {
		Action string `json:"action"`
		Slug   string `json:"slug"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	switch args.Action {
	case "list":
		apps := t.Service.List()
		if len(apps) == 0 {
			return "No apps installed.", nil
		}
		data, _ := json.MarshalIndent(apps, "", "  ")
		return string(data), nil

	case "catalog":
		catalog, err := t.Service.Catalog()
		if err != nil {
			return "", fmt.Errorf("failed to fetch catalog: %w", err)
		}
		if len(catalog) == 0 {
			return "Marketplace catalog is empty.", nil
		}
		data, _ := json.MarshalIndent(catalog, "", "  ")
		return string(data), nil

	case "install":
		if args.Slug == "" {
			return "", fmt.Errorf("slug is required for install")
		}
		if err := t.Service.Install(args.Slug); err != nil {
			return "", err
		}
		return fmt.Sprintf("App %q installed.", args.Slug), nil

	case "update":
		if args.Slug == "" {
			return "", fmt.Errorf("slug is required for update")
		}
		if err := t.Service.Update(args.Slug); err != nil {
			return "", err
		}
		return fmt.Sprintf("App %q updated.", args.Slug), nil

	case "enable":
		if args.Slug == "" {
			return "", fmt.Errorf("slug is required for enable")
		}
		if err := t.Service.Enable(args.Slug); err != nil {
			return "", err
		}
		return fmt.Sprintf("App %q enabled.", args.Slug), nil

	case "disable":
		if args.Slug == "" {
			return "", fmt.Errorf("slug is required for disable")
		}
		if err := t.Service.Disable(args.Slug); err != nil {
			return "", err
		}
		return fmt.Sprintf("App %q disabled.", args.Slug), nil

	case "uninstall":
		if args.Slug == "" {
			return "", fmt.Errorf("slug is required for uninstall")
		}
		if err := t.Service.Uninstall(args.Slug); err != nil {
			return "", err
		}
		return fmt.Sprintf("App %q uninstalled.", args.Slug), nil

	default:
		return "", fmt.Errorf("unknown action: %s (valid: list, catalog, install, update, enable, disable, uninstall)", args.Action)
	}
}
