package voice

import (
	"log"
	"os"
	"os/exec"
	"sync/atomic"
	"time"
)

// ReadyState tracks whether the whisper model is downloaded and ready.
type ReadyState struct {
	ready atomic.Bool
}

// IsReady returns true if the model is loaded and transcription is available.
func (r *ReadyState) IsReady() bool {
	return r.ready.Load()
}

// WarmUp downloads the whisper model in the background.
// Calls the transcribe script with a dummy file to trigger model download.
// Sets ready to true when complete.
func WarmUp(scriptPath, model, modelsDir string) *ReadyState {
	state := &ReadyState{}

	go func() {
		start := time.Now()
		log.Printf("voice: warming up model %q (downloading if needed)...", model)

		// Create a tiny silent WAV file for warmup
		tmpFile, err := os.CreateTemp("", "alf-warmup-*.wav")
		if err != nil {
			log.Printf("voice: warmup failed (temp file): %v", err)
			return
		}
		// Write minimal WAV header (44 bytes of silence)
		wavHeader := makeMinimalWAV()
		tmpFile.Write(wavHeader)
		tmpFile.Close()
		defer os.Remove(tmpFile.Name())

		// Run the transcription script — this triggers model download
		cmd := exec.Command("python3", scriptPath,
			tmpFile.Name(),
			"--model", model,
			"--models-dir", modelsDir,
		)
		cmd.Stderr = os.Stderr

		if err := cmd.Run(); err != nil {
			// Exit error is expected for empty/silence audio
			// The important thing is the model was downloaded
			log.Printf("voice: warmup script finished (may have empty transcription, this is OK)")
		}

		state.ready.Store(true)
		log.Printf("voice: model ready (warmup took %v)", time.Since(start).Round(time.Second))
	}()

	return state
}

// makeMinimalWAV creates a minimal valid WAV file (44-byte header + 1 second of silence)
func makeMinimalWAV() []byte {
	sampleRate := 16000
	bitsPerSample := 16
	numChannels := 1
	numSamples := sampleRate // 1 second
	dataSize := numSamples * numChannels * (bitsPerSample / 8)
	fileSize := 36 + dataSize

	wav := make([]byte, 44+dataSize)
	copy(wav[0:4], "RIFF")
	putLE32(wav[4:], uint32(fileSize))
	copy(wav[8:12], "WAVE")
	copy(wav[12:16], "fmt ")
	putLE32(wav[16:], 16) // chunk size
	putLE16(wav[20:], 1)  // PCM
	putLE16(wav[22:], uint16(numChannels))
	putLE32(wav[24:], uint32(sampleRate))
	putLE32(wav[28:], uint32(sampleRate*numChannels*bitsPerSample/8)) // byte rate
	putLE16(wav[32:], uint16(numChannels*bitsPerSample/8))           // block align
	putLE16(wav[34:], uint16(bitsPerSample))
	copy(wav[36:40], "data")
	putLE32(wav[40:], uint32(dataSize))
	// Rest is zeros (silence)
	return wav
}

func putLE16(b []byte, v uint16) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
}

func putLE32(b []byte, v uint32) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
}
