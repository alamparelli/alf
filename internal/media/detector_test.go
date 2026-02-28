package media

import (
	"testing"
)

func TestDetectMimeType(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		filename string
		want     string
	}{
		{
			name:     "JPEG magic bytes",
			data:     []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10},
			filename: "image.bin",
			want:     "image/jpeg",
		},
		{
			name:     "PNG magic bytes",
			data:     []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D},
			filename: "image.bin",
			want:     "image/png",
		},
		{
			name:     "PDF magic bytes",
			data:     []byte("%PDF-1.4\n"),
			filename: "doc.bin",
			want:     "application/pdf",
		},
		{
			name:     "GIF magic bytes",
			data:     []byte("GIF89a"),
			filename: "image.bin",
			want:     "image/gif",
		},
		{
			name:     "extension fallback .jpg",
			data:     []byte("not-actually-jpeg"),
			filename: "image.jpg",
			want:     "image/jpeg",
		},
		{
			name:     "extension fallback .txt",
			data:     []byte("hello world"),
			filename: "file.txt",
			want:     "text/plain",
		},
		{
			name:     "unknown extension",
			data:     []byte("random data"),
			filename: "file.xyz",
			want:     "application/octet-stream",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectMimeType(tt.data, tt.filename)
			if got != tt.want {
				t.Errorf("DetectMimeType() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIsTextContent(t *testing.T) {
	tests := []struct {
		mimeType string
		want     bool
	}{
		{"text/plain", true},
		{"text/markdown", true},
		{"application/json", true},
		{"application/xml", true},
		{"image/jpeg", false},
		{"application/pdf", false},
		{"audio/mpeg", false},
	}

	for _, tt := range tests {
		t.Run(tt.mimeType, func(t *testing.T) {
			got := IsTextContent(tt.mimeType)
			if got != tt.want {
				t.Errorf("IsTextContent(%q) = %v, want %v", tt.mimeType, got, tt.want)
			}
		})
	}
}

func TestIsImageContent(t *testing.T) {
	tests := []struct {
		mimeType string
		want     bool
	}{
		{"image/jpeg", true},
		{"image/png", true},
		{"image/gif", true},
		{"text/plain", false},
		{"application/pdf", false},
	}

	for _, tt := range tests {
		t.Run(tt.mimeType, func(t *testing.T) {
			got := IsImageContent(tt.mimeType)
			if got != tt.want {
				t.Errorf("IsImageContent(%q) = %v, want %v", tt.mimeType, got, tt.want)
			}
		})
	}
}
