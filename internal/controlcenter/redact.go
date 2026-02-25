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

// RestoreRedacted takes incoming JSON and restores any "***" values
// from the on-disk original.
func RestoreRedacted(incoming, ondisk []byte) ([]byte, error) {
	var inc map[string]json.RawMessage
	if err := json.Unmarshal(incoming, &inc); err != nil {
		return nil, err
	}

	var orig map[string]json.RawMessage
	if err := json.Unmarshal(ondisk, &orig); err != nil {
		return nil, err
	}

	for key := range inc {
		if sensitiveKeys[key] {
			var val string
			if err := json.Unmarshal(inc[key], &val); err == nil && val == redactedValue {
				if origVal, ok := orig[key]; ok {
					inc[key] = origVal
				}
			}
		}
	}

	return json.MarshalIndent(inc, "", "  ")
}
