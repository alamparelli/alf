package controlcenter

import (
	"embed"
	"encoding/json"
	"net/http"
	"strings"
)

//go:embed docs/*.md
var docsFS embed.FS

// DocsFS returns the embedded docs filesystem for external use (e.g. llms.txt generation).
func DocsFS() embed.FS { return docsFS }

type docMeta struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Summary  string   `json:"summary"`
	Category string   `json:"category,omitempty"`
	Tags     []string `json:"tags,omitempty"`
	Order    int      `json:"order"`
}

type docFull struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Category string   `json:"category,omitempty"`
	Tags     []string `json:"tags,omitempty"`
	Content  string   `json:"content"`
}

// DocsHandler serves embedded markdown documentation.
type DocsHandler struct{}

func (h *DocsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/api/docs/")
	id = strings.TrimSuffix(id, "/")

	if id == "" {
		h.serveList(w)
		return
	}

	h.serveDoc(w, id)
}

func (h *DocsHandler) serveList(w http.ResponseWriter) {
	entries, err := docsFS.ReadDir("docs")
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	var docs []docMeta
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".md")
		data, err := docsFS.ReadFile("docs/" + e.Name())
		if err != nil {
			continue
		}
		info := parseDocInfo(string(data))
		docs = append(docs, docMeta{
			ID:       id,
			Title:    info.title,
			Summary:  info.summary,
			Category: info.category,
			Tags:     info.tags,
			Order:    info.order,
		})
	}

	// Sort by order, then alphabetically.
	for i := 0; i < len(docs); i++ {
		for j := i + 1; j < len(docs); j++ {
			if docs[j].Order < docs[i].Order || (docs[j].Order == docs[i].Order && docs[j].Title < docs[i].Title) {
				docs[i], docs[j] = docs[j], docs[i]
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(docs)
}

func (h *DocsHandler) serveDoc(w http.ResponseWriter, id string) {
	// Sanitize: only allow alphanumeric and hyphens.
	for _, c := range id {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-') {
			http.NotFound(w, nil)
			return
		}
	}

	data, err := docsFS.ReadFile("docs/" + id + ".md")
	if err != nil {
		http.NotFound(w, nil)
		return
	}

	info := parseDocInfo(string(data))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(docFull{
		ID:       id,
		Title:    info.title,
		Category: info.category,
		Tags:     info.tags,
		Content:  info.body,
	})
}

type docInfo struct {
	title    string
	summary  string
	category string
	tags     []string
	order    int
	body     string // content with frontmatter stripped
}

// parseDocInfo extracts frontmatter (category, tags, order) and header info.
// Frontmatter is delimited by --- lines at the top of the file.
func parseDocInfo(content string) docInfo {
	var info docInfo
	info.body = content

	// Parse frontmatter if present.
	if strings.HasPrefix(content, "---\n") {
		end := strings.Index(content[4:], "\n---")
		if end >= 0 {
			fm := content[4 : 4+end]
			info.body = strings.TrimSpace(content[4+end+4:])
			for _, line := range strings.Split(fm, "\n") {
				line = strings.TrimSpace(line)
				if k, v, ok := strings.Cut(line, ":"); ok {
					k = strings.TrimSpace(k)
					v = strings.TrimSpace(v)
					switch k {
					case "category":
						info.category = v
					case "tags":
						for _, t := range strings.Split(v, ",") {
							t = strings.TrimSpace(t)
							if t != "" {
								info.tags = append(info.tags, t)
							}
						}
					case "order":
						for _, c := range v {
							if c >= '0' && c <= '9' {
								info.order = info.order*10 + int(c-'0')
							}
						}
					}
				}
			}
		}
	}

	// Parse title and summary from markdown body.
	lines := strings.Split(info.body, "\n")
	foundTitle := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !foundTitle {
			if strings.HasPrefix(trimmed, "# ") {
				info.title = strings.TrimPrefix(trimmed, "# ")
				foundTitle = true
			}
			continue
		}
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			break
		}
		info.summary = trimmed
		if len(info.summary) > 160 {
			info.summary = info.summary[:157] + "..."
		}
		break
	}
	if info.title == "" {
		info.title = "Untitled"
	}
	return info
}
