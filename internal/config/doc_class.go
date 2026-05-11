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

// DocClassConfig holds the loaded document-class registry.
type DocClassConfig struct {
	Classes []DocClass `yaml:"classes"`
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

// Classify returns the name of the first class whose patterns match the
// given title. Returns "" (unclassified) when no class matches or when the
// classifier is nil / empty.
//
// Title is matched as a whole-string regex search (not anchored) — patterns
// can use ^/$ explicitly when needed.
func (c *DocClassConfig) Classify(title string) string {
	if c == nil || len(c.Classes) == 0 || title == "" {
		return ""
	}
	for _, cls := range c.Classes {
		for _, re := range cls.compiled {
			if re.MatchString(title) {
				return cls.Name
			}
		}
	}
	return ""
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
