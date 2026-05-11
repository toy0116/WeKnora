package handler

import (
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/gin-gonic/gin"
	"gopkg.in/yaml.v3"
)

// EntityAliasHandler manages CRUD for the entity alias dictionary
// (config/entity_aliases.yaml). Changes are persisted to disk and the
// in-memory index is hot-reloaded so the search pipeline sees them immediately
// without a restart.
type EntityAliasHandler struct {
	cfg *config.Config
	mu  sync.Mutex // serialises concurrent write requests
}

// NewEntityAliasHandler creates a handler that reads/writes entity_aliases.yaml
// next to the main config file.
func NewEntityAliasHandler(cfg *config.Config) *EntityAliasHandler {
	return &EntityAliasHandler{cfg: cfg}
}

type entityAliasGroup struct {
	// Forms — cross-lingual / cross-spelling aliases that participate in
	// BM25/vector query expansion (e.g. "鲁邦通" ↔ "Robustel").
	Forms []string `json:"forms" yaml:"forms"`
	// Products — model numbers / SKUs that belong to this entity. They do
	// NOT enter query expansion (would corrupt retrieval) but are used by
	// the entity-mismatch and attribution-conflict detectors. See
	// internal/config/config.go EntityAliasGroup docstring for the full
	// rationale. omitempty so groups without products serialize cleanly.
	Products []string `json:"products,omitempty" yaml:"products,omitempty"`
	// Kind — classifies the group as "brand" (default) or "technology". Only
	// brand groups serve as anchors for entity-mismatch detection. omitempty
	// so brand groups (the common case) stay unchanged in yaml.
	Kind string `json:"kind,omitempty" yaml:"kind,omitempty"`
}

type entityAliasPayload struct {
	Groups []entityAliasGroup `json:"groups" yaml:"groups"`
}

// GetEntityAliases returns the current alias groups split into two views:
//   - groups          — yaml-declared, UI-editable.
//   - auto_discovered — wiki auto-discovered brand candidates (read-only,
//                       ignorable via /ignore).
// GET /api/v1/system/entity-aliases
func (h *EntityAliasHandler) GetEntityAliases(c *gin.Context) {
	h.mu.Lock()
	defer h.mu.Unlock()

	groups := h.currentGroups()
	autoDiscovered := h.currentAutoDiscovered()
	c.JSON(http.StatusOK, gin.H{
		"ok": 1,
		"data": gin.H{
			"groups":          groups,
			"auto_discovered": autoDiscovered,
		},
	})
}

// IgnoreAutoDiscoveredRequest carries the wiki slug the user clicked
// "ignore" on in the Web UI. After persisting to the denylist yaml the
// handler re-runs Build so the corresponding wiki-auto group disappears
// from runtimeGroups immediately.
type IgnoreAutoDiscoveredRequest struct {
	Slug string `json:"slug" binding:"required"`
}

// IgnoreAutoDiscovered appends a wiki entity slug to the auto-discovery
// denylist and re-applies the alias config so the group vanishes.
// POST /api/v1/system/entity-aliases/ignore
func (h *EntityAliasHandler) IgnoreAutoDiscovered(c *gin.Context) {
	h.mu.Lock()
	defer h.mu.Unlock()

	var req IgnoreAutoDiscoveredRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": 0, "error": err.Error()})
		return
	}
	if h.cfg.AliasDenylist == nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok": 0, "error": "alias denylist not configured on server",
		})
		return
	}
	if err := h.cfg.AliasDenylist.Append(req.Slug); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": 0, "error": err.Error()})
		return
	}
	// Rebuild so the wiki-auto group vanishes from runtimeGroups. Build
	// calls RuntimeRefresh which re-runs AutoDiscoverFromWiki — and the
	// new denylist entry now filters this slug out.
	if h.cfg.EntityAliases != nil {
		h.cfg.EntityAliases.Build()
	}
	c.JSON(http.StatusOK, gin.H{
		"ok":   1,
		"data": gin.H{"ignored_slug": req.Slug},
	})
}

// UpdateEntityAliases replaces all alias groups, persists to disk, and
// hot-reloads the in-memory index.
// PUT /api/v1/system/entity-aliases
func (h *EntityAliasHandler) UpdateEntityAliases(c *gin.Context) {
	h.mu.Lock()
	defer h.mu.Unlock()

	var payload entityAliasPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": 0, "error": err.Error()})
		return
	}

	// Normalise: remove empty groups / empty forms / empty products. A group
	// is kept if it has at least one form OR one product (product-only groups
	// are useful for "we know these model numbers exist but haven't decided
	// on a canonical brand name yet"; entity-mismatch detection still works
	// on them via the product list).
	clean := make([]entityAliasGroup, 0, len(payload.Groups))
	for _, g := range payload.Groups {
		var forms []string
		for _, f := range g.Forms {
			if f != "" {
				forms = append(forms, f)
			}
		}
		var products []string
		for _, p := range g.Products {
			if p != "" {
				products = append(products, p)
			}
		}
		if len(forms) >= 1 || len(products) >= 1 {
			clean = append(clean, entityAliasGroup{
				Forms:    forms,
				Products: products,
				Kind:     g.Kind,
			})
		}
	}
	payload.Groups = clean

	// Persist to disk
	if err := h.persist(payload); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": 0, "error": err.Error()})
		return
	}

	// Hot-reload in-memory index
	h.reload(payload)

	c.JSON(http.StatusOK, gin.H{"ok": 1, "data": gin.H{"groups": payload.Groups}})
}

