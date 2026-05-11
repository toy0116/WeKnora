package chatpipeline

import (
	"context"
	"strings"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
)

// tagEntityMismatches detects KB chunks whose primary entity family does NOT
// match the entity anchor(s) detected in the user query, and stamps those chunks
// with two metadata keys so the LLM can see the mismatch explicitly:
//
//	entity_mismatch = "true"
//	entity_owner    = "<canonical name of the entity found in the chunk>"
//
// Algorithm
//  1. Scan query + rewriteQuery for alias groups → "anchor groups" (the entity
//     the user is asking about / writing about).
//  2. For each KB chunk, scan KnowledgeTitle + KnowledgeFilename + first 300
//     bytes of Content for alias groups → "chunk groups".
//  3. If chunk groups are non-empty AND none of them intersects with the anchor
//     groups, the chunk is about a DIFFERENT entity → flag it.
//
// Web-search chunks are intentionally excluded: they may legitimately compare
// or contrast multiple vendors in a single passage.
//
// Side-effects: mutates r.Metadata in-place for mismatched chunks. All other
// chunks are left untouched.
func tagEntityMismatches(
	ctx context.Context,
	query, rewriteQuery string,
	aliases *config.EntityAliasConfig,
	results []*types.SearchResult,
) {
	if aliases == nil || len(results) == 0 {
		return
	}

	// Step 1: detect which entity groups the query is anchored to.
	// Use DetectFormGroups (strict, forms only) so a product mention in the
	// query (e.g. "EG71") is NOT treated as a brand anchor — the user might
	// be asking who owns the product. Only an explicit brand name (e.g.
	// "Robustel", "鲁邦通") counts as an anchor. This keeps the mismatch
	// check sensitive to "user named brand X but chunks are about brand Y".
	anchorGroups := aliases.DetectFormGroups(query + " " + rewriteQuery)
	if len(anchorGroups) == 0 {
		// Query doesn't reference any known entity — nothing to compare against.
		return
	}

	pipelineInfo(ctx, "EntityMismatch", "anchor_detected", map[string]interface{}{
		"anchor_group_count": len(anchorGroups),
		"anchors":            canonicalNames(anchorGroups),
	})

	mismatchCount := 0
	for _, r := range results {
		// Skip web-search chunks.
		isWeb := strings.ToLower(r.KnowledgeSource) == "web_search" ||
			r.ChunkType == string(types.ChunkTypeWebSearch)
		if isWeb {
			continue
		}

		// Step 2: scan chunk for entity group presence.
		scanText := r.KnowledgeTitle + " " + r.KnowledgeFilename
		if len(r.Content) > 300 {
			scanText += " " + r.Content[:300]
		} else {
			scanText += " " + r.Content
		}

		chunkGroups := aliases.DetectGroups(scanText)
		if len(chunkGroups) == 0 {
			// Chunk doesn't reference any known entity → can't determine mismatch.
			continue
		}

		// Step 3: check intersection.
		if groupsIntersect(anchorGroups, chunkGroups) {
			continue // Same entity family → OK.
		}

		// Mismatch: chunk is about a different entity than the query anchor.
		r.Metadata = ensureMetadata(r.Metadata)
		r.Metadata["entity_mismatch"] = "true"

		// Record the primary detected entity in the chunk (first group found).
		for _, name := range chunkGroups {
			r.Metadata["entity_owner"] = name
			break
		}

		mismatchCount++
		pipelineInfo(ctx, "EntityMismatch", "chunk_flagged", map[string]interface{}{
			"chunk_id":     r.ID,
			"source_doc":   r.KnowledgeTitle,
			"entity_owner": r.Metadata["entity_owner"],
			"anchor_names": canonicalNames(anchorGroups),
		})
	}

	if mismatchCount > 0 {
		pipelineInfo(ctx, "EntityMismatch", "summary", map[string]interface{}{
			"total_kb_results": len(results),
			"mismatch_count":   mismatchCount,
		})
	}
}

// groupsIntersect returns true when the two group-index maps share at least one key.
func groupsIntersect(a, b map[int]string) bool {
	// Iterate over the smaller map for efficiency.
	if len(a) > len(b) {
		a, b = b, a
	}
	for gi := range a {
		if _, ok := b[gi]; ok {
			return true
		}
	}
	return false
}

// canonicalNames extracts the canonical name values from a group map for logging.
func canonicalNames(groups map[int]string) []string {
	names := make([]string, 0, len(groups))
	for _, name := range groups {
		names = append(names, name)
	}
	return names
}
