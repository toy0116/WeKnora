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
				Forms:    []string{"Milesight", "星纵物联"},
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
