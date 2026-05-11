package tools

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
)

func testAliases() *config.EntityAliasConfig {
	cfg := &config.EntityAliasConfig{
		Groups: []config.EntityAliasGroup{
			{
				Forms:    []string{"Robustel", "鲁邦通"},
				Products: []string{"EG5120", "R1511LG", "RCMS"},
			},
			{
				Forms:    []string{"Milesight", "星纵物联", "星纵"},
				Products: []string{"EG71", "UG65"},
			},
		},
	}
	cfg.Build()
	return cfg
}

func TestTagChunkMismatchAttrs_MilesightChunkVsRobustelQuery(t *testing.T) {
	aliases := testAliases()
	scan := chunkScanSnippet(
		"Milesight EG71 Datasheet",
		"milesight-eg71.pdf",
		"Milesight EG71 features 8 universal inputs and PT1000 sensor support...",
	)
	attrs := TagChunkMismatchAttrs("Robustel EG71 spec", scan, aliases)
	if !strings.Contains(attrs, `entity_owner="Milesight"`) {
		t.Errorf("expected entity_owner=Milesight in attrs, got %q", attrs)
	}
	if !strings.Contains(attrs, `entity_mismatch="true"`) {
		t.Errorf("expected entity_mismatch=true in attrs, got %q", attrs)
	}
}

func TestTagChunkMismatchAttrs_OnTopicChunkProducesNoAttrs(t *testing.T) {
	aliases := testAliases()
	scan := chunkScanSnippet(
		"Robustel EG5120 spec",
		"robustel-eg5120.pdf",
		"Robustel EG5120 is a 5G industrial edge gateway...",
	)
	if got := TagChunkMismatchAttrs("Robustel EG5120 spec", scan, aliases); got != "" {
		t.Errorf("expected empty attrs for on-topic chunk, got %q", got)
	}
}

func TestTagChunkMismatchAttrs_NoAnchorProducesNoAttrs(t *testing.T) {
	aliases := testAliases()
	// Query mentions no known brand/product → no anchor → silent.
	scan := chunkScanSnippet("Milesight EG71", "x.pdf", "Milesight content")
	if got := TagChunkMismatchAttrs("compare two gateways", scan, aliases); got != "" {
		t.Errorf("expected empty attrs without anchor, got %q", got)
	}
}

func TestTagChunkMismatchAttrs_NilAliasesSafe(t *testing.T) {
	if got := TagChunkMismatchAttrs("anything", "anything", nil); got != "" {
		t.Errorf("nil aliases must be a no-op, got %q", got)
	}
}

func TestBuildEntityWarningBlock_FlagsConflict(t *testing.T) {
	aliases := testAliases()
	warn := BuildEntityWarningBlock("写鲁邦通 EG71 的 pitch", aliases)
	if warn == "" {
		t.Fatal("expected non-empty warning block for Robustel+EG71")
	}
	if !strings.Contains(warn, `claimed_owner="Robustel"`) {
		t.Errorf("warning missing claimed_owner=Robustel: %s", warn)
	}
	if !strings.Contains(warn, `actual_owner="Milesight"`) {
		t.Errorf("warning missing actual_owner=Milesight: %s", warn)
	}
	if !strings.Contains(warn, `product="EG71"`) {
		t.Errorf("warning missing product=EG71: %s", warn)
	}
	// Sanity: warning starts with the entity_warning open tag — order matters
	// for prepending before <search_results> / <grep_results>.
	if !strings.HasPrefix(warn, "<entity_warning") {
		t.Errorf("warning must start with <entity_warning, got: %s", warn[:40])
	}
}

func TestBuildEntityWarningBlock_NoConflictSilent(t *testing.T) {
	aliases := testAliases()
	if got := BuildEntityWarningBlock("Robustel EG5120 specs", aliases); got != "" {
		t.Errorf("expected silent on correct attribution, got %q", got)
	}
}

func TestBuildEntityWarningBlock_NilAliasesSafe(t *testing.T) {
	if got := BuildEntityWarningBlock("anything", nil); got != "" {
		t.Errorf("nil aliases must be a no-op, got %q", got)
	}
}

