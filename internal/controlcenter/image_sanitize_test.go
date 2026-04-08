package controlcenter

import (
	"bytes"
	"image"
	"image/png"
	"testing"
)

func TestSanitizeImage_ValidPNG(t *testing.T) {
	// Create a minimal valid PNG.
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	var buf bytes.Buffer
	png.Encode(&buf, img)

	out, err := sanitizeImage(buf.Bytes())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify output is valid PNG.
	decoded, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("output is not valid PNG: %v", err)
	}

	// Verify dimensions.
	bounds := decoded.Bounds()
	if bounds.Dx() != avatarSize || bounds.Dy() != avatarSize {
		t.Errorf("expected %dx%d, got %dx%d", avatarSize, avatarSize, bounds.Dx(), bounds.Dy())
	}
}

func TestSanitizeImage_RejectSVG(t *testing.T) {
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)
	_, err := sanitizeImage(svg)
	if err == nil {
		t.Fatal("expected error for SVG input")
	}
}

func TestSanitizeImage_RejectGarbage(t *testing.T) {
	_, err := sanitizeImage([]byte("not an image at all"))
	if err == nil {
		t.Fatal("expected error for garbage input")
	}
}

func TestSanitizeImage_RejectEmpty(t *testing.T) {
	_, err := sanitizeImage([]byte{})
	if err == nil {
		t.Fatal("expected error for empty input")
	}
}

func TestHasAllowedMagic(t *testing.T) {
	tests := []struct {
		name   string
		data   []byte
		expect bool
	}{
		{"PNG", []byte{0x89, 0x50, 0x4E, 0x47, 0x00}, true},
		{"JPEG", []byte{0xFF, 0xD8, 0xFF, 0x00}, true},
		{"WebP", []byte{0x52, 0x49, 0x46, 0x46, 0x00}, true},
		{"GIF", []byte{0x47, 0x49, 0x46, 0x38}, false},
		{"SVG", []byte("<svg"), false},
		{"empty", []byte{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasAllowedMagic(tt.data); got != tt.expect {
				t.Errorf("hasAllowedMagic(%s) = %v, want %v", tt.name, got, tt.expect)
			}
		})
	}
}
