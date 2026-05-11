// Package config — document-class classifier loaded from doc_classes.yaml.
//
// The classifier maps a knowledge-base document title to a single class name
// (product / strategy / competitive / research / training / solution / "").
// The downstream retrieval tools attach the class as an XML attribute on each
// chunk so the LLM can apply the trust hierarchy defined in the system
// prompt's Rule 13.
//
// Algorithm:
//   - First-match-wins ordering across classes (most-specific first; product
//     beats competitive even if a datasheet title happens to mention a
//     competitor name).
//   - Patterns are RE2 regexps compiled once at startup. Per-pattern errors
//     are logged and skipped; a malformed YAML never breaks startup, the
//     classifier just degrades gracefully to fewer patterns.

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"gopkg.in/yaml.v3"
)

// DocClassConfig holds the loaded document-class registry plus the optional
// KB-name → class default map.
type DocClassConfig struct {
	Classes []DocClass `yaml:"classes"`
	// KBDefaults maps a KB *name* (as it appears in the knowledge_bases
	// table) to the doc_class every document in that KB should default to,
	// unless a more specific title pattern wins. yaml-keyed by name because
	// KB UUIDs are opaque per-tenant and unstable across environments.
	KBDefaults map[string]string `yaml:"kb_defaults,omitempty"`

	// kbDefaultsByID is the runtime-resolved cache, keyed by KB ID for O(1)
	// lookup during chunk rendering. Populated by ResolveKBDefaults.
	kbDefaultsByID map[string]string
}

// KBSummary is the minimal {ID, Name} pair a KB service needs to feed in for
// kb-default resolution. We deliberately avoid coupling to internal/types
// here to keep this config package leaf-level.
type KBSummary struct {
	ID   string
	Name string
}

// DocClass is one named class with its title-matching patterns.
type DocClass struct {
	Name          string   `yaml:"name"`
	Description   string   `yaml:"description,omitempty"`
	TitlePatterns []string `yaml:"title_patterns"`

	// compiled lives outside the YAML field set; populated by Build.
	compiled []*regexp.Regexp
}

// Build compiles every pattern in every class. Invalid patterns are logged
// and skipped. Call once after YAML unmarshalling.
func (c *DocClassConfig) Build() {
	for i := range c.Classes {
		cls := &c.Classes[i]
		cls.compiled = make([]*regexp.Regexp, 0, len(cls.TitlePatterns))
		for _, p := range cls.TitlePatterns {
			re, err := regexp.Compile(p)
			if err != nil {
				fmt.Printf("doc_classes.yaml: class %q: pattern %q failed to compile: %v (skipping)\n",
					cls.Name, p, err)
				continue
			}
			cls.compiled = append(cls.compiled, re)
		}
	}
}

// Classify returns the class name for a chunk's source document.
//
// Resolution order (first match wins, stops there):
//  1. Title matches one of the per-class regex patterns → that class.
//  2. kbID is known and ResolveKBDefaults has been called → the KB's
//     default class from yaml's kb_defaults map.
//  3. Otherwise → "" (unclassified).
//
// kbID is optional (may be empty) — passing it lets the classifier honour
// per-KB defaults; otherwise resolution falls back to title-only.
func (c *DocClassConfig) Classify(title, kbID string) string {
	if c == nil {
		return ""
	}
	// Title-pattern pass (skipped when there are no classes configured).
	if title != "" && len(c.Classes) > 0 {
		for _, cls := range c.Classes {
			for _, re := range cls.compiled {
				if re.MatchString(title) {
					return cls.Name
				}
			}
		}
	}
	// KB-default fallback works independently of Classes — a deployment can
	// rely purely on kb_defaults with zero title patterns.
	if kbID != "" && len(c.kbDefaultsByID) > 0 {
		return c.kbDefaultsByID[kbID]
	}
	return ""
}

// ResolveKBDefaults rebuilds the runtime kbID→class map from the yaml-loaded
// kbDefaults (which is keyed by KB name) and the caller-provided id→name
// list. Safe to call multiple times — callers should re-call after KB create
// / rename so the map stays in sync with the database.
//
// KBs whose name isn't listed in yaml's kb_defaults are silently skipped —
// they contribute no default class.
func (c *DocClassConfig) ResolveKBDefaults(kbs []KBSummary) {
	if c == nil {
		return
	}
	resolved := make(map[string]string, len(kbs))
	for _, kb := range kbs {
		if kb.ID == "" {
			continue
		}
		if cls, ok := c.KBDefaults[kb.Name]; ok && cls != "" {
			resolved[kb.ID] = cls
		}
	}
	c.kbDefaultsByID = resolved
}

// loadDocClasses reads config/doc_classes.yaml from configDir. Returns nil
// (no error) when the file is absent — the feature is opt-in. On parse
// errors the loader returns the error so startup logs surface it.
func loadDocClasses(configDir string) (*DocClassConfig, error) {
	path := filepath.Join(configDir, "doc_classes.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("doc_classes.yaml: %w", err)
	}
	var cfg DocClassConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("doc_classes.yaml parse error: %w", err)
	}
	cfg.Build()
	fmt.Printf("Loaded document-class registry: %d classes\n", len(cfg.Classes))
	return &cfg, nil
}
