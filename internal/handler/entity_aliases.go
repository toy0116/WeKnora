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
}

type entityAliasPayload struct {
	Groups []entityAliasGroup `json:"groups" yaml:"groups"`
}

// GetEntityAliases returns the current alias groups.
// GET /api/v1/system/entity-aliases
func (h *EntityAliasHandler) GetEntityAliases(c *gin.Context) {
	h.mu.Lock()
	defer h.mu.Unlock()

	groups := h.currentGroups()
	c.JSON(http.StatusOK, gin.H{"ok": 1, "data": gin.H{"groups": groups}})
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
			clean = append(clean, entityAliasGroup{Forms: forms, Products: products})
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

// currentGroups returns the groups from in-memory config (already loaded at startup).
// Both Forms AND Products are surfaced so the Web UI can edit them round-trip
// without dropping data on save.
func (h *EntityAliasHandler) currentGroups() []entityAliasGroup {
	if h.cfg.EntityAliases == nil {
		return []entityAliasGroup{}
	}
	out := make([]entityAliasGroup, 0, len(h.cfg.EntityAliases.Groups))
	for _, g := range h.cfg.EntityAliases.Groups {
		out = append(out, entityAliasGroup{
			Forms:    g.Forms,
			Products: g.Products,
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

// reload updates the in-memory EntityAliasConfig from the given payload.
// Carries both Forms AND Products into the new config so entity-mismatch
// detection and attribution-conflict checks keep working after a save.
func (h *EntityAliasHandler) reload(payload entityAliasPayload) {
	groups := make([]config.EntityAliasGroup, 0, len(payload.Groups))
	for _, g := range payload.Groups {
		groups = append(groups, config.EntityAliasGroup{
			Forms:    g.Forms,
			Products: g.Products,
		})
	}
	newCfg := &config.EntityAliasConfig{Groups: groups}
	newCfg.Build()
	h.cfg.EntityAliases = newCfg
}
