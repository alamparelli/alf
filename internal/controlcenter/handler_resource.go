package controlcenter

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// ResourceHandler handles CRUD for a resource type (context, tools, skills).
// Routes:
//
//	GET  /api/{type}/        → list
//	GET  /api/{type}/{name}  → get
//	PUT  /api/{type}/{name}  → upsert
//	DELETE /api/{type}/{name} → delete
type ResourceHandler struct {
	Store       ResourceStore
	Notifier    Notifier       // optional: notify daemon on change
	Event       ReloadEvent    // which event to fire
	EventBroker *EventBroker
}

func (h *ResourceHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Extract resource name from path suffix.
	// The handler is registered at e.g. "/api/context/" so
	// the remaining path after the prefix is the resource name.
	name := ""
	if i := strings.LastIndex(r.URL.Path, "/"); i >= 0 {
		name = r.URL.Path[i+1:]
	}

	switch r.Method {
	case http.MethodGet:
		if name == "" {
			h.list(w)
		} else {
			h.get(w, name)
		}
	case http.MethodPut:
		if name == "" {
			http.Error(w, `{"error":"resource name required"}`, http.StatusBadRequest)
			return
		}
		h.put(w, r, name)
	case http.MethodDelete:
		if name == "" {
			http.Error(w, `{"error":"resource name required"}`, http.StatusBadRequest)
			return
		}
		h.del(w, name)
	default:
		methodNotAllowed(w)
	}
}

func (h *ResourceHandler) list(w http.ResponseWriter) {
	items, err := h.Store.List()
	if err != nil {
		http.Error(w, jsonErr(err.Error()), http.StatusInternalServerError)
		return
	}
	if items == nil {
		items = []ResourceMeta{}
	}
	data, _ := json.Marshal(map[string]any{"items": items})
	w.Write(data)
}

func (h *ResourceHandler) get(w http.ResponseWriter, name string) {
	content, err := h.Store.Get(name)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			http.Error(w, jsonErr(err.Error()), http.StatusNotFound)
		} else if strings.Contains(err.Error(), "invalid resource name") {
			http.Error(w, jsonErr(err.Error()), http.StatusBadRequest)
		} else {
			http.Error(w, jsonErr(err.Error()), http.StatusInternalServerError)
		}
		return
	}
	data, _ := json.Marshal(map[string]any{"name": name, "content": string(content)})
	w.Write(data)
}

func (h *ResourceHandler) put(w http.ResponseWriter, r *http.Request, name string) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxResourceSize+1024))
	if err != nil {
		http.Error(w, jsonErr("failed to read body"), http.StatusBadRequest)
		return
	}

	var payload struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, jsonErr("invalid JSON: "+err.Error()), http.StatusBadRequest)
		return
	}

	if err := h.Store.Put(name, []byte(payload.Content)); err != nil {
		if strings.Contains(err.Error(), "invalid resource name") {
			http.Error(w, jsonErr(err.Error()), http.StatusBadRequest)
		} else if strings.Contains(err.Error(), "too large") {
			http.Error(w, jsonErr(err.Error()), http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, jsonErr(err.Error()), http.StatusInternalServerError)
		}
		return
	}

	if h.Notifier != nil {
		h.Notifier.Notify(h.Event)
	}
	h.emitEvent()

	w.Write([]byte(`{"ok":true}`))
}

func (h *ResourceHandler) del(w http.ResponseWriter, name string) {
	if err := h.Store.Delete(name); err != nil {
		if strings.Contains(err.Error(), "not found") {
			http.Error(w, jsonErr(err.Error()), http.StatusNotFound)
		} else if strings.Contains(err.Error(), "invalid resource name") {
			http.Error(w, jsonErr(err.Error()), http.StatusBadRequest)
		} else {
			http.Error(w, jsonErr(err.Error()), http.StatusInternalServerError)
		}
		return
	}

	if h.Notifier != nil {
		h.Notifier.Notify(h.Event)
	}
	h.emitEvent()

	w.Write([]byte(`{"ok":true}`))
}

// emitEvent maps the ReloadEvent to the corresponding EventType for SSE.
func (h *ResourceHandler) emitEvent() {
	if h.EventBroker == nil {
		return
	}
	switch h.Event {
	case ReloadTools:
		h.EventBroker.Emit(EventTools)
	case ReloadSkills:
		h.EventBroker.Emit(EventSkills)
	}
}
