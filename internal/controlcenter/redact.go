package controlcenter

import "encoding/json"

const redactedValue = "***"

// sensitiveKeys lists config keys that must be redacted in API responses.
var sensitiveKeys = map[string]bool{
	"telegram_bot_token": true,
	"telegram_chat_id":   true,
	"cc_auth_token":      true,
}

// RedactJSON replaces sensitive values with "***" in a JSON map.
func RedactJSON(data []byte) ([]byte, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	for key := range raw {
		if sensitiveKeys[key] {
			raw[key], _ = json.Marshal(redactedValue)
		}
	}

	return json.MarshalIndent(raw, "", "  ")
}
