// Package config — denylist for the wiki brand auto-discovery feature.
//
// When the entity-alias pipeline does its second pass over wiki entity pages
// (AutoDiscoverFromWiki), it identifies brand candidates via inDegree from
// product-shaped pages. Some of those candidates are legitimate competitor
// brands; others are cloud integrations, SoC vendors, or software stacks
// that happen to be referenced by many products without being competitors
// (e.g. entity/aws-iot-core gets pointed at by every Robustel product with
// cloud connectivity).
//
// This file loads a per-slug denylist (config/entity_alias_denylist.yaml)
// that callers can use to suppress specific wiki entities from being
// auto-promoted. The Web UI exposes an ❌ Ignore button on each auto-
// discovered group that appends to this file via the API.

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
)

// AliasDenylistConfig is loaded from config/entity_alias_denylist.yaml and
// answers "is wiki slug X excluded from auto-discovery?".
type AliasDenylistConfig struct {
	// IgnoredSlugs is the raw list from yaml. Slug values are matched
	// case-sensitively as full strings (wiki slugs are canonicalised
	// lower-case anyway).
	IgnoredSlugs []string `yaml:"ignored_slugs"`

	// path is where this file was loaded from; used by Append so the
	// Web-UI ignore action can persist changes.
	path string
	mu   sync.RWMutex
	set  map[string]struct{}
}

// IsIgnored reports whether the given wiki slug appears in the denylist.
// Safe for concurrent reads.
func (c *AliasDenylistConfig) IsIgnored(slug string) bool {
	if c == nil {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.set[slug]
	return ok
}

// Append adds the given slug to the in-memory denylist and persists the
// full list back to disk. Idempotent — adding a slug that's already
// present is a no-op.
func (c *AliasDenylistConfig) Append(slug string) error {
	if c == nil {
		return fmt.Errorf("alias denylist not initialised")
	}
	if slug == "" {
		return fmt.Errorf("slug is empty")
	}

	c.mu.Lock()
	if c.set == nil {
		c.set = make(map[string]struct{})
	}
	if _, exists := c.set[slug]; exists {
		c.mu.Unlock()
		return nil
	}
	c.IgnoredSlugs = append(c.IgnoredSlugs, slug)
	c.set[slug] = struct{}{}
	// Capture a snapshot of the current list for persistence outside the
	// lock so we don't hold it during file I/O.
	snapshot := make([]string, len(c.IgnoredSlugs))
	copy(snapshot, c.IgnoredSlugs)
	c.mu.Unlock()

	return c.persist(snapshot)
}

// Remove drops a slug from the denylist and persists the change. Used by a
// future "restore auto-discovery for this slug" UI action; not wired into
// the v1 UI but provided for symmetry.
func (c *AliasDenylistConfig) Remove(slug string) error {
	if c == nil {
		return fmt.Errorf("alias denylist not initialised")
	}

	c.mu.Lock()
	if _, exists := c.set[slug]; !exists {
		c.mu.Unlock()
		return nil
	}
	delete(c.set, slug)
	newList := make([]string, 0, len(c.IgnoredSlugs))
	for _, s := range c.IgnoredSlugs {
		if s != slug {
			newList = append(newList, s)
		}
	}
	c.IgnoredSlugs = newList
	snapshot := make([]string, len(newList))
	copy(snapshot, newList)
	c.mu.Unlock()

	return c.persist(snapshot)
}

// Build (re)populates the lookup set from IgnoredSlugs. Call once after
// yaml unmarshal — and again any time IgnoredSlugs is mutated outside
// the Append / Remove helpers.
func (c *AliasDenylistConfig) Build() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.set = make(map[string]struct{}, len(c.IgnoredSlugs))
	for _, s := range c.IgnoredSlugs {
		if s != "" {
			c.set[s] = struct{}{}
		}
	}
}

// persist writes the current ignored-slug list back to the yaml file
// the config was loaded from. Preserves an explanatory header on every
// save so the file stays self-documenting.
func (c *AliasDenylistConfig) persist(slugs []string) error {
	if c.path == "" {
		return nil // loaded from a directory that doesn't write back (tests)
	}
	payload := struct {
		IgnoredSlugs []string `yaml:"ignored_slugs"`
	}{IgnoredSlugs: slugs}
	body, err := yaml.Marshal(payload)
	if err != nil {
		return err
	}
	var out []byte
	out = append(out, aliasDenylistHeader...)
	out = append(out, body...)
	return os.WriteFile(c.path, out, 0o644)
}

// aliasDenylistHeader keeps the schema doc on top of the file across
// every Web-UI save. yaml.Marshal would otherwise strip comments.
const aliasDenylistHeader = `# Auto-discovered wiki entity slugs that must NEVER be promoted to a
# wiki-auto brand group, regardless of how many product pages point at them.
#
# When to add an entry: a wiki entity shows up in the Web UI's "Wiki 自动
# 发现的品牌组" section but isn't actually a competitor / brand you want
# tracked (e.g. cloud platforms you integrate with, generic SoC vendors,
# software your products run on top of). Click the ❌ button in the Web UI
# to append automatically, or edit this file by hand.
#
# This file is hot-rebuilt on every EntityAlias save and on every server
# restart, so newly-added entries take effect immediately.

`

// loadAliasDenylist reads config/entity_alias_denylist.yaml from configDir.
// Missing file is non-fatal — returns (nil, nil) so callers can degrade
// gracefully (auto-discovery sees an empty denylist).
func loadAliasDenylist(configDir string) (*AliasDenylistConfig, error) {
	path := filepath.Join(configDir, "entity_alias_denylist.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Return an empty denylist that still knows where to write
			// when Append is later called from the Web UI.
			cfg := &AliasDenylistConfig{path: path}
			cfg.Build()
			return cfg, nil
		}
		return nil, fmt.Errorf("entity_alias_denylist.yaml: %w", err)
	}
	var cfg AliasDenylistConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("entity_alias_denylist.yaml parse error: %w", err)
	}
	cfg.path = path
	cfg.Build()
	fmt.Printf("Loaded alias denylist: %d ignored slugs\n", len(cfg.IgnoredSlugs))
	return &cfg, nil
}
