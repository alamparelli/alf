package telegram

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
)

// Client sends messages via the Telegram Bot API.
type Client struct {
	Token string
	HTTP  *http.Client
}

// NewClient creates a Telegram client with the given bot token.
func NewClient(token string) *Client {
	return &Client{
		Token: token,
		HTTP:  &http.Client{},
	}
}

// SendMessage converts markdown to HTML and sends with fallback.
// This is the main entry point for Claude responses.
func (c *Client) SendMessage(chatID int64, text string) error {
	html := MarkdownToHTML(text)

	if valid, _ := ValidateHTML(html); !valid {
		// Fallback to plain text.
		return c.sendChunks(chatID, []string{StripHTML(text)}, "")
	}

	chunks := ChunkHTML(html, 4000)
	return c.sendChunks(chatID, chunks, "HTML")
}

// SendHTML sends pre-formatted HTML (for bot-crafted messages).
func (c *Client) SendHTML(chatID int64, html string) error {
	chunks := ChunkHTML(html, 4000)
	return c.sendChunks(chatID, chunks, "HTML")
}

// SendKeyboard sends a message with an inline keyboard.
func (c *Client) SendKeyboard(chatID int64, text string, keyboard any) error {
	payload, _ := json.Marshal(map[string]any{
		"chat_id":      chatID,
		"text":         text,
		"reply_markup": keyboard,
	})
	return c.post("sendMessage", payload)
}

// sendChunks sends each chunk, falling back to plain text per-chunk on error.
func (c *Client) sendChunks(chatID int64, chunks []string, parseMode string) error {
	var lastErr error
	for _, chunk := range chunks {
		msg := map[string]any{
			"chat_id": chatID,
			"text":    chunk,
		}
		if parseMode != "" {
			msg["parse_mode"] = parseMode
		}

		payload, _ := json.Marshal(msg)
		if err := c.post("sendMessage", payload); err != nil {
			if parseMode != "" {
				// Retry this chunk as plain text.
				log.Printf("HTML send failed, retrying as plain text: %v", err)
				plain := StripHTML(chunk)
				fallback, _ := json.Marshal(map[string]any{
					"chat_id": chatID,
					"text":    plain,
				})
				if err2 := c.post("sendMessage", fallback); err2 != nil {
					lastErr = err2
				}
			} else {
				lastErr = err
			}
		}
	}
	return lastErr
}

// post makes a POST request to the Telegram Bot API.
func (c *Client) post(method string, payload []byte) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/%s", c.Token, method)
	resp, err := c.HTTP.Post(url, "application/json", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("telegram %s: %w", method, err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("telegram %s: invalid response", method)
	}
	if !result.OK {
		return fmt.Errorf("telegram %s: %s", method, result.Description)
	}

	log.Printf("→ message sent (chat via %s)", method)
	return nil
}
