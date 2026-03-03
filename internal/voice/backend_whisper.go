//go:build linux && arm64

package voice

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	whisper "github.com/ggerganov/whisper.cpp/bindings/go/pkg/whisper"
)

// Transcriber manages an in-process whisper.cpp model via CGO.
// Used on Linux arm64 where faster-whisper doesn't work.
type Transcriber struct {
	mu        sync.Mutex
	modelName string
	modelsDir string
	timeout   time.Duration

	model whisper.Model
	ready bool
}

// New creates a new Transcriber. scriptPath is ignored on arm64 (no Python needed).
func New(scriptPath, model, modelsDir string, timeout time.Duration) (*Transcriber, error) {
	if model == "" {
		model = "small-q5_1"
	}
	if modelsDir == "" {
		modelsDir = "/home/node/data/models"
	}

	return &Transcriber{
		modelName: model,
		modelsDir: modelsDir,
		timeout:   timeout,
	}, nil
}

// Start downloads the model if needed and loads it into memory.
func (t *Transcriber) Start() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.ready {
		return nil
	}

	modelPath, err := downloadModel(t.modelName, t.modelsDir)
	if err != nil {
		return fmt.Errorf("download model: %w", err)
	}

	model, err := whisper.New(modelPath)
	if err != nil {
		return fmt.Errorf("load whisper model: %w", err)
	}

	t.model = model
	t.ready = true
	log.Printf("voice: whisper.cpp model ready (model=%s)", t.modelName)
	return nil
}

// Stop releases the whisper model from memory.
func (t *Transcriber) Stop() {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.model != nil {
		t.model.Close()
		t.model = nil
		t.ready = false
		log.Println("voice: whisper.cpp model unloaded")
	}
}

// IsReady returns true if the model is loaded and ready.
func (t *Transcriber) IsReady() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.ready
}

// Transcribe converts an audio file to text using the in-process whisper model.
func (t *Transcriber) Transcribe(audioPath string) (*TranscribeResult, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.ready {
		return nil, fmt.Errorf("transcriber not ready")
	}

	start := time.Now()

	wavPath, err := convertToWAV(audioPath)
	if err != nil {
		return nil, fmt.Errorf("convert audio: %w", err)
	}
	defer os.Remove(wavPath)

	samples, err := readWAVSamples(wavPath)
	if err != nil {
		return nil, fmt.Errorf("read wav: %w", err)
	}
	log.Printf("voice: %d samples (%.1fs at 16kHz)", len(samples), float64(len(samples))/16000)

	ctx, err := t.model.NewContext()
	if err != nil {
		return nil, fmt.Errorf("create whisper context: %w", err)
	}
	if err := ctx.SetLanguage("auto"); err != nil {
		return nil, fmt.Errorf("set language: %w", err)
	}
	threads := uint(runtime.NumCPU())
	if threads > 4 {
		threads = 4
	}
	ctx.SetThreads(threads)
	log.Printf("voice: processing %d samples with whisper.cpp (threads=%d)", len(samples), threads)

	type processResult struct {
		err error
	}
	ch := make(chan processResult, 1)
	go func() {
		ch <- processResult{ctx.Process(samples, nil, nil, nil)}
	}()

	select {
	case res := <-ch:
		if res.err != nil {
			return nil, fmt.Errorf("whisper process: %w", res.err)
		}
	case <-time.After(t.timeout):
		return nil, fmt.Errorf("transcription timeout after %v", t.timeout)
	}

	var texts []string
	for {
		segment, err := ctx.NextSegment()
		if err != nil {
			break
		}
		text := strings.TrimSpace(segment.Text)
		if text != "" {
			texts = append(texts, text)
		}
	}

	fullText := strings.Join(texts, " ")
	if fullText == "" {
		return nil, fmt.Errorf("empty transcription")
	}

	elapsed := time.Since(start).Seconds()
	lang := ctx.DetectedLanguage()

	return &TranscribeResult{
		Text:      fullText,
		DurationS: elapsed,
		Language:  lang,
	}, nil
}

// DownloadAndTranscribe downloads a voice file from Telegram, transcribes it, cleans up.
func (t *Transcriber) DownloadAndTranscribe(client *http.Client, botToken, fileID string) (*TranscribeResult, error) {
	return downloadAndTranscribe(t.Transcribe, client, botToken, fileID)
}

// IsAvailable checks if ffmpeg exists (required for audio conversion on arm64).
// scriptPath is ignored on arm64.
func IsAvailable(scriptPath string) bool {
	_, err := exec.LookPath("ffmpeg")
	return err == nil
}
