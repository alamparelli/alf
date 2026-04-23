package media

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestBuildImageVisionBlock(t *testing.T) {
	data := []byte("hello")
	b := BuildImageVisionBlock(data, "x.png", "image/png")
	if b.Type != "image" {
		t.Errorf("expected type image, got %q", b.Type)
	}
	if b.Source == nil || b.Source.Type != "base64" {
		t.Fatalf("source should be base64: %+v", b.Source)
	}
	if b.Source.MediaType != "image/png" {
		t.Errorf("media type: %q", b.Source.MediaType)
	}
	decoded, err := base64.StdEncoding.DecodeString(b.Source.Data)
	if err != nil || string(decoded) != "hello" {
		t.Errorf("round-trip failed: %q / err=%v", decoded, err)
	}
}

func TestBuildDocumentVisionBlock(t *testing.T) {
	b := BuildDocumentVisionBlock([]byte("pdfbytes"), "x.pdf", "application/pdf")
	if b.Type != "document" {
		t.Errorf("expected type document, got %q", b.Type)
	}
	if b.Source.MediaType != "application/pdf" {
		t.Errorf("media type: %q", b.Source.MediaType)
	}
}

func TestExtractTextFromDocument_PlainText(t *testing.T) {
	got := ExtractTextFromDocument([]byte("hello world"), "text/plain")
	if got != "hello world" {
		t.Errorf("expected plain passthrough, got %q", got)
	}
}

func TestExtractTextFromDocument_InvalidUTF8(t *testing.T) {
	// Invalid continuation byte → must go through makeValidUTF8.
	bad := []byte{'h', 'i', 0xff, 0xfe}
	got := ExtractTextFromDocument(bad, "text/plain")
	if !strings.HasPrefix(got, "hi") {
		t.Errorf("expected hi prefix, got %q", got)
	}
	if !strings.ContainsRune(got, '\ufffd') {
		t.Errorf("expected replacement rune, got %q", got)
	}
}

func TestExtractTextFromDocument_BinaryFallback(t *testing.T) {
	got := ExtractTextFromDocument([]byte{0x00, 0x01, 0x02}, "application/x-binary")
	if !strings.Contains(got, "Binary document") {
		t.Errorf("expected binary fallback, got %q", got)
	}
}

func TestUTF8Valid(t *testing.T) {
	if !utf8Valid([]byte("ASCII only")) {
		t.Error("ASCII must be valid")
	}
	if !utf8Valid([]byte("café")) {
		t.Error("2-byte sequence must be valid")
	}
	if !utf8Valid([]byte("世界")) {
		t.Error("3-byte sequence must be valid")
	}
	// Invalid leading byte.
	if utf8Valid([]byte{0xff}) {
		t.Error("0xff should be invalid")
	}
	// Truncated multi-byte.
	if utf8Valid([]byte{0xc3}) {
		t.Error("truncated 2-byte should be invalid")
	}
	if utf8Valid([]byte{0xe2, 0x98}) {
		t.Error("truncated 3-byte should be invalid")
	}
}

func TestMakeValidUTF8(t *testing.T) {
	s := makeValidUTF8([]byte("ok"))
	if s != "ok" {
		t.Errorf("expected passthrough, got %q", s)
	}
	// Replace invalid byte.
	s = makeValidUTF8([]byte{'a', 0xff, 'b'})
	if !strings.Contains(s, "a") || !strings.Contains(s, "b") {
		t.Errorf("good bytes should survive, got %q", s)
	}
	if !strings.ContainsRune(s, '\ufffd') {
		t.Errorf("expected replacement char, got %q", s)
	}
}
