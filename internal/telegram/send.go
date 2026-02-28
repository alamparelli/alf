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

// SendMessageReturnID sends a message (with markdown→HTML conversion) and returns the last message_id.
func (c *Client) SendMessageReturnID(chatID int64, text string) (int64, error) {
	html := MarkdownToHTML(text)

	if valid, _ := ValidateHTML(html); !valid {
		return c.sendChunksTrack(chatID, []string{StripHTML(text)}, "")
	}

	chunks := ChunkHTML(html, 4000)
	return c.sendChunksTrack(chatID, chunks, "HTML")
}

// sendChunks sends each chunk, falling back to plain text per-chunk on error.
func (c *Client) sendChunks(chatID int64, chunks []string, parseMode string) error {
	_, err := c.sendChunksTrack(chatID, chunks, parseMode)
	return err
}

// sendChunksTrack sends chunks and returns the last message_id.
func (c *Client) sendChunksTrack(chatID int64, chunks []string, parseMode string) (int64, error) {
	var lastErr error
	var lastMsgID int64
	for _, chunk := range chunks {
		msg := map[string]any{
			"chat_id": chatID,
			"text":    chunk,
		}
		if parseMode != "" {
			msg["parse_mode"] = parseMode
		}

		payload, _ := json.Marshal(msg)
		body, err := c.postRaw("sendMessage", payload)
		if err != nil {
			if parseMode != "" {
				log.Printf("HTML send failed, retrying as plain text: %v", err)
				plain := StripHTML(chunk)
				fallback, _ := json.Marshal(map[string]any{
					"chat_id": chatID,
					"text":    plain,
				})
				body2, err2 := c.postRaw("sendMessage", fallback)
				if err2 != nil {
					lastErr = err2
				} else {
					lastMsgID = extractMessageID(body2)
				}
			} else {
				lastErr = err
			}
		} else {
			lastMsgID = extractMessageID(body)
		}
	}
	return lastMsgID, lastErr
}

func extractMessageID(body []byte) int64 {
	var resp struct {
		Result struct {
			MessageID int64 `json:"message_id"`
		} `json:"result"`
	}
	json.Unmarshal(body, &resp)
	return resp.Result.MessageID
}

// SendChatAction sends a typing indicator or other action.
func (c *Client) SendChatAction(chatID int64, action string) error {
	payload, _ := json.Marshal(map[string]any{
		"chat_id": chatID,
		"action":  action,
	})
	return c.post("sendChatAction", payload)
}

// SendMessageGetID sends a silent plain text message and returns the message_id.
func (c *Client) SendMessageGetID(chatID int64, text string) (int64, error) {
	payload, _ := json.Marshal(map[string]any{
		"chat_id":              chatID,
		"text":                 text,
		"disable_notification": true,
	})
	body, err := c.postRaw("sendMessage", payload)
	if err != nil {
		return 0, err
	}
	var resp struct {
		Result struct {
			MessageID int64 `json:"message_id"`
		} `json:"result"`
	}
	json.Unmarshal(body, &resp)
	return resp.Result.MessageID, nil
}

// EditMessage edits an existing message's text.
func (c *Client) EditMessage(chatID, messageID int64, text string) error {
	payload, _ := json.Marshal(map[string]any{
		"chat_id":    chatID,
		"message_id": messageID,
		"text":       text,
	})
	return c.post("editMessageText", payload)
}

// DeleteMessage deletes a message.
func (c *Client) DeleteMessage(chatID, messageID int64) error {
	payload, _ := json.Marshal(map[string]any{
		"chat_id":    chatID,
		"message_id": messageID,
	})
	return c.post("deleteMessage", payload)
}

// SetMessageReaction sets an emoji reaction on a message.
func (c *Client) SetMessageReaction(chatID, messageID int64, emoji string) error {
	payload, _ := json.Marshal(map[string]any{
		"chat_id":    chatID,
		"message_id": messageID,
		"reaction": []map[string]string{
			{"type": "emoji", "emoji": emoji},
		},
	})
	return c.post("setMessageReaction", payload)
}

// postRaw makes a POST request and returns the raw response body.
func (c *Client) postRaw(method string, payload []byte) ([]byte, error) {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/%s", c.Token, method)
	resp, err := c.HTTP.Post(url, "application/json", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("telegram %s: %w", method, err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("telegram %s: invalid response", method)
	}
	if !result.OK {
		return nil, fmt.Errorf("telegram %s: %s", method, result.Description)
	}

	return body, nil
}

// post makes a POST request to the Telegram Bot API.
func (c *Client) post(method string, payload []byte) error {
	_, err := c.postRaw(method, payload)
	return err
}
