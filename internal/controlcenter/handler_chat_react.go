package controlcenter

import (
	"encoding/json"
	"net/http"
)

// ChatReactHandler handles POST /api/chat/react.
type ChatReactHandler struct {
	Service *ChatService
}

func (h *ChatReactHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	var req ReactRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	if req.MsgID == "" || req.Emoji == "" {
		http.Error(w, `{"error":"msg_id and emoji required"}`, http.StatusBadRequest)
		return
	}

	result, err := h.Service.React(req)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}
