package telegram

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Client sends messages via the Telegram Bot API.
type Client struct {
	Token       string
	HTTP        *http.Client
	OnRateLimit func(wait time.Duration) // optional callback when rate-limited
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

// SendHTMLNoPreview sends HTML with link preview disabled.
// Use for messages containing one-time links (e.g., magic login links)
// to prevent Telegram's preview bot from consuming them.
func (c *Client) SendHTMLNoPreview(chatID int64, html string) error {
	payload, _ := json.Marshal(map[string]any{
		"chat_id":                  chatID,
		"text":                     html,
		"parse_mode":               "HTML",
		"disable_web_page_preview": true,
	})
	return c.post("sendMessage", payload)
}

// SendAnimation sends a GIF/animation by URL with an optional caption.
func (c *Client) SendAnimation(chatID int64, animationURL, caption string) error {
	msg := map[string]any{
		"chat_id":   chatID,
		"animation": animationURL,
	}
	if caption != "" {
		msg["caption"] = caption
		msg["parse_mode"] = "HTML"
	}
	payload, _ := json.Marshal(msg)
	return c.post("sendAnimation", payload)
}

// SendVideo sends a video by URL with an optional caption.
func (c *Client) SendVideo(chatID int64, videoURL, caption string) error {
	msg := map[string]any{
		"chat_id": chatID,
		"video":   videoURL,
	}
	if caption != "" {
		msg["caption"] = caption
		msg["parse_mode"] = "HTML"
	}
	payload, _ := json.Marshal(msg)
	return c.post("sendVideo", payload)
}

// SendVideoFile uploads a local video file to Telegram.
func (c *Client) SendVideoFile(chatID int64, filePath, caption string) error {
	return c.sendFileMultipart("sendVideo", "video", chatID, filePath, caption)
}

// SendAnimationFile uploads a local GIF/animation file to Telegram.
func (c *Client) SendAnimationFile(chatID int64, filePath, caption string) error {
	return c.sendFileMultipart("sendAnimation", "animation", chatID, filePath, caption)
}

// SendDocumentFile uploads a local file as a document to Telegram.
func (c *Client) SendDocumentFile(chatID int64, filePath, caption string) error {
	return c.sendFileMultipart("sendDocument", "document", chatID, filePath, caption)
}

// sendFileMultipart uploads a file via multipart/form-data to a Telegram API method.
func (c *Client) sendFileMultipart(method, field string, chatID int64, filePath, caption string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("open %s: %w", filePath, err)
	}
	defer f.Close()

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	w.WriteField("chat_id", fmt.Sprintf("%d", chatID))
	if caption != "" {
		w.WriteField("caption", caption)
		w.WriteField("parse_mode", "HTML")
	}

	part, err := w.CreateFormFile(field, filepath.Base(filePath))
	if err != nil {
		return fmt.Errorf("create form file: %w", err)
	}
	if _, err := io.Copy(part, f); err != nil {
		return fmt.Errorf("copy file: %w", err)
	}
	w.Close()

	url := fmt.Sprintf("https://api.telegram.org/bot%s/%s", c.Token, method)
	resp, err := c.HTTP.Post(url, w.FormDataContentType(), &buf)
	if err != nil {
		return fmt.Errorf("telegram %s: %w", method, err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var result struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if json.Unmarshal(body, &result) == nil && !result.OK {
		return fmt.Errorf("telegram %s: %s", method, result.Description)
	}
	return nil
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
			if parseMode != "" && !isRateLimitError(err) {
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

// maxRetryAfter is the maximum time we'll wait for a Telegram rate limit.
const maxRetryAfter = 120 * time.Second

// postRaw makes a POST request and returns the raw response body.
// Automatically waits and retries once on 429 Too Many Requests.
func (c *Client) postRaw(method string, payload []byte) ([]byte, error) {
	body, err := c.doPost(method, payload)
	if err == nil {
		return body, nil
	}

	// Check if it's a rate limit error - wait and retry once.
	wait := parseRetryAfter(body)
	if wait <= 0 {
		return nil, err
	}
	if wait > maxRetryAfter {
		log.Printf("telegram: rate limited for %v (capped at %v)", wait, maxRetryAfter)
		wait = maxRetryAfter
	} else {
		log.Printf("telegram: rate limited, waiting %v before retry", wait)
	}
	if c.OnRateLimit != nil {
		c.OnRateLimit(wait)
	}
	time.Sleep(wait)
	return c.doPost(method, payload)
}

func (c *Client) doPost(method string, payload []byte) ([]byte, error) {
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
		return body, fmt.Errorf("telegram %s: %s", method, result.Description)
	}

	return body, nil
}

// parseRetryAfter extracts the retry_after seconds from a Telegram 429 response.
func parseRetryAfter(body []byte) time.Duration {
	if len(body) == 0 {
		return 0
	}
	var resp struct {
		ErrorCode  int `json:"error_code"`
		Parameters struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	if json.Unmarshal(body, &resp) != nil || resp.ErrorCode != 429 || resp.Parameters.RetryAfter <= 0 {
		return 0
	}
	return time.Duration(resp.Parameters.RetryAfter) * time.Second
}

// isRateLimitError checks if the error is a Telegram 429 rate limit.
func isRateLimitError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "Too Many Requests")
}

// post makes a POST request to the Telegram Bot API.
func (c *Client) post(method string, payload []byte) error {
	_, err := c.postRaw(method, payload)
	return err
}
