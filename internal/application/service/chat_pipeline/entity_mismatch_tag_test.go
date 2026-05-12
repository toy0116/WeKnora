package chatpipeline

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
)

// testAliasesCfg builds a fixture mirroring the production yaml's brand groups
// (Robustel + Milesight + a technology group). Form ordering matches prod yaml
// (entity_aliases.yaml): the Chinese / canonical Chinese name comes first so
// DetectFormGroups returns "鲁邦通" / "Milesight" as the canonical labels,
// not the English / Pinyin form. Mirrors entity_mismatch_test.go in
// agent/tools so behaviour stays in sync across both retrieval paths.
func testAliasesCfg() *config.EntityAliasConfig {
	c := &config.EntityAliasConfig{
		Groups: []config.EntityAliasGroup{
			{Forms: []string{"鲁邦通", "Robustel"}, Products: []string{"EG5120", "R1520LG"}},
			{Forms: []string{"Milesight", "星纵物联", "星纵"}, Products: []string{"EG71", "UR75"}},
			{Forms: []string{"LoRaWAN", "LoRa"}, Kind: "technology"},
		},
	}
	c.Build()
	return c
}

// kbChunk is a small constructor for a non-web KB chunk fixture so each test
// stays focused on the assertion rather than struct literal noise.
func kbChunk(id, title, filename, content string) *types.SearchResult {
	return &types.SearchResult{
		ID:                id,
		KnowledgeTitle:    title,
		KnowledgeFilename: filename,
		Content:           content,
		KnowledgeSource:   "knowledge_base",
	}
}

// ── Direction B: anchor-free entity_owner tagging ─────────────────────────

func TestTagEntityMismatches_StampsOwnerOnSingleBrandChunkEvenWithoutAnchor(t *testing.T) {
	// Regression target for the f438430e session: a Milesight-only KB chunk
	// retrieved by a comparison query ("X 和 Y 差异") never got entity_owner
	// stamped because the old code only set owner when entity_mismatch=true.
	// LLM had to infer brand from filename. Anchor-free owner closes that gap.
	aliases := testAliasesCfg()
	chunks := []*types.SearchResult{
		kbChunk("c1", "ur75-datasheet-en.pdf", "ur75-datasheet-en.pdf",
			"Milesight UR75 is a 5G industrial router with quad-core ARM Cortex-A55..."),
	}
	// Query mentions Milesight (single anchor) — chunk is on-topic, no mismatch.
	gap := tagEntityMismatches(context.Background(), "Milesight UR75 spec", "", aliases, chunks)
	if gap != "" {
		t.Errorf("single-anchor on-topic should NOT produce retrieval_gap, got %q", gap)
	}
	md := chunks[0].Metadata
	if md == nil {
		t.Fatal("expected metadata to be stamped")
	}
	if md["entity_owner"] != "Milesight" {
		t.Errorf("expected entity_owner=Milesight (anchor-free path), got %q", md["entity_owner"])
	}
	if md["entity_aliases"] != "星纵物联,星纵" {
		t.Errorf("expected entity_aliases='星纵物联,星纵', got %q", md["entity_aliases"])
	}
	if md["entity_mismatch"] != "" {
		t.Errorf("on-topic chunk must NOT be flagged mismatch, got %q", md["entity_mismatch"])
	}
}

func TestTagEntityMismatches_NoAnchorButOwnerStillStamped(t *testing.T) {
	// Query has no brand mention at all (e.g. "what is LoRaWAN"). Chunk has
	// a single brand. Anchor-free owner tagging should still fire so a future
	// LLM turn can attribute the chunk's content to the right brand.
	aliases := testAliasesCfg()
	chunks := []*types.SearchResult{
		kbChunk("c1", "ur75-datasheet-en.pdf", "ur75-datasheet-en.pdf",
			"Milesight UR75 supports LoRaWAN class A/B/C devices..."),
	}
	gap := tagEntityMismatches(context.Background(), "what is LoRaWAN class C", "", aliases, chunks)
	if gap != "" {
		t.Errorf("no-brand query should not trigger retrieval_gap, got %q", gap)
	}
	if got := chunks[0].Metadata["entity_owner"]; got != "Milesight" {
		t.Errorf("anchor-free owner must fire without query anchor, got %q", got)
	}
}

