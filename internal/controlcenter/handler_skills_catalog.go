package controlcenter

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/alamparelli/alf/internal/skills"
)

// SkillsCatalogEntry represents a skill in the catalog API response.
type SkillsCatalogEntry struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Version     string   `json:"version,omitempty"`
	Triggers    []string `json:"triggers,omitempty"`
	Tier        string   `json:"tier,omitempty"`
	Source      string   `json:"source"` // "system" or "user"
	Dir         string   `json:"dir"`    // relative directory path
}

// SkillsCatalogHandler handles GET /api/skills/catalog — lists all skills from the runtime store.
type SkillsCatalogHandler struct {
	SkillStore skills.Store
	SkillsDir  string // system skills directory (/opt/alf/skills.d)
	DataDir    string // data directory (/home/alf/data)
}

func (h *SkillsCatalogHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	all := h.SkillStore.All()
	entries := make([]SkillsCatalogEntry, 0, len(all))

	// User skills are ONLY in dataDir/skills (imported/created by user).
	// Everything else (skillsDir, dataDir/skills.d which symlinks to skillsDir) is system.
	userSkillsDir := filepath.Join(h.DataDir, "skills") + "/"

	for _, sk := range all {
		source := "system"
		if strings.HasPrefix(sk.Dir+"/", userSkillsDir) {
			source = "user"
		}

		// Make dir relative for display
		relDir := sk.Dir
		if strings.HasPrefix(relDir, h.DataDir+"/") {
			relDir = strings.TrimPrefix(relDir, h.DataDir+"/")
		} else if strings.HasPrefix(relDir, h.SkillsDir+"/") {
			relDir = "skills.d/" + filepath.Base(relDir)
		} else {
			relDir = filepath.Base(relDir)
		}

		entries = append(entries, SkillsCatalogEntry{
			Name:        sk.Name,
			Description: sk.Description,
			Version:     sk.Version,
			Triggers:    sk.Triggers,
			Tier:        sk.Tier,
			Source:      source,
			Dir:         relDir,
		})
	}

	data, _ := json.Marshal(map[string]any{"skills": entries})
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}
