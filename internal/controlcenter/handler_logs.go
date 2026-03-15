package controlcenter

import (
	"encoding/json"
	"net/http"
	"strconv"
)

// LogsHandler handles GET /api/logs.
type LogsHandler struct {
	Reader LogReader
}

func (h *LogsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	name := r.URL.Query().Get("name")
	if name == "" {
		data, _ := json.MarshalIndent(map[string]any{
			"available": h.Reader.Available(),
		}, "", "  ")
		w.Write(data)
		return
	}

	n := 200
	if nStr := r.URL.Query().Get("n"); nStr != "" {
		if parsed, err := strconv.Atoi(nStr); err == nil {
			n = parsed
		}
	}
	if n < 1 {
		n = 1
	}
	if n > 1000 {
		n = 1000
	}

	lines, err := h.Reader.Tail(name, n)
	if err != nil {
		http.Error(w, jsonErr(err.Error()), http.StatusBadRequest)
		return
	}

	data, _ := json.MarshalIndent(map[string]any{
		"name":  name,
		"lines": lines,
		"count": len(lines),
	}, "", "  ")
	w.Write(data)
}
