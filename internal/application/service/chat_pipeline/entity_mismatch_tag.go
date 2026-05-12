package chatpipeline

import (
	"fmt"
	"sort"
	"strings"

	"context"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
)

// tagEntityMismatches walks the retrieved KB chunks and stamps three classes
// of brand-attribution signal onto r.Metadata, returning an optional
// <retrieval_gap> XML block when the query mentions multiple brands but the
// chunks cover only a strict subset of them.
//
// What gets stamped (per chunk):
//
//	entity_owner    = "<canonical brand>"           // chunk has exactly 1 brand
//	entity_aliases  = "alt1,alt2,..."               // alternate yaml-curated names
//	entity_mismatch = "true"                        // chunk's brand ≠ any anchor
//
// What gets returned (per request):
//
//	non-empty <retrieval_gap> XML block when the query anchors ≥ 2 brands
//	AND the retrieved chunks fail to cover at least one of them — this is the
//	"single-side comparison" failure mode where the user asked X-vs-Y but the
//	selected KB only contains X documents, producing a lopsided answer.
//
// Algorithm (mirrors agent tool TagChunkOwnerAttr + TagChunkMismatchAttrs):
//  1. Scan query + rewriteQuery for alias groups → "anchor groups" (strict
//     DetectFormGroups: brand mentions only, products excluded so "EG71" in a
//     query doesn't anchor anything by itself).
//  2. For each non-web chunk, scan title + filename + first 300 bytes for any
//     brand groups → "chunk groups" (technology / concept groups filtered out).
//  3. If chunk has EXACTLY 1 brand, stamp entity_owner unconditionally — even
//     without an anchor in the query. This mirrors anchor-free owner tagging
//     in list_knowledge_chunks so the LLM sees explicit attribution even in
//     comparison queries (where the intersection rule below wouldn't fire).
//  4. If anchor groups exist AND chunk groups are disjoint from anchors, ALSO
//     stamp entity_mismatch="true" (the classic cross-brand contamination
//     signal).
//  5. After the loop, compute the per-query coverage gap and return a warning
//     block when |anchor| ≥ 2 and not every anchor brand is represented in the
//     retrieved chunks.
//
// Web-search chunks are intentionally excluded: they may legitimately compare
// or contrast multiple vendors in a single passage, and their source-of-truth
// is the live web — not a KB the user can "expand".
//
// Side-effects: mutates r.Metadata in-place per the rules above. All other
// chunks are left untouched.
func tagEntityMismatches(
	ctx context.Context,
	query, rewriteQuery string,
	aliases *config.EntityAliasConfig,
	results []*types.SearchResult,
) string {
	if aliases == nil || len(results) == 0 {
		return ""
	}

	// Step 1: detect which entity groups the query is anchored to.
	// Use DetectFormGroups (strict, forms only) so a product mention in the
	// query (e.g. "EG71") is NOT treated as a brand anchor — the user might
	// be asking who owns the product. Only an explicit brand name (e.g.
	// "Robustel", "鲁邦通") counts as an anchor. This keeps the mismatch
	// check sensitive to "user named brand X but chunks are about brand Y".
	anchorGroups := aliases.DetectFormGroups(query + " " + rewriteQuery)

	if len(anchorGroups) > 0 {
		pipelineInfo(ctx, "EntityMismatch", "anchor_detected", map[string]interface{}{
			"anchor_group_count": len(anchorGroups),
			"anchors":            canonicalNames(anchorGroups),
		})
	}

	coveredAnchors := make(map[int]struct{}, len(anchorGroups))
	mismatchCount := 0
	ownerStampedCount := 0

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

		raw := aliases.DetectGroups(scanText)
		if len(raw) == 0 {
			// Chunk doesn't reference any known entity → nothing to stamp.
			continue
		}
		// Filter chunk-detected groups to BRAND groups only. A chunk that only
		// mentions a technology form (LoRa, BAS, IoT…) is not evidence of brand
		// attribution; flagging it would falsely tag the brand's own datasheets
		// when the chunk happens to discuss the technology.
		chunkGroups := make(map[int]string, len(raw))
		for gi, name := range raw {
			if gi >= 0 && gi < len(aliases.Groups) && aliases.Groups[gi].IsBrand() {
				chunkGroups[gi] = name
			}
		}
		if len(chunkGroups) == 0 {
			continue // no brand signal in chunk
		}

		// Step 3a (anchor-free owner): when chunk has exactly ONE brand, stamp
		// entity_owner + entity_aliases regardless of whether the query anchors
		// anything. Comparison chunks with multiple brands stay silent — the
		// ownership claim would be ambiguous.
		if len(chunkGroups) == 1 {
			var ownerCanonical string
			for _, name := range chunkGroups {
				ownerCanonical = name
				break
			}
			r.Metadata = ensureMetadata(r.Metadata)
			r.Metadata["entity_owner"] = ownerCanonical
			if forms := aliases.FormsForBrand(ownerCanonical); len(forms) > 1 {
				r.Metadata["entity_aliases"] = strings.Join(forms[1:], ",")
			}
			ownerStampedCount++
		}

		// Step 3b (coverage tracking for retrieval_gap): note which anchor
		// brands are represented by AUTHORITATIVE PRODUCT evidence in the
		// retrieved chunks. The Direction-C pass earlier in OnEvent stamps
		// r.Metadata["doc_class"]; per Rule 8 only `product` chunks are
		// authoritative for product-capability claims, so they're the only
		// class that counts as real coverage for a comparison question.
		//
		// Why this matters: in the f438430e session the KB had Robustel-
		// authored *competitive* analyses ("Competitive_Analysis_Teltonika_
		// RUT906...") that mention Robustel by name but don't contain any
		// Robustel product specs. If we counted every brand mention as
		// coverage, the gap warning would never fire — the LLM would see
		// "Robustel covered" and answer with Robustel's own self-promo
		// passages while pretending it's product info. Restricting to
		// doc_class=product makes the gap warning trigger correctly when
		// the missing side has only its competitive / strategy / research
		// material in the KB.
		//
		// When doc_class is empty (no doc_classes.yaml loaded, or chunk
		// title matched no pattern AND its KB has no kb_default), fall
		// back to "any brand mention counts as coverage" so deployments
		// without doc_class config don't lose the gap-warning entirely.
		docClass := ""
		if r.Metadata != nil {
			docClass = r.Metadata["doc_class"]
		}
		coverageCounts := docClass == "" || docClass == "product"
		if coverageCounts {
			for gi := range chunkGroups {
				if _, ok := anchorGroups[gi]; ok {
					coveredAnchors[gi] = struct{}{}
				}
			}
		}

		// Step 4 (anchor-driven mismatch): only meaningful when the user
		// actually named a brand in the query.
		if len(anchorGroups) == 0 {
			continue
		}
		if groupsIntersect(anchorGroups, chunkGroups) {
			continue // Same entity family → not a mismatch.
		}

		// Mismatch: chunk is about a different brand than what the user asked
		// about. Stamp the mismatch flag; entity_owner may already be set by
		// Step 3a (single-brand case). For multi-brand chunks with no anchor
		// overlap, fall back to "first brand wins" so downstream rules see a
		// concrete owner attribute.
		r.Metadata = ensureMetadata(r.Metadata)
		r.Metadata["entity_mismatch"] = "true"
		if r.Metadata["entity_owner"] == "" {
			for _, name := range chunkGroups {
				r.Metadata["entity_owner"] = name
				if forms := aliases.FormsForBrand(name); len(forms) > 1 {
					r.Metadata["entity_aliases"] = strings.Join(forms[1:], ",")
				}
				break
			}
		}

		mismatchCount++
		pipelineInfo(ctx, "EntityMismatch", "chunk_flagged", map[string]interface{}{
			"chunk_id":     r.ID,
			"source_doc":   r.KnowledgeTitle,
			"entity_owner": r.Metadata["entity_owner"],
			"anchor_names": canonicalNames(anchorGroups),
		})
	}

	if mismatchCount > 0 || ownerStampedCount > 0 {
		pipelineInfo(ctx, "EntityMismatch", "summary", map[string]interface{}{
			"total_kb_results":    len(results),
			"mismatch_count":      mismatchCount,
			"owner_stamped_count": ownerStampedCount,
		})
	}

	// Step 5 (retrieval_gap): the user named ≥ 2 brands but the chunks didn't
	// cover all of them. The LLM is told to refuse the comparison and ask the
	// user to broaden the KB selection — never to fabricate the missing side.
	if len(anchorGroups) >= 2 && len(coveredAnchors) < len(anchorGroups) {
		warning := buildRetrievalGapBlock(anchorGroups, coveredAnchors)
		pipelineInfo(ctx, "EntityMismatch", "retrieval_gap", map[string]interface{}{
			"anchor_count":  len(anchorGroups),
			"covered_count": len(coveredAnchors),
			"anchors":       canonicalNames(anchorGroups),
		})
		return warning
	}
	return ""
}

