package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

func main() {
	token := readSecret("TELEGRAM_BOT_TOKEN")
	chatID := readSecret("TELEGRAM_CHAT_ID")

	if token == "" || chatID == "" {
		log.Fatal("TELEGRAM_BOT_TOKEN and TELEGRAM_CHAT_ID are required")
	}

	// Verify claude CLI is available
	if _, err := exec.LookPath("claude"); err != nil {
		log.Fatal("claude CLI not found in PATH")
	}

	log.Println("alf-daemon starting...")

	var offset int64
	client := &http.Client{Timeout: 35 * time.Second}

	for {
		updates, err := getUpdates(client, token, offset)
		if err != nil {
			log.Printf("getUpdates error: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}

		for _, u := range updates {
			offset = u.UpdateID + 1

			if u.Message == nil || u.Message.Text == "" {
				continue
			}

			log.Printf("← %s: %s", u.Message.From.Username, u.Message.Text)

			reply, err := askClaude(u.Message.Text)
			if err != nil {
				log.Printf("claude error: %v", err)
				reply = fmt.Sprintf("Error: %v", err)
			}

			sendMessage(client, token, u.Message.Chat.ID, reply)
		}
	}
}

func askClaude(prompt string) (string, error) {
	cmd := exec.Command("claude",
		"-p", prompt,
		"--model", "sonnet",
		"--output-format", "text",
		"--dangerously-skip-permissions",
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%v: %s", err, stderr.String())
	}

	return strings.TrimSpace(stdout.String()), nil
}

func readSecret(envVar string) string {
	if path := os.Getenv(envVar + "_FILE"); path != "" {
		data, err := os.ReadFile(path)
		if err == nil {
			return strings.TrimSpace(string(data))
		}
	}
	return strings.TrimSpace(os.Getenv(envVar))
}

type Update struct {
	UpdateID int64    `json:"update_id"`
	Message  *Message `json:"message"`
}

type Message struct {
	Chat Chat   `json:"chat"`
	From User   `json:"from"`
	Text string `json:"text"`
}

type Chat struct {
	ID int64 `json:"id"`
}

type User struct {
	Username string `json:"username"`
}

func getUpdates(client *http.Client, token string, offset int64) ([]Update, error) {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?offset=%d&timeout=30", token, offset)
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result struct {
		OK     bool     `json:"ok"`
		Result []Update `json:"result"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	if !result.OK {
		return nil, fmt.Errorf("telegram API error: %s", string(body))
	}
	return result.Result, nil
}

func sendMessage(client *http.Client, token string, chatID int64, text string) {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token)
	payload, _ := json.Marshal(map[string]any{
		"chat_id": chatID,
		"text":    text,
	})
	resp, err := http.Post(url, "application/json", bytes.NewReader(payload))
	if err != nil {
		log.Printf("sendMessage error: %v", err)
		return
	}
	defer resp.Body.Close()
	log.Printf("→ reply sent (chat %d)", chatID)
}
