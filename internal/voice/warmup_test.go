package voice

import (
	"testing"
)

func TestMakeMinimalWAV(t *testing.T) {
	wav := makeMinimalWAV()

	// Check RIFF header
	if string(wav[0:4]) != "RIFF" {
		t.Errorf("expected RIFF header, got %q", string(wav[0:4]))
	}
	if string(wav[8:12]) != "WAVE" {
		t.Errorf("expected WAVE format, got %q", string(wav[8:12]))
	}
	if string(wav[12:16]) != "fmt " {
		t.Errorf("expected fmt chunk, got %q", string(wav[12:16]))
	}
	if string(wav[36:40]) != "data" {
		t.Errorf("expected data chunk, got %q", string(wav[36:40]))
	}

	// Check PCM format
	format := uint16(wav[20]) | uint16(wav[21])<<8
	if format != 1 {
		t.Errorf("expected PCM format (1), got %d", format)
	}

	// Check sample rate (16000)
	sampleRate := uint32(wav[24]) | uint32(wav[25])<<8 | uint32(wav[26])<<16 | uint32(wav[27])<<24
	if sampleRate != 16000 {
		t.Errorf("expected sample rate 16000, got %d", sampleRate)
	}

	// Total size should be 44 header + 32000 data (1s * 16000 * 2 bytes)
	expectedSize := 44 + 32000
	if len(wav) != expectedSize {
		t.Errorf("WAV size = %d, want %d", len(wav), expectedSize)
	}
}

func TestReadyState(t *testing.T) {
	state := &ReadyState{}

	if state.IsReady() {
		t.Error("expected not ready initially")
	}

	state.ready.Store(true)

	if !state.IsReady() {
		t.Error("expected ready after store")
	}
}
