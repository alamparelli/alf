package media

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// TelegramFile represents a file response from Telegram
type TelegramFile struct {
	Ok     bool        `json:"ok"`
	Result FileResult `json:"result"`
}

type FileResult struct {
	FilePath string `json:"file_path"`
	FileSize int    `json:"file_size"`
}

// DownloadFile downloads a file from Telegram and returns its content
func DownloadFile(client *http.Client, botToken, fileID string) ([]byte, error) {
	// First, get the file path
	getFileURL := fmt.Sprintf("https://api.telegram.org/bot%s/getFile?file_id=%s", botToken, fileID)
	resp, err := client.Get(getFileURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var tfResp TelegramFile
	if err := json.NewDecoder(resp.Body).Decode(&tfResp); err != nil {
		return nil, err
	}

	if !tfResp.Ok {
		return nil, fmt.Errorf("telegram getFile failed")
	}

	// Download the actual file
	downloadURL := fmt.Sprintf("https://api.telegram.org/file/bot%s/%s", botToken, tfResp.Result.FilePath)
	resp, err = client.Get(downloadURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return io.ReadAll(resp.Body)
}

// VisionBlock represents a content block for Claude's vision API
type VisionBlock struct {
	Type      string            `json:"type"`
	Text      string            `json:"text,omitempty"`
	Source    *VisionSource     `json:"source,omitempty"`
}

type VisionSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

// BuildImageVisionBlock creates an image vision block from downloaded data
func BuildImageVisionBlock(data []byte, filename string, mimeType string) VisionBlock {
	b64 := base64.StdEncoding.EncodeToString(data)
	return VisionBlock{
		Type: "image",
		Source: &VisionSource{
			Type:      "base64",
			MediaType: mimeType,
			Data:      b64,
		},
	}
}

// BuildDocumentVisionBlock creates a document vision block from downloaded data
func BuildDocumentVisionBlock(data []byte, filename string, mimeType string) VisionBlock {
	b64 := base64.StdEncoding.EncodeToString(data)
	return VisionBlock{
		Type: "document",
		Source: &VisionSource{
			Type:      "base64",
			MediaType: mimeType,
			Data:      b64,
		},
	}
}

// ExtractTextFromDocument extracts text content from text files.
// For PDFs, uses pdftotext (poppler-utils). For plain text, returns as-is.
func ExtractTextFromDocument(data []byte, mimeType string) string {
	if IsTextContent(mimeType) {
		if utf8Valid(data) {
			return string(data)
		}
		return makeValidUTF8(data)
	}

	if mimeType == "application/pdf" {
		return extractPDFText(data)
	}

	return fmt.Sprintf("[Binary document: %s (%d bytes)]", mimeType, len(data))
}

// extractPDFText shells out to pdftotext (poppler-utils) to extract text from PDF data.
func extractPDFText(data []byte) string {
	tmpFile, err := os.CreateTemp("", "alf-pdf-*.pdf")
	if err != nil {
		return "[PDF: failed to create temp file]"
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Write(data)
	tmpFile.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "pdftotext", "-layout", tmpFile.Name(), "-")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Sprintf("[PDF: text extraction failed: %v]", err)
	}

	text := strings.TrimSpace(stdout.String())
	if text == "" {
		return "[PDF: no extractable text (scanned/image-based document)]"
	}

	// Cap at 100KB to avoid prompt bloat.
	const maxPDFText = 100 * 1024
	if len(text) > maxPDFText {
		text = text[:maxPDFText] + "\n\n[... truncated, PDF text exceeds 100KB ...]"
	}

	return text
}

// utf8Valid checks if data is valid UTF-8
func utf8Valid(data []byte) bool {
	for i := 0; i < len(data); {
		r := rune(data[i])
		if r < 0x80 {
			i++
			continue
		}

		// Multi-byte rune
		var size int
		if r&0xE0 == 0xC0 {
			size = 2
		} else if r&0xF0 == 0xE0 {
			size = 3
		} else if r&0xF8 == 0xF0 {
			size = 4
		} else {
			return false
		}

		if i+size > len(data) {
			return false
		}
		i += size
	}
	return true
}

// makeValidUTF8 replaces invalid UTF-8 sequences with replacement character
func makeValidUTF8(data []byte) string {
	var buf bytes.Buffer
	for i := 0; i < len(data); {
		r := rune(data[i])
		if r < 0x80 {
			buf.WriteByte(byte(r))
			i++
			continue
		}

		// Multi-byte rune
		var size int
		if r&0xE0 == 0xC0 {
			size = 2
		} else if r&0xF0 == 0xE0 {
			size = 3
		} else if r&0xF8 == 0xF0 {
			size = 4
		} else {
			buf.WriteRune(rune('\ufffd')) // replacement character
			i++
			continue
		}

		if i+size > len(data) {
			buf.WriteRune(rune('\ufffd'))
			break
		}

		// Valid multibyte sequence
		buf.Write(data[i : i+size])
		i += size
	}
	return buf.String()
}
