package handle

import (
	"encoding/json"
	"errors"
	"io"
)

// SecretValue is the §7.5 "secret never crosses back as cleartext"
// envelope. SecretsHandle.Get returns one of these instead of a raw
// string so the runtime composition surface (JSON marshalling, fmt
// printing, log lines, tool-output reinjection) never accidentally
// surfaces the plaintext to the LLM context.
//
// Threat model. Without this wrapper, a capability that reads a
// secret and includes the value in its tool output would round-trip
// the cleartext through Runtime → LLM context. Even a benign
// %v on the result struct, a json.Marshal in a log line, or a
// memory-recall snapshot of the conversation persists the secret in
// places the kernel prompt cannot demote post-hoc. SecretValue
// closes that channel by construction:
//
//   - String() returns "<redacted>" — covers fmt.Sprintf, log.Printf
//     %v, %s, %q (which falls back to String for non-string types).
//   - GoString() returns "<redacted>" — covers %#v.
//   - MarshalJSON returns "<redacted>" as a JSON string — the LLM
//     sees a placeholder, not the bytes; downstream JSON consumers
//     get a syntactically valid string but no plaintext.
//   - MarshalBinary returns ErrSecretValueNotMarshalable — the
//     gob path explicitly fails rather than silently emitting
//     bytes. Same for any encoder that tries the BinaryMarshaler
//     interface.
//
// Trusted-caller path. Code that legitimately needs the plaintext
// — building an HTTP header, computing an HMAC, comparing to a
// challenge — calls one of two methods:
//
//   - Reveal(): returns the underlying string. Audit-greppable name
//     so a `git grep` in a security review surfaces every site that
//     intentionally exposes the secret. Use sparingly.
//   - ConsumeInto(w io.Writer): writes the plaintext to w and
//     zeroes the internal buffer. Single-use; subsequent calls (or
//     later Reveal()) get an empty string. Recommended for the
//     common cases (HTTP header injection, HMAC seed) because the
//     consumed bytes do not linger in process memory between the
//     write and the next GC cycle.
//
// Why bytes not string. SecretValue holds a []byte rather than a
// string so ConsumeInto can zero the slice in-place after writing.
// Strings in Go are immutable; a string-backed implementation
// could not deliver the "scrub after use" property without
// allocating a copy purely for zeroing.
type SecretValue struct {
	// v is the plaintext bytes. Nil after ConsumeInto. Tests can
	// observe nil via the unexported field via Reveal() returning
	// "" — the public API never exposes the slice header itself.
	v []byte
}

// ErrSecretValueNotMarshalable is returned by MarshalBinary so a
// gob/binary encoder cannot silently emit secret bytes. The
// MarshalJSON path uses a different strategy (returns a redaction
// string) because JSON consumers commonly expect a printable
// representation; failing JSON would break logging pipelines that
// don't actually care about the value.
var ErrSecretValueNotMarshalable = errors.New("handle: SecretValue is not binary-marshalable; use Reveal or ConsumeInto in trusted code")

// NewSecretValue wraps b. The caller's slice is borrowed — not
// copied — so a follow-up zero on b also clears the SecretValue.
// In tests you typically build with []byte("..."); production
// callers (vault reader) typically pass a buffer they've already
// scrubbed-on-error.
func NewSecretValue(b []byte) SecretValue {
	return SecretValue{v: b}
}

// NewSecretValueFromString is the convenience constructor for the
// common case where the secret arrives as a Go string (vault
// reader returning string). Allocates a fresh byte slice; the
// caller's string can be discarded.
func NewSecretValueFromString(s string) SecretValue {
	if s == "" {
		return SecretValue{}
	}
	b := make([]byte, len(s))
	copy(b, s)
	return SecretValue{v: b}
}

// String implements fmt.Stringer. Always returns "<redacted>".
// This is the load-bearing redaction: %v, %s, log.Printf, and
// fmt.Sprint all route through here for non-string types that
// implement Stringer.
func (s SecretValue) String() string {
	return "<redacted>"
}

// GoString implements fmt.GoStringer so %#v also redacts. Without
// this, `fmt.Sprintf("%#v", s)` would dump the struct's internals
// (including the byte slice) — the redaction would silently leak
// to anyone who used the verbose printer.
func (s SecretValue) GoString() string {
	return "<redacted>"
}

// MarshalJSON implements encoding/json.Marshaler. Returns
// "<redacted>" as a JSON string. Important: the wrapper has no
// `*SecretValue` receiver because json.Marshal on a value type
// uses the value-receiver method via the Marshaler interface
// matching, but a pointer receiver would not be detected if the
// caller had a value (Go's interface-method-set rule). Value
// receiver covers both cases.
func (s SecretValue) MarshalJSON() ([]byte, error) {
	return json.Marshal("<redacted>")
}

// MarshalBinary implements encoding.BinaryMarshaler with an
// explicit refusal. Encoders that try this interface (gob, msgpack,
// some custom serialisers) fail loudly instead of silently emitting
// the bytes.
func (s SecretValue) MarshalBinary() ([]byte, error) {
	return nil, ErrSecretValueNotMarshalable
}

// MarshalText implements encoding.TextMarshaler with the same
// redaction MarshalJSON uses. Covers TOML, INI, and other
// text-based encoders that try TextMarshaler before falling back
// to fmt.Stringer.
func (s SecretValue) MarshalText() ([]byte, error) {
	return []byte("<redacted>"), nil
}

// Reveal returns the plaintext as a string. Audit-greppable name
// — every call site is meant to be visible to a security review.
// Use sparingly; ConsumeInto is preferred for the cases where
// the secret's lifetime can be bounded to a single write.
//
// Returns "" if the value was already consumed via ConsumeInto.
func (s SecretValue) Reveal() string {
	if len(s.v) == 0 {
		return ""
	}
	return string(s.v)
}

// IsZero reports whether the SecretValue holds anything. Useful for
// "did I get a value or not" checks without calling Reveal (which
// would surface the plaintext to the calling stack frame).
func (s SecretValue) IsZero() bool {
	return len(s.v) == 0
}

// ConsumeInto writes the plaintext to w and zeroes the internal
// buffer in place. Returns the number of bytes written. After the
// call the SecretValue's Reveal() returns "". Subsequent
// ConsumeInto calls are no-ops returning (0, nil).
//
// The buffer-scrub property only holds when SecretValue is used by
// pointer receiver. The non-pointer ConsumeInto on a value would
// scrub a copy. Documented here because Go's method-set rules
// surprise people: callers passing SecretValue by value into a
// helper, then calling ConsumeInto, would not scrub the original.
//
// Production code should always hold *SecretValue when scrub-after-
// use matters (HTTP request preparation, HMAC computation).
func (s *SecretValue) ConsumeInto(w io.Writer) (int64, error) {
	if s == nil || len(s.v) == 0 {
		return 0, nil
	}
	n, err := w.Write(s.v)
	for i := range s.v {
		s.v[i] = 0
	}
	s.v = nil
	return int64(n), err
}
