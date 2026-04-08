package tooling

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// AvatarService manages the LLM's profile avatar.
type AvatarService interface {
	SetFromBytes(imgBytes []byte) error
	Reset()
	HasCustomAvatar() bool
}

// AvatarNativeTool lets the LLM change its own profile avatar.
type AvatarNativeTool struct {
	Service AvatarService
}

func (AvatarNativeTool) ToolName() string { return "avatar" }

func (AvatarNativeTool) Schema() ToolSchema {
	return ToolSchema{
		Name:        "avatar",
		Description: "Change your profile avatar image. Accepts base64-encoded PNG, JPEG, or WebP (max 256KB). The image is sanitized and resized to 128x128.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"enum":        []string{"set", "reset", "status"},
					"description": "set: upload a new avatar (requires image field), reset: remove custom avatar, status: check if custom avatar is set.",
				},
				"image": map[string]any{
					"type":        "string",
					"description": "Base64-encoded image data (PNG, JPEG, or WebP). Required for 'set' action.",
				},
			},
			"required":             []string{"action"},
			"additionalProperties": false,
		},
	}
}

func (t AvatarNativeTool) Run(_ context.Context, argsJSON string) (string, error) {
	var args struct {
		Action string `json:"action"`
		Image  string `json:"image"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	switch args.Action {
	case "set":
		if args.Image == "" {
			return "", fmt.Errorf("image field required for set action")
		}
		imgBytes, err := base64.StdEncoding.DecodeString(args.Image)
		if err != nil {
			return "", fmt.Errorf("invalid base64: %w", err)
		}
		if err := t.Service.SetFromBytes(imgBytes); err != nil {
			return "", err
		}
		return "Avatar updated successfully.", nil

	case "reset":
		t.Service.Reset()
		return "Avatar reset to default.", nil

	case "status":
		if t.Service.HasCustomAvatar() {
			return "Custom avatar is set.", nil
		}
		return "Using default avatar.", nil

	default:
		return "", fmt.Errorf("unknown action: %s (valid: set, reset, status)", args.Action)
	}
}
