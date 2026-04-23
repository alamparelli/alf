package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/alamparelli/alf/internal/memory/embed"
)

const (
	maxBodySize     = 1 << 20 // 1 MB
	maxBatchSize    = 100
	maxInstances    = 50
)

type server struct {
	embedder *embed.Embedder
	secret   string
	tokens   sync.Map // instance_id → token
	dims     int
}

func main() {
	modelDir := flag.String("model-dir", "/opt/alf/models/multilingual-e5-small", "Path to ONNX model directory")
	addr := flag.String("addr", ":8090", "Listen address")
	flag.Parse()

	secret := readSecret("EMBED_SHARED_SECRET")
	if secret == "" {
		log.Fatal("EMBED_SHARED_SECRET or EMBED_SHARED_SECRET_FILE is required")
	}

	embedder, err := embed.NewEmbedder(*modelDir)
	if err != nil {
		log.Fatalf("embedder init: %v", err)
	}
	if err := embedder.Start(); err != nil {
		log.Fatalf("embedder start: %v", err)
	}
	defer embedder.Stop()

	srv := &server{
		embedder: embedder,
		secret:   secret,
		dims:     embedder.Dims(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/register", srv.handleRegister)
	mux.HandleFunc("/embed", srv.requireAuth(srv.handleEmbed))
	mux.HandleFunc("/embed-query", srv.requireAuth(srv.handleEmbedQuery))
	mux.HandleFunc("/embed-batch", srv.requireAuth(srv.handleEmbedBatch))
	mux.HandleFunc("/health", srv.handleHealth)

	log.Printf("embed-server: listening on %s (dims=%d)", *addr, srv.dims)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatalf("listen: %v", err)
	}
}

func (s *server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		InstanceID string `json:"instance_id"`
		Secret     string `json:"secret"`
	}
	if err := decodeJSON(r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if subtle.ConstantTimeCompare([]byte(req.Secret), []byte(s.secret)) != 1 {
		http.Error(w, "invalid secret", http.StatusUnauthorized)
		return
	}
	if req.InstanceID == "" {
		http.Error(w, "instance_id required", http.StatusBadRequest)
		return
	}

	// Check instance cap (re-registration of existing ID is always allowed).
	if _, exists := s.tokens.Load(req.InstanceID); !exists {
		count := 0
		s.tokens.Range(func(_, _ any) bool { count++; return true })
		if count >= maxInstances {
			http.Error(w, "max instances reached", http.StatusTooManyRequests)
			return
		}
	}

	token := generateToken()
	s.tokens.Store(req.InstanceID, token)

	log.Printf("embed-server: registered instance %s", req.InstanceID)
	writeJSON(w, map[string]any{
		"token": token,
		"dims":  s.dims,
	})
}

func (s *server) handleEmbed(w http.ResponseWriter, r *http.Request) {
	if s.embedder == nil || !s.embedder.IsReady() {
		http.Error(w, "model not ready", http.StatusServiceUnavailable)
		return
	}

	var req struct {
		Text string `json:"text"`
	}
	if err := decodeJSON(r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	vec, err := s.embedder.Embed(req.Text)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]any{"embedding": vec})
}

func (s *server) handleEmbedQuery(w http.ResponseWriter, r *http.Request) {
	if s.embedder == nil || !s.embedder.IsReady() {
		http.Error(w, "model not ready", http.StatusServiceUnavailable)
		return
	}

	var req struct {
		Text string `json:"text"`
	}
	if err := decodeJSON(r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	vec, err := s.embedder.EmbedQuery(req.Text)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]any{"embedding": vec})
}

func (s *server) handleEmbedBatch(w http.ResponseWriter, r *http.Request) {
	if s.embedder == nil || !s.embedder.IsReady() {
		http.Error(w, "model not ready", http.StatusServiceUnavailable)
		return
	}

	var req struct {
		Texts []string `json:"texts"`
	}
	if err := decodeJSON(r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if len(req.Texts) == 0 {
		writeJSON(w, map[string]any{"embeddings": [][]float32{}})
		return
	}
	if len(req.Texts) > maxBatchSize {
		http.Error(w, fmt.Sprintf("batch size %d exceeds max %d", len(req.Texts), maxBatchSize), http.StatusBadRequest)
		return
	}

	vecs, err := s.embedder.EmbedBatch(req.Texts)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]any{"embeddings": vecs})
}

func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ready := s.embedder != nil && s.embedder.IsReady()
	status := "ok"
	if !ready {
		status = "loading"
	}
	writeJSON(w, map[string]any{
		"status": status,
		"dims":   s.dims,
		"model":  "multilingual-e5-small",
	})
}

// requireAuth wraps a handler with bearer token validation.
func (s *server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			http.Error(w, "missing authorization", http.StatusUnauthorized)
			return
		}
		token := strings.TrimPrefix(auth, "Bearer ")

		valid := false
		s.tokens.Range(func(_, v any) bool {
			if subtle.ConstantTimeCompare([]byte(v.(string)), []byte(token)) == 1 {
				valid = true
			}
			return true // always iterate all entries to prevent timing leak
		})

		if !valid {
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}

		next(w, r)
	}
}

func decodeJSON(r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, maxBodySize)
	return json.NewDecoder(r.Body).Decode(v)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func generateToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func readSecret(envName string) string {
	// Check file-based secret first (Docker secret pattern).
	if filePath := os.Getenv(envName + "_FILE"); filePath != "" {
		data, err := os.ReadFile(filePath)
		if err == nil {
			return strings.TrimSpace(string(data))
		}
	}
	return strings.TrimSpace(os.Getenv(envName))
}