func TestTagEntityMismatches_MultiBrandChunkStaysSilent(t *testing.T) {
	// A comparison chunk that names BOTH brands shouldn't claim single
	// ownership — mirrors agent path's TagChunkOwnerAttr semantics.
	aliases := testAliasesCfg()
	chunks := []*types.SearchResult{
		kbChunk("c1", "Robustel vs Milesight comparison.md", "comparison.md",
			"Robustel R1520LG has 4G LTE, Milesight UR75 has 5G NSA — both target industrial gateways."),
	}
	_ = tagEntityMismatches(context.Background(), "industrial gateway market", "", aliases, chunks)
	if got := chunks[0].Metadata["entity_owner"]; got != "" {
		t.Errorf("multi-brand comparison chunk must stay silent, got entity_owner=%q", got)
	}
}

func TestTagEntityMismatches_TechnologyOnlyChunkProducesNoOwner(t *testing.T) {
	// Mirrors the existing chat_pipeline regression (TestTagChunkMismatchAttrs_TechOnlyChunkProducesNoOwner
	// in agent/tools): a chunk mentioning only LoRaWAN (technology group, IsBrand=false)
	// must not be claimed as owned by any brand.
	aliases := testAliasesCfg()
	chunks := []*types.SearchResult{
		kbChunk("c1", "lorawan-class-c-overview.pdf", "lorawan-class-c-overview.pdf",
			"LoRaWAN class C devices listen continuously, trading battery for latency."),
	}
	_ = tagEntityMismatches(context.Background(), "Robustel LoRaWAN class support", "", aliases, chunks)
	if got := chunks[0].Metadata["entity_owner"]; got != "" {
		t.Errorf("technology-only chunk must produce no owner, got %q", got)
	}
}

// ── Direction A: retrieval_gap for single-side comparison ──────────────────

func TestTagEntityMismatches_GapFiresOnSingleSideComparison(t *testing.T) {
	// Reproduces the f438430e session exactly: user asks "鲁邦通 vs Milesight"
	// but the KB only has Milesight datasheets. tagEntityMismatches MUST
	// return a non-empty retrieval_gap block naming 鲁邦通 as missing.
	aliases := testAliasesCfg()
	chunks := []*types.SearchResult{
		kbChunk("c1", "ur75-datasheet-en.pdf", "ur75-datasheet-en.pdf",
			"Milesight UR75 is a 5G industrial router..."),
		kbChunk("c2", "ur32-datasheet-en.pdf", "ur32-datasheet-en.pdf",
			"Milesight UR32 is a cellular gateway..."),
		kbChunk("c3", "milesight-iot-brochure-collection-en.pdf", "milesight-iot-brochure-collection-en.pdf",
			"Milesight catalogue: UR series routers, EG series gateways..."),
	}
	gap := tagEntityMismatches(context.Background(),
		"鲁邦通和Milesight在工业路由器市场的主要差异是什么？", "",
		aliases, chunks)
	if gap == "" {
		t.Fatal("expected non-empty retrieval_gap block for single-side comparison")
	}
	if !strings.HasPrefix(gap, `<retrieval_gap kind="single_side_comparison">`) {
		t.Errorf("retrieval_gap must start with the kind=single_side_comparison open tag, got: %s", gap[:60])
	}
	if !strings.Contains(gap, "Missing brand documents: [鲁邦通]") {
		t.Errorf("expected '鲁邦通' in Missing line, got: %s", gap)
	}
	if !strings.Contains(gap, "Retrieved chunks cover: [Milesight]") {
		t.Errorf("expected '[Milesight]' in Retrieved line, got: %s", gap)
	}
	if !strings.Contains(gap, "Query references brands: [Milesight, 鲁邦通]") {
		t.Errorf("expected sorted [Milesight, 鲁邦通] in Query references, got: %s", gap)
	}
	if !strings.Contains(gap, "ACTION:") {
		t.Errorf("retrieval_gap must include an ACTION directive, got: %s", gap)
	}
}

