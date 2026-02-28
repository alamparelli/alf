package media

import (
	"bytes"
	"path/filepath"
	"strings"
)

// DetectMimeType detects MIME type from magic bytes, with fallback to extension.
func DetectMimeType(data []byte, filename string) string {
	// Check magic bytes
	if len(data) >= 4 {
		// JPEG
		if len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF {
			return "image/jpeg"
		}
		// PNG
		if len(data) >= 8 && bytes.Equal(data[:8], []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}) {
			return "image/png"
		}
		// GIF
		if len(data) >= 6 && bytes.Equal(data[:3], []byte("GIF")) {
			if data[3] == '8' && (data[4] == '7' || data[4] == '9') && data[5] == 'a' {
				return "image/gif"
			}
		}
		// WebP
		if bytes.Equal(data[:4], []byte("RIFF")) && len(data) >= 12 && bytes.Equal(data[8:12], []byte("WEBP")) {
			return "image/webp"
		}
		// PDF
		if len(data) >= 4 && bytes.Equal(data[:4], []byte("%PDF")) {
			return "application/pdf"
		}
		// DOCX (ZIP file)
		if len(data) >= 2 && bytes.Equal(data[:2], []byte("PK")) {
			if isDocxFile(data) {
				return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
			}
		}
	}

	// Fallback to extension-based detection
	ext := strings.ToLower(filepath.Ext(filename))
	mimeMap := map[string]string{
		".jpg":  "image/jpeg",
		".jpeg": "image/jpeg",
		".png":  "image/png",
		".gif":  "image/gif",
		".webp": "image/webp",
		".pdf":  "application/pdf",
		".txt":  "text/plain",
		".md":   "text/markdown",
		".json": "application/json",
		".csv":  "text/csv",
		".xml":  "application/xml",
		".html": "text/html",
		".py":   "text/x-python",
		".js":   "text/javascript",
		".go":   "text/x-go",
		".sh":   "text/x-shellscript",
		".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		".doc":  "application/msword",
		".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		".xls":  "application/vnd.ms-excel",
	}

	if mime, ok := mimeMap[ext]; ok {
		return mime
	}

	// Default to octet-stream
	return "application/octet-stream"
}

// isDocxFile checks if a ZIP is likely a DOCX file by looking for content.xml
func isDocxFile(data []byte) bool {
	// This is a simplistic check. For a full implementation,
	// we'd need to parse the ZIP and check for [Content_Types].xml
	// For now, we just return true if it's a ZIP
	return true
}

// IsTextContent checks if MIME type is text-based and can be injected into prompts
func IsTextContent(mimeType string) bool {
	return strings.HasPrefix(mimeType, "text/") ||
		mimeType == "application/json" ||
		mimeType == "application/xml" ||
		mimeType == "application/x-shellscript"
}

// IsImageContent checks if MIME type is an image
func IsImageContent(mimeType string) bool {
	return strings.HasPrefix(mimeType, "image/")
}

// IsPDFContent checks if MIME type is PDF
func IsPDFContent(mimeType string) bool {
	return mimeType == "application/pdf"
}

// IsVoiceContent checks if MIME type is audio
func IsVoiceContent(mimeType string) bool {
	return strings.HasPrefix(mimeType, "audio/") ||
		mimeType == "audio/ogg"
}
