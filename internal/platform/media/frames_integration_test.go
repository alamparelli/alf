package media

import (
	"os"
	"os/exec"
	"testing"
)

func TestExtractFrames_ContactSheet(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}

	// Create a 6-second test video.
	tmp := t.TempDir() + "/test.mp4"
	cmd := exec.Command("ffmpeg", "-f", "lavfi", "-i",
		"testsrc=duration=6:size=640x480:rate=25",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-y", tmp)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create test video: %v\n%s", err, out)
	}

	paths, err := ExtractFrames(tmp, 12)
	if err != nil {
		t.Fatalf("ExtractFrames: %v", err)
	}
	defer func() {
		for _, p := range paths {
			os.Remove(p)
		}
	}()

	if len(paths) != 1 {
		t.Fatalf("expected 1 contact sheet, got %d files", len(paths))
	}

	info, err := os.Stat(paths[0])
	if err != nil {
		t.Fatalf("stat sheet: %v", err)
	}
	t.Logf("contact sheet: %s (%d bytes)", paths[0], info.Size())

	if info.Size() < 1000 {
		t.Error("contact sheet suspiciously small")
	}
}

func TestExtractFrames_ShortVideo(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}

	// 0.3-second video → should get single frame.
	tmp := t.TempDir() + "/short.mp4"
	cmd := exec.Command("ffmpeg", "-f", "lavfi", "-i",
		"testsrc=duration=0.3:size=320x240:rate=25",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-y", tmp)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create test video: %v\n%s", err, out)
	}

	paths, err := ExtractFrames(tmp, 12)
	if err != nil {
		t.Fatalf("ExtractFrames: %v", err)
	}
	defer func() {
		for _, p := range paths {
			os.Remove(p)
		}
	}()

	if len(paths) != 1 {
		t.Fatalf("expected 1 frame for short video, got %d", len(paths))
	}
	t.Logf("single frame extracted for 0.3s video")
}