// buildRetrievalGapBlock formats a deterministic XML warning telling the LLM
// the retrieval is missing one side of a multi-brand comparison.
//
// Output is stable (sorted brand lists) so it can be string-match-tested
// without relying on map iteration order. Format:
//
//	<retrieval_gap kind="single_side_comparison">
//	  Query references brands: [鲁邦通, Milesight]
//	  Retrieved chunks cover: [Milesight]
//	  Missing brand documents: [鲁邦通]
//	  ACTION: ...
//	</retrieval_gap>
func buildRetrievalGapBlock(anchors map[int]string, covered map[int]struct{}) string {
	allBrands := make([]string, 0, len(anchors))
	coveredBrands := make([]string, 0, len(covered))
	missingBrands := make([]string, 0, len(anchors)-len(covered))
	for gi, name := range anchors {
		allBrands = append(allBrands, name)
		if _, ok := covered[gi]; ok {
			coveredBrands = append(coveredBrands, name)
		} else {
			missingBrands = append(missingBrands, name)
		}
	}
	sort.Strings(allBrands)
	sort.Strings(coveredBrands)
	sort.Strings(missingBrands)

	var b strings.Builder
	b.WriteString(`<retrieval_gap kind="single_side_comparison">` + "\n")
	fmt.Fprintf(&b, "  Query references brands: [%s]\n", strings.Join(allBrands, ", "))
	if len(coveredBrands) == 0 {
		b.WriteString("  Retrieved chunks cover: [] (no retrieved chunk references any anchor brand)\n")
	} else {
		fmt.Fprintf(&b, "  Retrieved chunks cover: [%s]\n", strings.Join(coveredBrands, ", "))
	}
	fmt.Fprintf(&b, "  Missing brand documents: [%s]\n", strings.Join(missingBrands, ", "))
	b.WriteString(
		"  ACTION: The user asked a comparison across multiple brands but the\n" +
			"  currently-selected knowledge base(s) contain documents for only a\n" +
			"  subset of them. Tell the user explicitly which brand(s) lack source\n" +
			"  documents in this retrieval, and recommend they expand the KB\n" +
			"  selection (e.g. add a KB containing the missing brand's datasheets)\n" +
			"  before re-asking. Do NOT fabricate specs for the missing side.\n" +
			"  Do NOT produce a misleading single-sided comparison that the user\n" +
			"  might mistake for a balanced answer.\n")
	b.WriteString("</retrieval_gap>\n")
	return b.String()
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
