package handle

import (
	"bytes"
	"encoding"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestSecretValue_StringRedacts(t *testing.T) {
	s := NewSecretValueFromString("super-secret-token")
	if got := s.String(); got != "<redacted>" {
		t.Errorf("String: got %q, want <redacted>", got)
	}
	// fmt.Sprint variants: all route through Stringer.
	if got := fmt.Sprint(s); got != "<redacted>" {
		t.Errorf("Sprint: got %q", got)
	}
	if got := fmt.Sprintf("%v", s); got != "<redacted>" {
		t.Errorf("Sprintf %%v: got %q", got)
	}
	if got := fmt.Sprintf("%s", s); got != "<redacted>" {
		t.Errorf("Sprintf %%s: got %q", got)
	}
}

// TestSecretValue_GoStringRedacts pins %#v — the most common
// "but I just wanted to debug" leak surface.
func TestSecretValue_GoStringRedacts(t *testing.T) {
	s := NewSecretValueFromString("super-secret-token")
	got := fmt.Sprintf("%#v", s)
	if got != "<redacted>" {
		t.Errorf("Sprintf %%#v: got %q, want <redacted>", got)
	}
	// Cross-check: the literal plaintext does NOT appear anywhere
	// in the formatted output (covers any path through reflect-
	// based formatters).
	if strings.Contains(got, "super-secret-token") {
		t.Errorf("plaintext leaked through %%#v: %q", got)
	}
}

func TestSecretValue_QuoteVerbAlsoRedacts(t *testing.T) {
	// %q on a non-string type calls Stringer.
	s := NewSecretValueFromString("super-secret-token")
	got := fmt.Sprintf("%q", s)
	if !strings.Contains(got, "<redacted>") {
		t.Errorf("Sprintf %%q: got %q, want a redacted form", got)
	}
	if strings.Contains(got, "super-secret-token") {
		t.Errorf("plaintext leaked through %%q: %q", got)
	}
}

// TestSecretValue_MarshalJSONRedacts pins the load-bearing redaction
// at the JSON boundary. Without this, a SecretValue embedded in a
// struct that gets json.Marshal'd anywhere (audit log, tool output,
// memory snapshot) would round-trip the plaintext.
//
// json.Marshal HTML-escapes `<` and `>` by default, so the on-wire
// bytes are `"<redacted>"`. We decode through json.Unmarshal
// and check the semantic value rather than the byte form.
func TestSecretValue_MarshalJSONRedacts(t *testing.T) {
	s := NewSecretValueFromString("super-secret-token")
	out, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded string
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded != "<redacted>" {
		t.Errorf("Decoded value: got %q, want <redacted>", decoded)
	}
	if strings.Contains(string(out), "super-secret-token") {
		t.Errorf("plaintext leaked through json.Marshal: %s", string(out))
	}
}

// TestSecretValue_StructMarshalingRedacts verifies the redaction
// holds when the SecretValue is a field in a containing struct
// (the common path: a tool output struct with a Token field).
func TestSecretValue_StructMarshalingRedacts(t *testing.T) {
	type ToolOutput struct {
		Status string      `json:"status"`
		Token  SecretValue `json:"token"`
	}
	out, err := json.Marshal(ToolOutput{
		Status: "ok",
		Token:  NewSecretValueFromString("super-secret-token"),
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	// Decode through Unmarshal — the on-wire bytes carry HTML
	// escapes for `<` and `>` but the semantic value is the
	// redaction string.
	var decoded struct {
		Status string `json:"status"`
		Token  string `json:"token"`
	}
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.Token != "<redacted>" {
		t.Errorf("Decoded token: got %q, want <redacted>", decoded.Token)
	}
	if strings.Contains(string(out), "super-secret-token") {
		t.Errorf("plaintext leaked through containing struct: %s", string(out))
	}
}

func TestSecretValue_MarshalBinaryRefuses(t *testing.T) {
	s := NewSecretValueFromString("super-secret-token")
	// Confirm SecretValue does implement BinaryMarshaler so the
	// encoder will pick our refusing implementation rather than
	// fall through to a different strategy.
	var bm encoding.BinaryMarshaler = s
	out, err := bm.MarshalBinary()
	if !errors.Is(err, ErrSecretValueNotMarshalable) {
		t.Errorf("expected ErrSecretValueNotMarshalable, got %v", err)
	}
	if out != nil {
		t.Errorf("MarshalBinary returned non-nil bytes: %v", out)
	}
}

func TestSecretValue_MarshalTextRedacts(t *testing.T) {
	s := NewSecretValueFromString("super-secret-token")
	var tm encoding.TextMarshaler = s
	out, err := tm.MarshalText()
	if err != nil {
		t.Fatalf("MarshalText: %v", err)
	}
	if string(out) != "<redacted>" {
		t.Errorf("MarshalText: got %q, want <redacted>", out)
	}
}

func TestSecretValue_RevealReturnsPlaintext(t *testing.T) {
	const plaintext = "super-secret-token"
	s := NewSecretValueFromString(plaintext)
	if got := s.Reveal(); got != plaintext {
		t.Errorf("Reveal: got %q, want %q", got, plaintext)
	}
}

func TestSecretValue_RevealOnZeroValue(t *testing.T) {
	var s SecretValue
	if got := s.Reveal(); got != "" {
		t.Errorf("zero-value Reveal: got %q, want empty", got)
	}
	if !s.IsZero() {
		t.Errorf("zero-value IsZero: got false")
	}
}

// TestSecretValue_ConsumeIntoWritesAndScrubs is the load-bearing
// scrub-after-use property. After ConsumeInto, the buffer is
// zeroed and Reveal() returns "" — the secret no longer lingers
// in process memory between the write and the next GC cycle.
func TestSecretValue_ConsumeIntoWritesAndScrubs(t *testing.T) {
	s := NewSecretValueFromString("super-secret-token")
	var buf bytes.Buffer
	n, err := s.ConsumeInto(&buf)
	if err != nil {
		t.Fatalf("ConsumeInto: %v", err)
	}
	if n != int64(len("super-secret-token")) {
		t.Errorf("ConsumeInto wrote %d bytes, want %d", n, len("super-secret-token"))
	}
	if buf.String() != "super-secret-token" {
		t.Errorf("buf content: got %q, want %q", buf.String(), "super-secret-token")
	}
	if got := s.Reveal(); got != "" {
		t.Errorf("after ConsumeInto, Reveal must be empty (scrub failed): got %q", got)
	}
	if !s.IsZero() {
		t.Errorf("after ConsumeInto, IsZero: got false")
	}
}

func TestSecretValue_ConsumeIntoNilReceiverNoOp(t *testing.T) {
	var s *SecretValue
	var buf bytes.Buffer
	n, err := s.ConsumeInto(&buf)
	if err != nil {
		t.Errorf("nil receiver ConsumeInto: got %v, want nil", err)
	}
	if n != 0 {
		t.Errorf("nil receiver ConsumeInto: wrote %d, want 0", n)
	}
	if buf.Len() != 0 {
		t.Errorf("nil receiver ConsumeInto: buf=%q", buf.String())
	}
}

func TestSecretValue_ConsumeIntoTwiceIsIdempotent(t *testing.T) {
	s := NewSecretValueFromString("super-secret-token")
	var buf1, buf2 bytes.Buffer
	if _, err := s.ConsumeInto(&buf1); err != nil {
		t.Fatalf("first ConsumeInto: %v", err)
	}
	n, err := s.ConsumeInto(&buf2)
	if err != nil {
		t.Errorf("second ConsumeInto: got %v, want nil", err)
	}
	if n != 0 {
		t.Errorf("second ConsumeInto wrote %d, want 0 (already scrubbed)", n)
	}
}

// TestSecretValue_FmtPrintEverywhereStaysRedacted is the
// "comprehensive sweep" test: tries every fmt verb that could
// reasonably surface the plaintext and confirms none of them do.
func TestSecretValue_FmtPrintEverywhereStaysRedacted(t *testing.T) {
	const plaintext = "super-secret-token"
	s := NewSecretValueFromString(plaintext)

	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%T:%v"} {
		got := fmt.Sprintf(verb, s)
		if strings.Contains(got, plaintext) {
			t.Errorf("verb %q leaked plaintext: %q", verb, got)
		}
	}
}

// TestSecretValue_NewSecretValueBorrowsBuffer pins the documented
// contract: NewSecretValue (byte-slice constructor) borrows the
// caller's buffer; a zero-on-the-original also clears the value.
// Production callers (vault reader) rely on this for scrub-on-error.
func TestSecretValue_NewSecretValueBorrowsBuffer(t *testing.T) {
	buf := []byte("super-secret-token")
	s := NewSecretValue(buf)
	for i := range buf {
		buf[i] = 0
	}
	if got := s.Reveal(); got != "\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00" {
		t.Errorf("after caller zeroed buf, Reveal should be the zeroed bytes: got %q", got)
	}
}

// TestSecretValue_NewSecretValueFromStringCopies pins the converse:
// the string constructor allocates a fresh slice; the caller's
// string is not borrowed.
func TestSecretValue_NewSecretValueFromStringCopies(t *testing.T) {
	plaintext := "super-secret-token"
	s := NewSecretValueFromString(plaintext)
	// Modifying the original (impossible in Go for strings) is
	// not the test; the test is that the SecretValue's bytes are
	// independent. ConsumeInto scrubbing must not affect the
	// caller's notion of the string.
	var buf bytes.Buffer
	_, _ = s.ConsumeInto(&buf)
	if plaintext != "super-secret-token" {
		t.Errorf("string constructor leaked into the caller's view: %q", plaintext)
	}
}
