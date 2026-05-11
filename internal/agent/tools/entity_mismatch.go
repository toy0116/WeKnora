// Package tools — entity-mismatch tagging for Agent-mode tool output.
//
// Background:
//   - The chat-pipeline path (PluginIntoChatMessage) already runs
//     tagEntityMismatches on retrieved chunks, surfacing entity_mismatch /
//     entity_owner attributes to the LLM via context XML.
//   - But Agent mode (engine.go ReAct loop) BYPASSES chat_pipeline entirely:
//     it builds chunks via knowledge_search / grep_chunks tool calls and feeds
//     them straight back to the LLM. So the entity-mismatch guardrail never
//     fires in Agent mode.
//
// This file provides shared helpers both Agent tools call when rendering
// chunks to XML, so the same machine-confirmed mismatch signal reaches the
// LLM regardless of which path produced the chunk.
//
// Two helpers:
//   - TagChunkMismatchAttrs:    given a query + chunk text/title/filename,
//                                returns the XML attribute fragment to append
//                                to a <chunk ...> tag (empty if no mismatch).
//   - BuildEntityWarningBlock:   given the query, returns an <entity_warning>
//                                XML block when the query attributes a product
//                                to the wrong brand (e.g. "Robustel EG71" when
//                                EG71 is registered under Milesight). Empty
//                                string when there is no conflict.
//
// Both helpers are nil-safe: if EntityAliases config is absent they no-op.

package tools

import (
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/config"
)

// TagChunkMismatchAttrs returns an attribute fragment like
//   ` entity_owner="Milesight" entity_mismatch="true"`
// (leading space included) to be appended to an opening <chunk ...> tag,
// when the chunk's entity family does not intersect the query's entity
// anchor. Returns "" when there is no anchor, no chunk entity, or they
// match (chunk is on-topic).
//
// Same algorithm as chat_pipeline.tagEntityMismatches but operating per-chunk
// inline so each tool can decide independently whether to emit the attrs.
//
// scanText is what the function scans inside the chunk for entity presence —
// callers should pass title + filename + a leading content snippet (no need
// to include the entire chunk; the first ~300 chars are usually enough and
// avoid coincidental brand mentions deep inside long chunks).
func TagChunkMismatchAttrs(query string, scanText string, aliases *config.EntityAliasConfig) string {
	if aliases == nil {
		return ""
	}
	// Query side: forms only. A product mention in the query (e.g. "EG71")
	// is NOT treated as a brand anchor — the user might be asking who owns
	// the product. Only an explicit brand name counts as an anchor.
	anchorGroups := aliases.DetectFormGroups(query)
	if len(anchorGroups) == 0 {
		return ""
	}
	// Chunk side: forms + products. Any signal of which brand the chunk
	// belongs to is fair game.
	chunkGroups := aliases.DetectGroups(scanText)
	if len(chunkGroups) == 0 {
		return ""
	}
	// Intersect by group index — if any group overlaps, on-topic.
	for gi := range anchorGroups {
		if _, ok := chunkGroups[gi]; ok {
			return ""
		}
	}
	// No intersection → mismatch. Pick any owner from the chunk groups.
	var owner string
	for _, name := range chunkGroups {
		owner = name
		break
	}
	if owner == "" {
		return ` entity_mismatch="true"`
	}
	return fmt.Sprintf(` entity_owner="%s" entity_mismatch="true"`,
		xmlAttrEscape(owner))
}

// BuildEntityWarningBlock returns an <entity_warning> XML block when the
// query commits a "wrong-brand-for-product" attribution — e.g. mentions
// "Robustel" together with a product registered under "Milesight". Returns
// "" when there is no such conflict.
//
// The block is intended to be prepended to the tool's output string so the
// LLM sees it BEFORE the chunk list, encouraging it to abort fabrication
// before reading any retrieved content. The block names the claimed owner,
// the product, and the actual registered owner, and instructs the LLM not
// to copy specs across the attribution gap.
//
// One block aggregates all detected conflicts (deduplicated by claimed→
// actual pair) so the LLM gets the full picture in a single hit.
func BuildEntityWarningBlock(query string, aliases *config.EntityAliasConfig) string {
	if aliases == nil {
		return ""
	}
	conflicts := aliases.DetectAttributionConflicts(query)
	if len(conflicts) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(`<entity_warning kind="attribution_conflict">` + "\n")
	sb.WriteString("  Query attributes a product to a brand it does not belong to.\n")
	sb.WriteString("  Do NOT fabricate specs across this mismatch. Either ask the user to\n")
	sb.WriteString("  confirm the correct model name, restrict the answer to brand-level\n")
	sb.WriteString("  positioning (no product-specific specs), or use the registered owner.\n")
	for _, c := range conflicts {
		sb.WriteString(fmt.Sprintf(
			"  <conflict claimed_owner=\"%s\" product=\"%s\" actual_owner=\"%s\" />\n",
			xmlAttrEscape(c.ClaimedOwner),
			xmlAttrEscape(c.Product),
			xmlAttrEscape(c.ActualOwner),
		))
	}
	sb.WriteString("</entity_warning>\n")
	return sb.String()
}

// xmlAttrEscape is a minimal XML attribute escaper for the small set of
// characters that can appear in brand / product names (quotes, angle
// brackets, ampersands). Names are short and ASCII-ish so we don't need a
// full XML serializer.
func xmlAttrEscape(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return r.Replace(s)
}

// chunkScanSnippet returns the leading bytes of content used for chunk-side
// entity detection. Matches the 300-byte budget used by the chat_pipeline
// tagEntityMismatches so both paths produce consistent flags.
func chunkScanSnippet(title, filename, content string) string {
	const budget = 300
	if len(content) > budget {
		content = content[:budget]
	}
	return title + " " + filename + " " + content
}
