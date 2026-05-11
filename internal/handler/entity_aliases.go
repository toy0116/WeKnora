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
	Forms []string `json:"forms" yaml:"forms"`
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

	// Normalise: remove empty groups / empty forms
	clean := make([]entityAliasGroup, 0, len(payload.Groups))
	for _, g := range payload.Groups {
		var forms []string
		for _, f := range g.Forms {
			if f != "" {
				forms = append(forms, f)
			}
		}
		if len(forms) >= 1 {
			clean = append(clean, entityAliasGroup{Forms: forms})
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
func (h *EntityAliasHandler) currentGroups() []entityAliasGroup {
	if h.cfg.EntityAliases == nil {
		return []entityAliasGroup{}
	}
	out := make([]entityAliasGroup, 0, len(h.cfg.EntityAliases.Groups))
	for _, g := range h.cfg.EntityAliases.Groups {
		out = append(out, entityAliasGroup{Forms: g.Forms})
	}
	return out
}

// persist writes the payload to entity_aliases.yaml in the config directory.
func (h *EntityAliasHandler) persist(payload entityAliasPayload) error {
	if h.cfg.ConfigDir == "" {
		return nil // no config dir — skip persistence (e.g. tests)
	}
	path := filepath.Join(h.cfg.ConfigDir, "entity_aliases.yaml")
	data, err := yaml.Marshal(payload)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// reload updates the in-memory EntityAliasConfig from the given payload.
func (h *EntityAliasHandler) reload(payload entityAliasPayload) {
	groups := make([]config.EntityAliasGroup, 0, len(payload.Groups))
	for _, g := range payload.Groups {
		groups = append(groups, config.EntityAliasGroup{Forms: g.Forms})
	}
	newCfg := &config.EntityAliasConfig{Groups: groups}
	newCfg.Build()
	h.cfg.EntityAliases = newCfg
}
