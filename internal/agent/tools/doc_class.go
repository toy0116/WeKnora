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
// for a chunk's knowledge title, or "" when the title doesn't classify.
//
// The classifier is first-match-wins over the patterns in doc_classes.yaml;
// see internal/config/doc_class.go for the algorithm.
func BuildDocClassAttr(title string, classes *config.DocClassConfig) string {
	if classes == nil {
		return ""
	}
	name := classes.Classify(title)
	if name == "" {
		return ""
	}
	return fmt.Sprintf(` doc_class="%s"`, xmlAttrEscape(name))
}
