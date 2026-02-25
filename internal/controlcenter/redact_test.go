package controlcenter

import (
	"encoding/json"
	"testing"
)

func TestRedactJSON(t *testing.T) {
	input := `{"log_level":"info","telegram_bot_token":"secret123","model":"sonnet"}`
	redacted, err := RedactJSON([]byte(input))
	if err != nil {
		t.Fatalf("RedactJSON error: %v", err)
	}

	var result map[string]string
	json.Unmarshal(redacted, &result)

	if result["telegram_bot_token"] != "***" {
		t.Errorf("expected telegram_bot_token to be '***', got %q", result["telegram_bot_token"])
	}
	if result["log_level"] != "info" {
		t.Errorf("expected log_level to be 'info', got %q", result["log_level"])
	}
	if result["model"] != "sonnet" {
		t.Errorf("expected model to stay 'sonnet', got %q", result["model"])
	}
}

func TestRedactJSON_AllSensitive(t *testing.T) {
	input := `{"telegram_bot_token":"tok","telegram_chat_id":"123","cc_auth_token":"abc"}`
	redacted, err := RedactJSON([]byte(input))
	if err != nil {
		t.Fatalf("RedactJSON error: %v", err)
	}

	var result map[string]string
	json.Unmarshal(redacted, &result)

	for _, key := range []string{"telegram_bot_token", "telegram_chat_id", "cc_auth_token"} {
		if result[key] != "***" {
			t.Errorf("expected %s to be '***', got %q", key, result[key])
		}
	}
}

func TestRestoreRedacted(t *testing.T) {
	ondisk := `{"log_level":"info","telegram_bot_token":"real_secret"}`
	incoming := `{"log_level":"debug","telegram_bot_token":"***"}`

	restored, err := RestoreRedacted([]byte(incoming), []byte(ondisk))
	if err != nil {
		t.Fatalf("RestoreRedacted error: %v", err)
	}

	var result map[string]string
	json.Unmarshal(restored, &result)

	if result["log_level"] != "debug" {
		t.Errorf("expected log_level 'debug', got %q", result["log_level"])
	}
	if result["telegram_bot_token"] != "real_secret" {
		t.Errorf("expected token restored to 'real_secret', got %q", result["telegram_bot_token"])
	}
}

func TestRestoreRedacted_NonSentinel(t *testing.T) {
	ondisk := `{"telegram_bot_token":"old_secret"}`
	incoming := `{"telegram_bot_token":"new_secret"}`

	restored, err := RestoreRedacted([]byte(incoming), []byte(ondisk))
	if err != nil {
		t.Fatalf("RestoreRedacted error: %v", err)
	}

	var result map[string]string
	json.Unmarshal(restored, &result)

	if result["telegram_bot_token"] != "new_secret" {
		t.Errorf("non-sentinel value should not be restored, got %q", result["telegram_bot_token"])
	}
}