// currentGroups returns the YAML-declared groups (UI-editable surface).
// Wiki-auto-discovered groups are returned separately by currentAutoDiscovered.
func (h *EntityAliasHandler) currentGroups() []entityAliasGroup {
	if h.cfg.EntityAliases == nil {
		return []entityAliasGroup{}
	}
	out := make([]entityAliasGroup, 0, len(h.cfg.EntityAliases.Groups))
	for _, g := range h.cfg.EntityAliases.Groups {
		out = append(out, entityAliasGroup{
			Forms:    g.Forms,
			Products: g.Products,
			Kind:     g.Kind,
		})
	}
	return out
}

// autoDiscoveredGroup is the read-only payload shape for wiki-auto groups.
// The Source + WikiSlug fields are required so the UI can render the ❌
// Ignore button and route the click back to the slug for persistence.
type autoDiscoveredGroup struct {
	Forms    []string `json:"forms"`
	Products []string `json:"products,omitempty"`
	Kind     string   `json:"kind,omitempty"`
	Source   string   `json:"source"`    // always "wiki-auto" in this slice
	WikiSlug string   `json:"wiki_slug"` // originating wiki slug, e.g. "entity/teltonika"
}

// currentAutoDiscovered returns the subset of runtimeGroups marked
// Source="wiki-auto" — i.e. brand candidates that came from the
// inDegree heuristic, not from yaml.
func (h *EntityAliasHandler) currentAutoDiscovered() []autoDiscoveredGroup {
	if h.cfg.EntityAliases == nil {
		return []autoDiscoveredGroup{}
	}
	rt := h.cfg.EntityAliases.RuntimeGroups()
	out := make([]autoDiscoveredGroup, 0)
	for _, g := range rt {
		if g.Source != "wiki-auto" {
			continue
		}
		out = append(out, autoDiscoveredGroup{
			Forms:    g.Forms,
			Products: g.Products,
			Kind:     g.Kind,
			Source:   g.Source,
			WikiSlug: g.WikiSlug,
		})
	}
	return out
}

// persist writes the payload to entity_aliases.yaml in the config directory.
// A schema-doc header is prepended on every save so that round-tripping
// through the Web UI doesn't strip the file's documentation (yaml.Marshal
// loses comments by design — the only way to keep them is to write them
// back explicitly).
func (h *EntityAliasHandler) persist(payload entityAliasPayload) error {
	if h.cfg.ConfigDir == "" {
		return nil // no config dir — skip persistence (e.g. tests)
	}
	path := filepath.Join(h.cfg.ConfigDir, "entity_aliases.yaml")
	body, err := yaml.Marshal(payload)
	if err != nil {
		return err
	}
	var out []byte
	out = append(out, entityAliasYAMLHeader...)
	out = append(out, body...)
	return os.WriteFile(path, out, 0o644)
}

// entityAliasYAMLHeader documents the file schema so that users editing the
// file by hand (or skimming it after a Web-UI save) immediately see what
// each field does. Kept in sync with the EntityAliasGroup docstring in
// internal/config/config.go.
const entityAliasYAMLHeader = `# Entity alias dictionary for cross-lingual knowledge base retrieval AND
# product-attribution conflict detection.
#
# Two fields per group:
#   - forms:    cross-lingual / cross-spelling aliases for the SAME entity
#               (e.g. "鲁邦通" ↔ "Robustel"). Forms expand into BM25/vector
#               queries so retrieval finds documents regardless of which form
#               the user typed.
#   - products: model numbers / SKUs that belong to this entity (e.g.
#               "EG5120" belongs to Robustel). Products are NOT expanded into
#               queries (that would corrupt retrieval); they only participate
#               in entity-detection and attribution-conflict checks.
#
# When a query says "Brand X's Product Y" but Y is registered under Brand Z,
# the pipeline emits an <entity_warning> block to the LLM so it can refuse
# to fabricate specs.
#
# This header is regenerated automatically every time the file is saved via
# the Web UI (Settings → Entity Aliases). Hand-edits to the comments below
# will be lost on the next save — edit only the data, or update the header
# in internal/handler/entity_aliases.go.

`

// reload updates the yaml-on-disk Groups in place and triggers Build, which
// re-applies any wiki augmentation via RuntimeRefresh. The config instance
// pointer is preserved so the RuntimeRefresh closure installed by main.go
// keeps working — replacing h.cfg.EntityAliases wholesale (the previous
// behaviour) would drop the closure and silently disable wiki augmentation
// after the first save.
func (h *EntityAliasHandler) reload(payload entityAliasPayload) {
	groups := make([]config.EntityAliasGroup, 0, len(payload.Groups))
	for _, g := range payload.Groups {
		groups = append(groups, config.EntityAliasGroup{
			Forms:    g.Forms,
			Products: g.Products,
			Kind:     g.Kind,
		})
	}
	if h.cfg.EntityAliases == nil {
		h.cfg.EntityAliases = &config.EntityAliasConfig{Groups: groups}
	} else {
		h.cfg.EntityAliases.Groups = groups
	}
	// Build runs RuntimeRefresh internally so wiki-merged forms/products
	// are reapplied to runtimeGroups before the index is rebuilt.
	h.cfg.EntityAliases.Build()
}