func TestTagChunkMismatchAttrs_TechOnlyChunkProducesNoOwner(t *testing.T) {
	// Regression: a Robustel chunk that doesn't mention any registered brand
	// or product in its first 300 bytes but DOES mention "Building Automation"
	// (a technology group with kind=technology) must NOT be tagged with
	// entity_owner="楼宇自动化" entity_mismatch="true". The technology group
	// is not a brand — chunks owning only technology mentions provide no
	// evidence of cross-brand attribution.
	cfg := &config.EntityAliasConfig{
		Groups: []config.EntityAliasGroup{
			{Forms: []string{"Robustel", "鲁邦通"}, Products: []string{"R1520LG"}},
			{Forms: []string{"Milesight"}, Products: []string{"EG71"}},
			{Forms: []string{"楼宇自动化", "Building Automation", "BAS", "BMS"}, Kind: "technology"},
		},
	}
	cfg.Build()
	scan := chunkScanSnippet(
		"some-internal-doc.pdf", // no brand/product in title
		"",
		"This chunk discusses Building Automation Systems (BAS) generally, including BMS integration patterns and IoT considerations.",
	)
	got := TagChunkMismatchAttrs("Robustel LoRaWAN Edge Gateway specs", scan, cfg)
	if got != "" {
		t.Errorf("tech-only chunk must not be flagged, got %q", got)
	}
}

func TestTagChunkOwnerAttr_SingleBrandDetected(t *testing.T) {
	aliases := testAliases()
	scan := chunkScanSnippet(
		"Milesight EG71 Datasheet",
		"eg71-datasheet-en.pdf",
		"Milesight EG71 is an intelligent and powerful edge IoT gateway...",
	)
	got := TagChunkOwnerAttr(scan, aliases)
	if !strings.Contains(got, `entity_owner="Milesight"`) {
		t.Errorf("expected entity_owner=Milesight, got %q", got)
	}
	// No entity_mismatch attribute — anchor-free variant.
	if strings.Contains(got, "entity_mismatch") {
		t.Errorf("entity_mismatch must not appear in anchor-free tag, got %q", got)
	}
}

func TestTagChunkOwnerAttr_DetectsViaProductSKU(t *testing.T) {
	// Chunk that doesn't mention Milesight by name but contains EG71 (a
	// registered Milesight product). Should still attribute to Milesight.
	aliases := testAliases()
	scan := chunkScanSnippet(
		"EG71 building IoT gateway specs",
		"",
		"Receive data on up to 8 LoRaWAN channels, supports BACnet MS/TP, Modbus...",
	)
	got := TagChunkOwnerAttr(scan, aliases)
	if !strings.Contains(got, `entity_owner="Milesight"`) {
		t.Errorf("expected entity_owner=Milesight via EG71 product, got %q", got)
	}
}

func TestTagChunkOwnerAttr_AmbiguousChunkProducesNothing(t *testing.T) {
	// Comparison chunk mentions BOTH Robustel and Milesight — can't claim
	// single ownership, must stay silent.
	aliases := testAliases()
	scan := chunkScanSnippet(
		"Robustel vs Milesight comparison",
		"",
		"Robustel R1520LG has X, Milesight EG71 has Y, our analysis shows...",
	)
	if got := TagChunkOwnerAttr(scan, aliases); got != "" {
		t.Errorf("ambiguous chunk must produce empty, got %q", got)
	}
}

func TestTagChunkOwnerAttr_NoBrandProducesNothing(t *testing.T) {
	aliases := testAliases()
	scan := chunkScanSnippet("general LoRaWAN concepts", "", "LoRaWAN is a low-power...")
	if got := TagChunkOwnerAttr(scan, aliases); got != "" {
		t.Errorf("no-brand chunk must produce empty, got %q", got)
	}
}

func TestTagChunkOwnerAttr_NilAliasesSafe(t *testing.T) {
	if got := TagChunkOwnerAttr("Milesight EG71 datasheet", nil); got != "" {
		t.Errorf("nil aliases must be a no-op, got %q", got)
	}
}

