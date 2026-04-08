package controlcenter

import (
	"bytes"
	"fmt"
	"image"
	"image/png"

	"golang.org/x/image/draw"
)

// Allowed magic byte prefixes for image formats.
var imageMagic = []struct {
	name   string
	prefix []byte
}{
	{"PNG", []byte{0x89, 0x50, 0x4E, 0x47}},
	{"JPEG", []byte{0xFF, 0xD8, 0xFF}},
	{"WebP", []byte{0x52, 0x49, 0x46, 0x46}}, // RIFF header
}

// sanitizeImage decodes an image, resizes to avatarSize×avatarSize, and
// re-encodes as PNG. This destroys any embedded payloads, metadata, or
// polyglot content — only pixel data survives.
func sanitizeImage(raw []byte) ([]byte, error) {
	// 1. Validate magic bytes — reject SVG, GIF, BMP, TIFF, etc.
	if !hasAllowedMagic(raw) {
		return nil, fmt.Errorf("unsupported format (only PNG, JPEG, WebP)")
	}

	// 2. Decode — fails on corrupt or polyglot files.
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("decode failed: %w", err)
	}

	// 3. Resize to fixed dimensions.
	dst := image.NewRGBA(image.Rect(0, 0, avatarSize, avatarSize))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)

	// 4. Re-encode as PNG — only pixel data, no metadata.
	var buf bytes.Buffer
	if err := png.Encode(&buf, dst); err != nil {
		return nil, fmt.Errorf("encode failed: %w", err)
	}

	return buf.Bytes(), nil
}

func hasAllowedMagic(data []byte) bool {
	for _, m := range imageMagic {
		if len(data) >= len(m.prefix) && bytes.HasPrefix(data, m.prefix) {
			return true
		}
	}
	return false
}
