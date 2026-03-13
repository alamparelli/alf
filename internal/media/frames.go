package media

import (
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	thumbWidth  = 320 // px per thumbnail - smaller = more frames fit
	thumbHeight = 240 // px per thumbnail - 4:3 aspect ratio
)

// ExtractFrames extracts evenly-spaced JPEG frames from a video or GIF,
// then concatenates them into a single contact sheet with timestamp overlays.
// Returns a single-element slice with the contact sheet path. Caller cleans up.
func ExtractFrames(videoPath string, maxFrames int) ([]string, error) {
	if maxFrames <= 0 {
		maxFrames = 12
	}

	duration, err := probeDuration(videoPath)
	if err != nil {
		return extractSingleFrame(videoPath)
	}

	// Scale frame count to duration for better coverage.
	maxFrames = adaptFrameCount(duration, maxFrames)

	if maxFrames == 1 {
		return extractSingleFrame(videoPath)
	}

	// Calculate evenly-spaced timestamps.
	timestamps := make([]float64, maxFrames)
	interval := duration / float64(maxFrames+1)
	for i := range timestamps {
		timestamps[i] = interval * float64(i+1)
	}

	selectParts := make([]string, len(timestamps))
	for i, ts := range timestamps {
		selectParts[i] = fmt.Sprintf("between(t,%.2f,%.2f)", ts-0.1, ts+0.1)
	}
	selectFilter := strings.Join(selectParts, "+")

	outPattern := filepath.Join(os.TempDir(), fmt.Sprintf("alf-frame-%d-%%03d.jpg", os.Getpid()))

	args := []string{
		"-i", videoPath,
		"-vf", fmt.Sprintf("select='%s',scale='min(%d,iw)':-1", selectFilter, thumbWidth),
		"-vsync", "vfr",
		"-q:v", "3",
		"-frames:v", strconv.Itoa(maxFrames),
		outPattern,
	}

	cmd := exec.Command("ffmpeg", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("ffmpeg extract failed: %w\n%s", err, string(out))
	}

	paths, err := collectFrameFiles(outPattern, maxFrames)
	if err != nil {
		return nil, err
	}

	// Concatenate frames into a single contact sheet with timestamps.
	sheet, err := buildContactSheet(paths, timestamps)
	if err != nil {
		// Fallback: return individual frames.
		for _, p := range paths {
			os.Chmod(p, 0o644)
		}
		return paths, nil
	}

	// Clean up individual frames, return only the sheet.
	for _, p := range paths {
		os.Remove(p)
	}
	os.Chmod(sheet, 0o644)
	return []string{sheet}, nil
}

// adaptFrameCount adjusts the number of frames based on content duration.
func adaptFrameCount(duration float64, maxFrames int) int {
	switch {
	case duration < 0.5:
		return 1
	case duration < 2:
		return min(maxFrames, 4)
	case duration < 5:
		return min(maxFrames, 8)
	case duration < 15:
		return min(maxFrames, 12)
	case duration < 60:
		return min(maxFrames, 16)
	default:
		// Long videos: ~1 frame per 5 seconds, capped at maxFrames.
		n := int(math.Ceil(duration / 5))
		return min(n, maxFrames)
	}
}

// buildContactSheet concatenates frame images into a grid with timestamp overlays.
// Layout: 4 columns (standard), 3 for ≤6 frames, 2 for ≤4.
func buildContactSheet(framePaths []string, timestamps []float64) (string, error) {
	n := len(framePaths)
	if n <= 1 {
		return framePaths[0], nil
	}

	// Determine grid columns.
	cols := 4
	switch {
	case n <= 4:
		cols = 2
	case n <= 6:
		cols = 3
	}
	rows := (n + cols - 1) / cols

	// Build ffmpeg input args.
	var args []string
	for _, p := range framePaths {
		args = append(args, "-i", p)
	}

	// Pad to fill the grid (use last frame as filler).
	total := rows * cols
	for i := n; i < total; i++ {
		args = append(args, "-i", framePaths[n-1])
	}

	// Build filter: scale + timestamp overlay + xstack.
	var filterParts []string
	for i := 0; i < total; i++ {
		ts := ""
		if i < len(timestamps) {
			ts = formatTimestamp(timestamps[i])
		}
		// Scale to uniform size, overlay timestamp in top-left corner.
		// Use pad with fixed height (240px for 320px width at 4:3) so xstack gets uniform tiles.
		scale := fmt.Sprintf("[%d:v]scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2", i, thumbWidth, thumbHeight, thumbWidth, thumbHeight)
		if ts != "" {
			// White text with dark background box for readability.
			scale += fmt.Sprintf(",drawtext=text='%s':fontsize=14:fontcolor=white:box=1:boxcolor=black@0.5:boxborderw=3:x=4:y=4", ts)
		}
		filterParts = append(filterParts, scale+fmt.Sprintf("[v%d]", i))
	}

	// Build xstack layout string.
	var inputs []string
	var layout []string
	for i := 0; i < total; i++ {
		inputs = append(inputs, fmt.Sprintf("[v%d]", i))
		col := i % cols
		row := i / cols
		layout = append(layout, fmt.Sprintf("%d*w0_%d*h0", col, row))
	}

	filter := strings.Join(filterParts, ";") + ";" +
		strings.Join(inputs, "") +
		fmt.Sprintf("xstack=inputs=%d:layout=%s[out]", total, strings.Join(layout, "|"))

	outPath := filepath.Join(os.TempDir(), fmt.Sprintf("alf-sheet-%d.jpg", os.Getpid()))
	args = append(args,
		"-filter_complex", filter,
		"-map", "[out]",
		"-q:v", "4",
		"-y", outPath,
	)

	cmd := exec.Command("ffmpeg", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("ffmpeg contact sheet failed: %w\n%s", err, string(out))
	}

	if _, err := os.Stat(outPath); err != nil {
		return "", fmt.Errorf("contact sheet not created")
	}
	return outPath, nil
}

// formatTimestamp converts seconds to a human-readable timestamp.
func formatTimestamp(seconds float64) string {
	if seconds < 60 {
		return fmt.Sprintf("%.1fs", seconds)
	}
	m := int(seconds) / 60
	s := seconds - float64(m*60)
	return fmt.Sprintf("%d:%04.1f", m, s)
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
		"-vf", fmt.Sprintf("scale='min(%d,iw)':-1", thumbWidth),
		"-frames:v", "1",
		"-q:v", "3",
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
		p := filepath.Join(os.TempDir(), fmt.Sprintf("alf-frame-%d-%03d.jpg", os.Getpid(), i))
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
