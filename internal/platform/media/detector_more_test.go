package media

import "testing"

func TestIsVideoContent(t *testing.T) {
	tests := []struct {
		mime, name string
		want       bool
	}{
		{"video/mp4", "x.mp4", true},
		{"application/octet-stream", "clip.mov", true},
		{"application/octet-stream", "clip.MOV", true},
		{"text/plain", "x.txt", false},
		{"", "x.avi", true},
		{"", "x.webm", true},
		{"image/png", "x.png", false},
	}
	for _, tt := range tests {
		if got := IsVideoContent(tt.mime, tt.name); got != tt.want {
			t.Errorf("IsVideoContent(%q, %q) = %v, want %v", tt.mime, tt.name, got, tt.want)
		}
	}
}

func TestIsPDFContent(t *testing.T) {
	if !IsPDFContent("application/pdf") {
		t.Error("PDF mime must be pdf")
	}
	if IsPDFContent("text/plain") {
		t.Error("text/plain is not pdf")
	}
}

func TestIsVoiceContent(t *testing.T) {
	for _, mime := range []string{"audio/mp3", "audio/ogg", "audio/wav"} {
		if !IsVoiceContent(mime) {
			t.Errorf("%s should be voice content", mime)
		}
	}
	if IsVoiceContent("video/mp4") {
		t.Error("video is not voice")
	}
	if IsVoiceContent("") {
		t.Error("empty must not be voice")
	}
}

// isDocxFile is always true for PK-prefixed data per its current stub. Exercise
// both the trivial path and the DetectMimeType DOCX branch for coverage.
func TestDetectMimeType_DOCXStub(t *testing.T) {
	// Any PK-prefixed data counts as DOCX per the stub.
	data := []byte("PK\x03\x04dummy")
	got := DetectMimeType(data, "file.docx")
	want := "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	if got != want {
		t.Errorf("expected DOCX mime, got %q", got)
	}
}
