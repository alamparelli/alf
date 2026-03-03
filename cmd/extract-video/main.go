// extract-video extracts key frames and audio transcript from a video file.
// Designed to be called by Claude Code via Bash tool inside the ALF container.
//
// Usage:
//
//	extract-video <video-path> [--frames N] [--no-audio]
//
// Output (JSON to stdout):
//
//	{
//	  "frames": ["/tmp/frame-001.jpg", "/tmp/frame-002.jpg"],
//	  "duration_seconds": 15.3,
//	  "transcript": "Hello world...",
//	  "transcript_language": "en"
//	}
//
// The caller is responsible for cleaning up the frame files.
// Requires: ffmpeg, ffprobe. Optional: whisper (faster-whisper on amd64, whisper.cpp on arm64).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/alamparelli/alf/internal/voice"
)

type output struct {
	Frames             []string `json:"frames"`
	DurationSeconds    float64  `json:"duration_seconds"`
	Transcript         string   `json:"transcript,omitempty"`
	TranscriptLanguage string   `json:"transcript_language,omitempty"`
	Error              string   `json:"error,omitempty"`
}

func main() {
	maxFrames := flag.Int("frames", 8, "maximum number of frames to extract")
	noAudio := flag.Bool("no-audio", false, "skip audio transcription")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: extract-video <video-path> [--frames N] [--no-audio]\n\n")
		fmt.Fprintf(os.Stderr, "Extract key frames and audio transcript from a video file.\n")
		fmt.Fprintf(os.Stderr, "Outputs JSON with frame paths and transcript to stdout.\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(1)
	}

	videoPath := flag.Arg(0)
	if _, err := os.Stat(videoPath); err != nil {
		fatal(output{Error: fmt.Sprintf("file not found: %s", videoPath)})
	}

	out := output{}

	// Get duration via ffprobe.
	if d, err := probeDuration(videoPath); err == nil {
		out.DurationSeconds = d
	}

	// Adjust frame count for short videos.
	n := *maxFrames
	switch {
	case out.DurationSeconds < 1:
		n = 1
	case out.DurationSeconds < 3:
		n = min(n, 2)
	case out.DurationSeconds < 5:
		n = min(n, 3)
	}

	// Extract frames.
	frames, err := extractFrames(videoPath, n)
	if err != nil {
		fatal(output{Error: fmt.Sprintf("frame extraction failed: %v", err)})
	}
	out.Frames = frames

	// Transcribe audio track.
	if !*noAudio {
		text, lang := transcribeAudio(videoPath)
		out.Transcript = text
		out.TranscriptLanguage = lang
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(out)
}

func fatal(out output) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(out)
	os.Exit(1)
}

// --- ffmpeg helpers ---

func probeDuration(path string) (float64, error) {
	out, err := exec.Command("ffprobe",
		"-v", "quiet",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		path,
	).Output()
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
}

func extractFrames(videoPath string, n int) ([]string, error) {
	prefix := filepath.Join(os.TempDir(), fmt.Sprintf("alf-ev-%d", os.Getpid()))

	if n <= 1 {
		return extractSingle(videoPath, prefix)
	}

	duration, err := probeDuration(videoPath)
	if err != nil || duration < 1 {
		return extractSingle(videoPath, prefix)
	}

	// Build select filter for evenly-spaced timestamps.
	interval := duration / float64(n+1)
	parts := make([]string, n)
	for i := range parts {
		ts := interval * float64(i+1)
		parts[i] = fmt.Sprintf("between(t,%.2f,%.2f)", ts-0.1, ts+0.1)
	}

	pattern := prefix + "-%03d.jpg"
	cmd := exec.Command("ffmpeg",
		"-i", videoPath,
		"-vf", fmt.Sprintf("select='%s',scale='min(1280,iw)':-1", strings.Join(parts, "+")),
		"-vsync", "vfr",
		"-q:v", "2",
		"-frames:v", strconv.Itoa(n),
		pattern,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("ffmpeg: %w\n%s", err, out)
	}

	return collectFiles(prefix, n)
}

func extractSingle(videoPath, prefix string) ([]string, error) {
	out := prefix + "-001.jpg"
	cmd := exec.Command("ffmpeg",
		"-i", videoPath,
		"-vf", "scale='min(1280,iw)':-1",
		"-frames:v", "1",
		"-q:v", "2",
		"-y", out,
	)
	if o, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("ffmpeg: %w\n%s", err, o)
	}
	if _, err := os.Stat(out); err != nil {
		return nil, fmt.Errorf("frame not created")
	}
	return []string{out}, nil
}

func collectFiles(prefix string, n int) ([]string, error) {
	var paths []string
	for i := 1; i <= n; i++ {
		p := fmt.Sprintf("%s-%03d.jpg", prefix, i)
		if _, err := os.Stat(p); err == nil {
			paths = append(paths, p)
		}
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no frames extracted")
	}
	return paths, nil
}

// --- audio transcription ---

func transcribeAudio(videoPath string) (string, string) {
	scriptPath := envOr("ALF_TRANSCRIBE_SCRIPT", "/opt/alf/transcribe.py")
	if !voice.IsAvailable(scriptPath) {
		return "", ""
	}

	// Check for audio stream.
	probe, err := exec.Command("ffprobe",
		"-v", "quiet",
		"-select_streams", "a",
		"-show_entries", "stream=codec_type",
		"-of", "default=noprint_wrappers=1:nokey=1",
		videoPath,
	).Output()
	if err != nil || strings.TrimSpace(string(probe)) == "" {
		return "", ""
	}

	// Extract audio as 16kHz mono WAV.
	audioPath := filepath.Join(os.TempDir(), fmt.Sprintf("alf-ev-audio-%d.wav", os.Getpid()))
	defer os.Remove(audioPath)

	cmd := exec.Command("ffmpeg",
		"-i", videoPath,
		"-vn", "-acodec", "pcm_s16le", "-ar", "16000", "-ac", "1",
		"-y", audioPath,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "audio extraction failed: %s\n", out)
		return "", ""
	}

	info, err := os.Stat(audioPath)
	if err != nil || info.Size() < 1000 {
		return "", ""
	}

	model := envOr("WHISPER_MODEL", "small")
	modelsDir := envOr("ALF_MODELS_DIR", filepath.Join(os.Getenv("HOME"), "data", "models"))

	t, err := voice.New(scriptPath, model, modelsDir, 120*time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "voice init failed: %v\n", err)
		return "", ""
	}
	if err := t.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "voice start failed: %v\n", err)
		return "", ""
	}
	defer t.Stop()

	result, err := t.Transcribe(audioPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "transcription failed: %v\n", err)
		return "", ""
	}
	return result.Text, result.Language
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
