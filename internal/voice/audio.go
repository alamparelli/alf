package voice

import (
	"encoding/binary"
	"fmt"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
)

// convertToWAV converts any audio file to 16kHz mono PCM WAV using ffmpeg.
// Returns the path to the temporary WAV file. Caller must clean up.
func convertToWAV(audioPath string) (string, error) {
	wavFile, err := os.CreateTemp("", "alf-whisper-*.wav")
	if err != nil {
		return "", fmt.Errorf("create temp wav: %w", err)
	}
	wavPath := wavFile.Name()
	wavFile.Close()

	cmd := exec.Command("ffmpeg",
		"-i", audioPath,
		"-vn",
		"-acodec", "pcm_s16le",
		"-ar", "16000",
		"-ac", "1",
		"-y", wavPath,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		os.Remove(wavPath)
		return "", fmt.Errorf("ffmpeg: %w: %s", err, out)
	}

	return wavPath, nil
}

// readWAVSamples reads a 16-bit PCM WAV file and returns normalized float32 samples.
// Expects mono 16kHz PCM produced by convertToWAV.
// Properly scans for chunks — handles WAV files with extra chunks (LIST, fact, etc.)
// that ffmpeg may insert between fmt and data.
func readWAVSamples(wavPath string) ([]float32, error) {
	data, err := os.ReadFile(wavPath)
	if err != nil {
		return nil, err
	}

	if len(data) < 12 {
		return nil, fmt.Errorf("file too small for WAV")
	}

	// Verify RIFF/WAVE magic.
	if string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, fmt.Errorf("not a WAV file")
	}

	// Scan chunks starting after "WAVE" (offset 12).
	var audioFormat, channels, bitsPerSample uint16
	var pcmData []byte

	pos := 12
	for pos+8 <= len(data) {
		chunkID := string(data[pos : pos+4])
		chunkSize := int(binary.LittleEndian.Uint32(data[pos+4 : pos+8]))
		chunkData := pos + 8

		if chunkData+chunkSize > len(data) {
			chunkSize = len(data) - chunkData
		}

		switch chunkID {
		case "fmt ":
			if chunkSize < 16 {
				return nil, fmt.Errorf("fmt chunk too small")
			}
			audioFormat = binary.LittleEndian.Uint16(data[chunkData : chunkData+2])
			channels = binary.LittleEndian.Uint16(data[chunkData+2 : chunkData+4])
			bitsPerSample = binary.LittleEndian.Uint16(data[chunkData+14 : chunkData+16])
		case "data":
			pcmData = data[chunkData : chunkData+chunkSize]
		}

		// Advance to next chunk (chunks are word-aligned).
		pos = chunkData + chunkSize
		if chunkSize%2 != 0 {
			pos++
		}
	}

	if audioFormat != 1 {
		return nil, fmt.Errorf("unsupported audio format %d (expected PCM=1)", audioFormat)
	}
	if channels != 1 || bitsPerSample != 16 {
		return nil, fmt.Errorf("expected mono 16-bit, got %d channels %d bits", channels, bitsPerSample)
	}
	if len(pcmData) == 0 {
		return nil, fmt.Errorf("no data chunk found in WAV")
	}

	numSamples := len(pcmData) / 2
	samples := make([]float32, numSamples)
	for i := 0; i < numSamples; i++ {
		sample := int16(binary.LittleEndian.Uint16(pcmData[i*2 : i*2+2]))
		samples[i] = float32(sample) / float32(math.MaxInt16)
	}

	return samples, nil
}

// downloadModel downloads a GGML whisper model from HuggingFace if not already present.
// Models are stored as ggml-{name}.bin in the given directory.
func downloadModel(modelName, modelsDir string) (string, error) {
	filename := fmt.Sprintf("ggml-%s.bin", modelName)
	modelPath := filepath.Join(modelsDir, filename)

	if info, err := os.Stat(modelPath); err == nil && info.Size() > 0 {
		return modelPath, nil
	}

	if err := os.MkdirAll(modelsDir, 0755); err != nil {
		return "", fmt.Errorf("create models dir: %w", err)
	}

	url := fmt.Sprintf("https://huggingface.co/ggerganov/whisper.cpp/resolve/main/%s?download=true", filename)
	log.Printf("voice: downloading model %s from %s", filename, url)

	cmd := exec.Command("curl",
		"--retry", "3",
		"--retry-delay", "5",
		"-fSL",
		"-o", modelPath,
		url,
	)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		os.Remove(modelPath)
		return "", fmt.Errorf("download model %s: %w", filename, err)
	}

	info, err := os.Stat(modelPath)
	if err != nil || info.Size() == 0 {
		os.Remove(modelPath)
		return "", fmt.Errorf("model download produced empty file")
	}

	log.Printf("voice: model %s downloaded (%.0f MB)", filename, float64(info.Size())/1e6)
	return modelPath, nil
}
