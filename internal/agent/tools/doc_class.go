// Package tools — document-class XML attribute builder.
//
// Wraps internal/config.DocClassConfig.Classify and returns the
// ` doc_class="..."` XML attribute fragment to splice into a <chunk ...>
// opening tag. Returns "" when the classifier is nil, the title is empty,
// or no class pattern matches — keeping unclassified chunks attribute-free
// so the LLM's prompt rule "prefer doc_class=product when available" can
// detect their absence.
//
// Centralised here (rather than inlined in every tool) so that the agent
// path (knowledge_search / grep_chunks / list_knowledge_chunks) and the
// classic RAG path (chat_pipeline.buildContextAttributes) always emit the
// same attribute shape for the same title.

package tools

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/config"
)

// BuildDocClassAttr returns ` doc_class="<class>"` (leading space included)
// for a chunk's knowledge title, or "" when neither the title pattern nor
// the KB default classifies. Pass kbID so the classifier can honour KB-level
// defaults configured in doc_classes.yaml's kb_defaults section; pass "" if
// the caller doesn't know the KB ID (e.g. web-search chunks).
//
// The classifier resolves in priority: title pattern → KB default → "".
// See internal/config/doc_class.go for the full algorithm.
func BuildDocClassAttr(title, kbID string, classes *config.DocClassConfig) string {
	if classes == nil {
		return ""
	}
	name := classes.Classify(title, kbID)
	if name == "" {
		return ""
	}
	return fmt.Sprintf(` doc_class="%s"`, xmlAttrEscape(name))
}
