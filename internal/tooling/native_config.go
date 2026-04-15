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

// ConfigNativeTool provides read access to system configuration and avatar management.
type ConfigNativeTool struct {
	Service ConfigService
	Avatar  AvatarService // optional: avatar management
}

func (ConfigNativeTool) ToolName() string { return "config" }

func (ConfigNativeTool) Schema() ToolSchema {
	return ToolSchema{
		Name:        "config",
		Description: "Read system configuration or manage your profile avatar.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"enum":        []string{"get", "avatar-set", "avatar-reset", "avatar-status"},
					"description": "get: read config. avatar-set: upload profile image (requires image field). avatar-reset: remove custom avatar. avatar-status: check if custom avatar is set.",
				},
				"image": map[string]any{
					"type":        "string",
					"description": "Base64-encoded image data (PNG, JPEG, or WebP, max 256KB). Required for avatar-set.",
				},
			},
			"required":             []string{"action"},
			"additionalProperties": false,
		},
	}
}

func (t ConfigNativeTool) Run(_ context.Context, argsJSON string) (string, error) {
	var args struct {
		Action string `json:"action"`
		Image  string `json:"image"`
	}
	if err := parseArgs(argsJSON, &args); err != nil {
		return "", err
	}

	switch args.Action {
	case "get":
		cfg, err := t.Service.Get()
		if err != nil {
			return "", err
		}
		data, _ := json.MarshalIndent(cfg, "", "  ")
		return string(data), nil

	case "avatar-set":
		if t.Avatar == nil {
			return "", fmt.Errorf("avatar management not available")
		}
		if args.Image == "" {
			return "", fmt.Errorf("image field required for avatar-set")
		}
		imgBytes, err := base64.StdEncoding.DecodeString(args.Image)
		if err != nil {
			return "", fmt.Errorf("invalid base64: %w", err)
		}
		if err := t.Avatar.SetFromBytes(imgBytes); err != nil {
			return "", err
		}
		return "Avatar updated successfully.", nil

	case "avatar-reset":
		if t.Avatar == nil {
			return "", fmt.Errorf("avatar management not available")
		}
		t.Avatar.Reset()
		return "Avatar reset to default.", nil

	case "avatar-status":
		if t.Avatar == nil {
			return "", fmt.Errorf("avatar management not available")
		}
		if t.Avatar.HasCustomAvatar() {
			return "Custom avatar is set.", nil
		}
		return "Using default avatar.", nil

	default:
		return "", fmt.Errorf("unknown action: %s (valid: get, avatar-set, avatar-reset, avatar-status)", args.Action)
	}
}