func TestTagChunkOwnerAttr_EmitsEntityAliasesWhenAlternatesExist(t *testing.T) {
	aliases := testAliases() // Milesight group has 3 forms: Milesight, 星纵物联, 星纵
	scan := chunkScanSnippet(
		"Milesight EG71 Datasheet",
		"eg71-datasheet-en.pdf",
		"Milesight EG71 is a powerful edge IoT gateway...",
	)
	got := TagChunkOwnerAttr(scan, aliases)
	if !strings.Contains(got, `entity_owner="Milesight"`) {
		t.Errorf("missing entity_owner, got %q", got)
	}
	if !strings.Contains(got, `entity_aliases="星纵物联,星纵"`) {
		t.Errorf("expected entity_aliases listing 星纵物联 and 星纵, got %q", got)
	}
}

func TestTagChunkOwnerAttr_NoAliasesAttrWhenBrandHasNoAlternates(t *testing.T) {
	// Build a config where the brand has a single Form — the LLM has no
	// alternate-name decision to make, so no entity_aliases attribute.
	cfg := &config.EntityAliasConfig{
		Groups: []config.EntityAliasGroup{
			{Forms: []string{"OnlyOneNameCo"}, Products: []string{"OO100"}},
		},
	}
	cfg.Build()
	scan := chunkScanSnippet("OO100 spec", "", "OnlyOneNameCo OO100 features...")
	got := TagChunkOwnerAttr(scan, cfg)
	if !strings.Contains(got, `entity_owner="OnlyOneNameCo"`) {
		t.Errorf("missing entity_owner, got %q", got)
	}
	if strings.Contains(got, "entity_aliases") {
		t.Errorf("single-form brand must NOT emit entity_aliases, got %q", got)
	}
}

func TestTagChunkMismatchAttrs_IncludesEntityAliasesOnMismatch(t *testing.T) {
	aliases := testAliases()
	scan := chunkScanSnippet(
		"Milesight EG71 Datasheet",
		"eg71-datasheet-en.pdf",
		"Milesight EG71 features 8 universal inputs and PT1000 sensor support...",
	)
	// Query anchors Robustel; chunk owner is Milesight — mismatch fires.
	attrs := TagChunkMismatchAttrs("Robustel EG71 spec", scan, aliases)
	if !strings.Contains(attrs, `entity_owner="Milesight"`) {
		t.Errorf("missing entity_owner, got %q", attrs)
	}
	if !strings.Contains(attrs, `entity_aliases="星纵物联,星纵"`) {
		t.Errorf("mismatch attrs must surface aliases too, got %q", attrs)
	}
	if !strings.Contains(attrs, `entity_mismatch="true"`) {
		t.Errorf("missing entity_mismatch, got %q", attrs)
	}
}

func TestTagChunkOwnerAttr_TechnologyOnlyChunkProducesNothing(t *testing.T) {
	// A chunk only mentioning a technology group (LoRaWAN) is not owned by
	// any brand — owner attribute must not fire.
	cfg := &config.EntityAliasConfig{
		Groups: []config.EntityAliasGroup{
			{Forms: []string{"Robustel"}, Products: []string{"R1520LG"}},
			{Forms: []string{"LoRaWAN", "LoRa"}, Kind: "technology"},
		},
	}
	cfg.Build()
	scan := chunkScanSnippet("LoRaWAN gateway concepts", "", "LoRaWAN supports class A, B, C devices...")
	if got := TagChunkOwnerAttr(scan, cfg); got != "" {
		t.Errorf("technology-only chunk must produce empty, got %q", got)
	}
}

func TestChunkScanSnippet_RespectsBudget(t *testing.T) {
	// Long content must be truncated so deep-buried coincidental brand
	// mentions don't trigger false-positive mismatches.
	title := "x"
	filename := "y"
	content := strings.Repeat("a", 1000) + "Milesight" // brand appears after byte 300
	scan := chunkScanSnippet(title, filename, content)
	if strings.Contains(scan, "Milesight") {
		t.Errorf("expected Milesight to be truncated past 300-byte budget, but scan contained it")
	}
}
