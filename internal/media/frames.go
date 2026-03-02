package media

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// ExtractFrames extracts evenly-spaced JPEG frames from a video or GIF.
// Returns paths to temporary frame files. Caller is responsible for cleanup.
func ExtractFrames(videoPath string, maxFrames int) ([]string, error) {
	if maxFrames <= 0 {
		maxFrames = 5
	}

	duration, err := probeDuration(videoPath)
	if err != nil {
		// Fallback: extract first frame only (works for GIFs and broken metadata).
		return extractSingleFrame(videoPath)
	}

	// Adjust frame count for short videos.
	switch {
	case duration < 1:
		maxFrames = 1
	case duration < 3:
		maxFrames = min(maxFrames, 2)
	case duration < 5:
		maxFrames = min(maxFrames, 3)
	}

	if maxFrames == 1 {
		return extractSingleFrame(videoPath)
	}

	// Calculate evenly-spaced timestamps.
	timestamps := make([]float64, maxFrames)
	interval := duration / float64(maxFrames+1)
	for i := range timestamps {
		timestamps[i] = interval * float64(i+1)
	}

	// Build select filter: select frames closest to target timestamps.
	// Using the select filter with between() for each target time.
	selectParts := make([]string, len(timestamps))
	for i, ts := range timestamps {
		selectParts[i] = fmt.Sprintf("between(t,%.2f,%.2f)", ts-0.1, ts+0.1)
	}
	selectFilter := strings.Join(selectParts, "+")

	outPattern := filepath.Join(os.TempDir(), fmt.Sprintf("alf-frame-%d-%%03d.jpg", os.Getpid()))

	args := []string{
		"-i", videoPath,
		"-vf", fmt.Sprintf("select='%s',scale='min(1280,iw)':-1", selectFilter),
		"-vsync", "vfr",
		"-q:v", "2",
		"-frames:v", strconv.Itoa(maxFrames),
		outPattern,
	}

	cmd := exec.Command("ffmpeg", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("ffmpeg extract failed: %w\n%s", err, string(out))
	}

	// Collect output files and make them world-readable for claude subprocess.
	paths, err := collectFrameFiles(outPattern, maxFrames)
	for _, p := range paths {
		os.Chmod(p, 0o644)
	}
	return paths, err
}

// probeDuration returns video duration in seconds using ffprobe.
func probeDuration(path string) (float64, error) {
	cmd := exec.Command("ffprobe",
		"-v", "quiet",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		path,
	)
	out, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
}

// extractSingleFrame extracts just the first frame.
func extractSingleFrame(videoPath string) ([]string, error) {
	outPath := filepath.Join(os.TempDir(), fmt.Sprintf("alf-frame-%d-001.jpg", os.Getpid()))
	cmd := exec.Command("ffmpeg",
		"-i", videoPath,
		"-vf", "scale='min(1280,iw)':-1",
		"-frames:v", "1",
		"-q:v", "2",
		"-y",
		outPath,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("ffmpeg single frame failed: %w\n%s", err, string(out))
	}
	if _, err := os.Stat(outPath); err != nil {
		return nil, fmt.Errorf("frame not created: %w", err)
	}
	os.Chmod(outPath, 0o644)
	return []string{outPath}, nil
}

// collectFrameFiles gathers generated frame files matching the output pattern.
func collectFrameFiles(pattern string, maxFrames int) ([]string, error) {
	var paths []string
	for i := 1; i <= maxFrames; i++ {
		p := fmt.Sprintf(strings.Replace(pattern, "%03d", "%03d", 1), i)
		// Re-format with the actual index.
		p = filepath.Join(os.TempDir(), fmt.Sprintf("alf-frame-%d-%03d.jpg", os.Getpid(), i))
		if _, err := os.Stat(p); err == nil {
			paths = append(paths, p)
		}
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no frames extracted")
	}
	return paths, nil
}

// ExtractAudio extracts the audio track from a video as a WAV file.
// Returns empty string if the video has no audio stream.
func ExtractAudio(videoPath string) (string, error) {
	// Check if video has an audio stream.
	cmd := exec.Command("ffprobe",
		"-v", "quiet",
		"-select_streams", "a",
		"-show_entries", "stream=codec_type",
		"-of", "default=noprint_wrappers=1:nokey=1",
		videoPath,
	)
	out, err := cmd.Output()
	if err != nil || strings.TrimSpace(string(out)) == "" {
		return "", nil // No audio stream.
	}

	outPath := filepath.Join(os.TempDir(), fmt.Sprintf("alf-audio-%d.wav", os.Getpid()))
	cmd = exec.Command("ffmpeg",
		"-i", videoPath,
		"-vn",
		"-acodec", "pcm_s16le",
		"-ar", "16000",
		"-ac", "1",
		"-y",
		outPath,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("ffmpeg audio extract failed: %w\n%s", err, string(out))
	}

	// Verify the file was created and has content.
	info, err := os.Stat(outPath)
	if err != nil || info.Size() < 1000 {
		os.Remove(outPath)
		return "", nil // Too short / empty audio.
	}
	os.Chmod(outPath, 0o644)
	return outPath, nil
}

// HasAudioStream checks if a video file contains an audio track.
func HasAudioStream(videoPath string) bool {
	cmd := exec.Command("ffprobe",
		"-v", "quiet",
		"-select_streams", "a",
		"-show_entries", "stream=codec_type",
		"-of", "default=noprint_wrappers=1:nokey=1",
		videoPath,
	)
	out, err := cmd.Output()
	return err == nil && strings.TrimSpace(string(out)) != ""
}

