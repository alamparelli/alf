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
		methodNotAllowed(w)
		return
	}

	var req ReactRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.MsgID == "" || req.Emoji == "" {
		respondError(w, http.StatusBadRequest, "msg_id and emoji required")
		return
	}

	result, err := h.Service.React(req)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, result)
}
