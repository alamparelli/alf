package media

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

// ExtractTextFromDocument extracts text content from text files
// For PDFs and DOCX, this is a simple placeholder; real implementation would use libraries
func ExtractTextFromDocument(data []byte, mimeType string) string {
	if IsTextContent(mimeType) {
		// Try to decode as UTF-8, fallback to lossy conversion
		if utf8Valid(data) {
			return string(data)
		}
		// Replace invalid UTF-8 with replacement character
		return makeValidUTF8(data)
	}

	// For binary formats, return a placeholder
	// Real implementation would use libraries like fitz (PDF) or zipexplorer (DOCX)
	return fmt.Sprintf("[Binary document: %s (%d bytes)]", mimeType, len(data))
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