func TestTagEntityMismatches_GapFiresWhenMissingBrandIsOnlyInCompetitiveChunks(t *testing.T) {
	// Reproduces the f438430e session faithfully: the KB has Milesight
	// PRODUCT datasheets AND a Robustel-authored COMPETITIVE analysis of
	// Teltonika. The competitive chunk mentions "Robustel" by name (because
	// Robustel wrote it), but does NOT contain Robustel product specs.
	// Without doc_class-aware coverage, the gap silently failed because
	// "Robustel" appeared in chunks. With the refinement, coverage counts
	// only product-class chunks → Robustel uncovered → gap fires.
	aliases := testAliasesCfg()
	milesightProduct := kbChunk("c1", "ur75-datasheet-en.pdf", "ur75-datasheet-en.pdf",
		"Milesight UR75 is a 5G industrial router...")
	milesightProduct.Metadata = map[string]string{"doc_class": "product"}

	robustelCompetitive := kbChunk("c2", "Competitive_Analysis_Teltonika_RUT906.md",
		"Competitive_Analysis_Teltonika_RUT906.md",
		"Robustel R1520 outperforms Teltonika RUT906 in IPsec stability and "+
			"工业协议 support. Robustel has 多年深耕 in industrial IoT...")
	robustelCompetitive.Metadata = map[string]string{"doc_class": "competitive"}

	chunks := []*types.SearchResult{milesightProduct, robustelCompetitive}
	gap := tagEntityMismatches(context.Background(),
		"鲁邦通和Milesight在工业路由器市场的主要差异是什么？", "",
		aliases, chunks)
	if gap == "" {
		t.Fatal("expected gap because Robustel appears only in competitive (non-product) chunk")
	}
	if !strings.Contains(gap, "Missing brand documents: [鲁邦通]") {
		t.Errorf("expected '鲁邦通' missing (only Milesight has a product chunk), got: %s", gap)
	}
}

func TestTagEntityMismatches_GapSilentWhenBothSidesCovered(t *testing.T) {
	aliases := testAliasesCfg()
	// Both brands have product datasheets — coverage is symmetric, no gap.
	c1 := kbChunk("c1", "ur75-datasheet-en.pdf", "ur75-datasheet-en.pdf",
		"Milesight UR75 is a 5G industrial router...")
	c1.Metadata = map[string]string{"doc_class": "product"}
	c2 := kbChunk("c2", "RT056_DS_R1520_V1.0.15.pdf", "RT056_DS_R1520_V1.0.15.pdf",
		"Robustel R1520 is a 4G LTE industrial router...")
	c2.Metadata = map[string]string{"doc_class": "product"}
	chunks := []*types.SearchResult{c1, c2}
	gap := tagEntityMismatches(context.Background(),
		"鲁邦通和Milesight的差异", "", aliases, chunks)
	if gap != "" {
		t.Errorf("both-side coverage should produce no gap, got: %s", gap)
	}
}

func TestTagEntityMismatches_GapCoverageFallsBackWhenDocClassAbsent(t *testing.T) {
	// Deployment without doc_classes.yaml: chunks have no doc_class metadata.
	// The fallback in Step 3b is "any brand mention counts as coverage" so
	// the gap warning stays useful without doc-class config.
	aliases := testAliasesCfg()
	chunks := []*types.SearchResult{
		kbChunk("c1", "ur75.pdf", "ur75.pdf",
			"Milesight UR75 is a 5G industrial router..."),
		kbChunk("c2", "r1520.pdf", "r1520.pdf",
			"Robustel R1520 is a 4G LTE industrial router..."),
	}
	gap := tagEntityMismatches(context.Background(),
		"鲁邦通和Milesight的差异", "", aliases, chunks)
	if gap != "" {
		t.Errorf("without doc_class metadata, any brand mention should count as coverage, got: %s", gap)
	}
}

func TestTagEntityMismatches_GapSilentForSingleBrandQuery(t *testing.T) {
	// Single-brand query is not a "comparison gap" even with zero hits — that
	// failure mode is handled by Rule 5 ("来源不明确"), not by retrieval_gap.
	// We require ≥ 2 anchor brands before triggering the gap warning.
	aliases := testAliasesCfg()
	chunks := []*types.SearchResult{
		kbChunk("c1", "milesight-brochure.pdf", "milesight-brochure.pdf",
			"Milesight catalogue..."),
	}
	gap := tagEntityMismatches(context.Background(), "Robustel EG5120 spec", "", aliases, chunks)
	if gap != "" {
		t.Errorf("single-brand query must not trigger gap (handled by other rules), got: %s", gap)
	}
}

func TestTagEntityMismatches_GapSilentWhenNoChunksReferenceAnchor(t *testing.T) {
	// Edge case: query anchors 2 brands but ALL chunks mention neither brand
	// (e.g. retrieval returned generic LoRaWAN background). The comparison
	// gap rule still fires because the user asked X-vs-Y and the chunks
	// cover NEITHER X NOR Y — exactly the kind of asymmetric/lopsided answer
	// we want the LLM to refuse rather than smooth over.
	aliases := testAliasesCfg()
	chunks := []*types.SearchResult{
		kbChunk("c1", "lorawan-class-c.pdf", "lorawan-class-c.pdf",
			"LoRaWAN class C devices listen continuously..."),
	}
	gap := tagEntityMismatches(context.Background(),
		"鲁邦通和Milesight的对比", "", aliases, chunks)
	if gap == "" {
		t.Fatal("expected gap when chunks cover neither anchor brand")
	}
	if !strings.Contains(gap, "Missing brand documents: [Milesight, 鲁邦通]") {
		t.Errorf("expected both brands listed as missing, got: %s", gap)
	}
}

// ── Mismatch path still fires when anchor is single-brand and chunks differ ──

func TestTagEntityMismatches_SingleAnchorMismatchStillStamps(t *testing.T) {
	// Pre-existing behaviour regression: user anchors Robustel, all retrieved
	// chunks are Milesight → every chunk gets entity_mismatch=true.
	aliases := testAliasesCfg()
	chunks := []*types.SearchResult{
		kbChunk("c1", "ur75-datasheet-en.pdf", "ur75-datasheet-en.pdf",
			"Milesight UR75 is a 5G industrial router..."),
	}
	_ = tagEntityMismatches(context.Background(), "Robustel UR75 spec", "", aliases, chunks)
	if got := chunks[0].Metadata["entity_mismatch"]; got != "true" {
		t.Errorf("expected entity_mismatch=true for cross-brand query, got %q", got)
	}
	if got := chunks[0].Metadata["entity_owner"]; got != "Milesight" {
		t.Errorf("expected entity_owner=Milesight, got %q", got)
	}
}

// ── Web-search exclusion ──────────────────────────────────────────────────

func TestTagEntityMismatches_SkipsWebSearchChunks(t *testing.T) {
	aliases := testAliasesCfg()
	web := kbChunk("w1", "competitor blog", "https://example.com/blog",
		"Milesight UR75 vs Robustel R1520 — both 4G/5G industrial routers...")
	web.KnowledgeSource = "web_search"
	chunks := []*types.SearchResult{web}
	gap := tagEntityMismatches(context.Background(),
		"鲁邦通和Milesight的对比", "", aliases, chunks)
	if got := web.Metadata["entity_owner"]; got != "" {
		t.Errorf("web-search chunk must not be stamped, got entity_owner=%q", got)
	}
	// Coverage tracking also skips web chunks, so the gap fires (anchors
	// uncovered by any KB chunk).
	if gap == "" {
		t.Errorf("web-only retrieval should still produce gap (KB coverage = 0)")
	}
}

// ── Defensive guards ──────────────────────────────────────────────────────

func TestTagEntityMismatches_NilAliasesSafe(t *testing.T) {
	gap := tagEntityMismatches(context.Background(), "anything", "", nil,
		[]*types.SearchResult{kbChunk("c1", "x", "x.pdf", "content")})
	if gap != "" {
		t.Errorf("nil aliases must be a no-op, got %q", gap)
	}
}

func TestTagEntityMismatches_EmptyResultsSafe(t *testing.T) {
	aliases := testAliasesCfg()
	gap := tagEntityMismatches(context.Background(), "anything", "", aliases, nil)
	if gap != "" {
		t.Errorf("nil results must be a no-op, got %q", gap)
	}
}
