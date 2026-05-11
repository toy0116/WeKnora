package config

import (
	"reflect"
	"sort"
	"testing"
)

// fixtureAliases mirrors the production entity_aliases.yaml shape for two
// brands plus a generic protocol group. Keeping the fixture inline (rather
// than re-reading the YAML file) makes the test independent of yaml loading
// and from disk layout — we're testing the detection algorithm, not the
// loader.
func fixtureAliases() *EntityAliasConfig {
	cfg := &EntityAliasConfig{
		Groups: []EntityAliasGroup{
			{
				Forms:    []string{"Robustel", "鲁邦通"},
				Products: []string{"EG5120", "R1511LG", "R1520LG", "RCMS"},
			},
			{
				Forms:    []string{"Milesight", "星纵物联", "星纵"},
				Products: []string{"EG71", "UG65"},
			},
			{
				Forms: []string{"Siemens", "西门子"},
			},
			{
				Forms: []string{"LoRaWAN", "LoRa"},
			},
		},
	}
	cfg.Build()
	return cfg
}

func TestDetectGroups_FormOnlyMatch(t *testing.T) {
	cfg := fixtureAliases()
	got := cfg.DetectGroups("我们和 Siemens 合作过几个项目")
	if _, ok := got[2]; !ok {
		t.Fatalf("expected group 2 (Siemens) to match, got %v", got)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 group matched, got %d: %v", len(got), got)
	}
}

func TestDetectGroups_ProductOnlyMatch(t *testing.T) {
	cfg := fixtureAliases()
	// "EG71" alone (no brand) — should detect Milesight via product.
	got := cfg.DetectGroups("EG71 datasheet specs")
	if name, ok := got[1]; !ok || name != "Milesight" {
		t.Fatalf("expected group 1 (Milesight) via EG71, got %v", got)
	}
}

func TestDetectGroups_ProductCaseInsensitive(t *testing.T) {
	cfg := fixtureAliases()
	got := cfg.DetectGroups("eg71 specs")
	if _, ok := got[1]; !ok {
		t.Fatalf("expected case-insensitive product match on lowercase eg71, got %v", got)
	}
}

func TestDetectFormGroups_IgnoresProducts(t *testing.T) {
	// The strict variant is used for query anchoring: a product mention alone
	// must NOT count as a brand anchor (the user might be asking who owns it).
	cfg := fixtureAliases()
	got := cfg.DetectFormGroups("EG71 datasheet")
	if len(got) != 0 {
		t.Errorf("DetectFormGroups must ignore product matches, got %v", got)
	}
	// With an explicit brand form added, it should match the brand.
	got = cfg.DetectFormGroups("Robustel EG71 datasheet")
	if len(got) != 1 {
		t.Errorf("expected exactly 1 form match for Robustel, got %v", got)
	}
	if _, ok := got[0]; !ok {
		t.Errorf("expected group 0 (Robustel) via form, got %v", got)
	}
}

func TestDetectGroups_BrandAndProductInDifferentGroups(t *testing.T) {
	// User writes "Robustel EG71". EG71 is registered under Milesight. Both
	// brand (Robustel) and product (EG71) should be detected — the caller
	// (attribution-conflict check) then flags the cross-group mismatch.
	cfg := fixtureAliases()
	got := cfg.DetectGroups("Robustel EG71 pitch deck")
	if _, ok := got[0]; !ok {
		t.Fatalf("expected group 0 (Robustel) detected via brand form, got %v", got)
	}
	if _, ok := got[1]; !ok {
		t.Fatalf("expected group 1 (Milesight) detected via product, got %v", got)
	}
}

func TestDetectAttributionConflicts_FlagsCrossBrandProduct(t *testing.T) {
	cfg := fixtureAliases()
	conflicts := cfg.DetectAttributionConflicts("写一个鲁邦通 EG71 的 pitch")
	if len(conflicts) != 1 {
		t.Fatalf("expected exactly 1 conflict, got %d: %+v", len(conflicts), conflicts)
	}
	c := conflicts[0]
	if c.ClaimedOwner != "Robustel" {
		t.Errorf("ClaimedOwner = %q, want %q", c.ClaimedOwner, "Robustel")
	}
	if c.ActualOwner != "Milesight" {
		t.Errorf("ActualOwner = %q, want %q", c.ActualOwner, "Milesight")
	}
	// product field matches the form as it appears in the yaml (canonical case).
	if c.Product != "EG71" {
		t.Errorf("Product = %q, want %q", c.Product, "EG71")
	}
}

func TestDetectAttributionConflicts_NoConflictWhenBrandMatchesProduct(t *testing.T) {
	cfg := fixtureAliases()
	// "Robustel EG5120" — EG5120 IS Robustel's product. No conflict.
	conflicts := cfg.DetectAttributionConflicts("Robustel EG5120 specs")
	if len(conflicts) != 0 {
		t.Fatalf("expected no conflict for correctly-attributed product, got %+v", conflicts)
	}
}

func TestDetectAttributionConflicts_NoBrandMentioned(t *testing.T) {
	cfg := fixtureAliases()
	// "EG71 datasheet" alone — no brand claim, no conflict to detect.
	conflicts := cfg.DetectAttributionConflicts("EG71 datasheet")
	if len(conflicts) != 0 {
		t.Fatalf("expected no conflict when no brand is claimed, got %+v", conflicts)
	}
}

func TestDetectAttributionConflicts_NoProductMentioned(t *testing.T) {
	cfg := fixtureAliases()
	conflicts := cfg.DetectAttributionConflicts("Robustel 公司简介")
	if len(conflicts) != 0 {
		t.Fatalf("expected no conflict when no product is mentioned, got %+v", conflicts)
	}
}

func TestDetectAttributionConflicts_BilingualBrandClaim(t *testing.T) {
	cfg := fixtureAliases()
	// Chinese brand form must trigger the same conflict.
	conflicts := cfg.DetectAttributionConflicts("鲁邦通的 EG71 怎么样")
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict via 鲁邦通 form, got %d: %+v", len(conflicts), conflicts)
	}
	if conflicts[0].ActualOwner != "Milesight" {
		t.Errorf("ActualOwner = %q, want Milesight", conflicts[0].ActualOwner)
	}
}

func TestDetectAttributionConflicts_MultipleConflicts(t *testing.T) {
	cfg := fixtureAliases()
	// Both EG71 (Milesight) and an explicit Robustel mention → conflict.
	// Add UG65 too — also Milesight, same conflict pair.
	conflicts := cfg.DetectAttributionConflicts("Robustel EG71 vs UG65")
	if len(conflicts) != 2 {
		t.Fatalf("expected 2 conflicts (EG71, UG65), got %d: %+v", len(conflicts), conflicts)
	}
	gotProducts := []string{conflicts[0].Product, conflicts[1].Product}
	sort.Strings(gotProducts)
	wantProducts := []string{"EG71", "UG65"}
	if !reflect.DeepEqual(gotProducts, wantProducts) {
		t.Errorf("products = %v, want %v", gotProducts, wantProducts)
	}
}

func TestDetectFormGroups_TechnologyGroupsExcluded(t *testing.T) {
	// Regression test: a query mentioning a technology form (e.g. "LoRa")
	// must NOT count as a brand anchor — that would make any chunk discussing
	// LoRa look "off-topic" for a different group and trigger a spurious
	// entity_mismatch on the brand's own datasheet.
	cfg := &EntityAliasConfig{
		Groups: []EntityAliasGroup{
			{Forms: []string{"Robustel", "鲁邦通"}, Products: []string{"R1520LG"}},
			{Forms: []string{"LoRaWAN", "LoRa"}, Kind: "technology"},
		},
	}
	cfg.Build()

	// Query that mentions only the technology form.
	got := cfg.DetectFormGroups("LG5100|R3000.*LG|EG.*LoRa")
	if len(got) != 0 {
		t.Fatalf("DetectFormGroups must skip technology groups, got %v", got)
	}

	// DetectGroups (the generous variant used for chunk scanning) STILL sees
	// technology groups — that's fine for downstream non-mismatch consumers.
	got = cfg.DetectGroups("LoRa technology overview")
	if _, ok := got[1]; !ok {
		t.Errorf("DetectGroups should still see technology mentions: got %v", got)
	}

	// Sanity: a real brand form still anchors correctly.
	got = cfg.DetectFormGroups("Robustel LG5100 LoRa")
	if _, ok := got[0]; !ok {
		t.Errorf("Robustel brand form must still anchor, got %v", got)
	}
	if _, ok := got[1]; ok {
		t.Errorf("LoRa technology must NOT anchor even when present, got %v", got)
	}
}

func TestDetectAttributionConflicts_TechnologyClaimIsNotABrandClaim(t *testing.T) {
	// "LoRa Robustel EG71" — LoRa is a technology, Robustel is a brand,
	// EG71 is Milesight's product. Only the Robustel↔EG71 conflict should
	// be flagged, NOT a "LoRa claimed but EG71 is Milesight" conflict.
	cfg := &EntityAliasConfig{
		Groups: []EntityAliasGroup{
			{Forms: []string{"Robustel", "鲁邦通"}, Products: []string{"EG5120"}},
			{Forms: []string{"Milesight"}, Products: []string{"EG71"}},
			{Forms: []string{"LoRaWAN", "LoRa"}, Kind: "technology"},
		},
	}
	cfg.Build()
	conflicts := cfg.DetectAttributionConflicts("LoRa Robustel EG71 pitch")
	if len(conflicts) != 1 {
		t.Fatalf("expected exactly 1 conflict (Robustel→EG71→Milesight), got %d: %+v", len(conflicts), conflicts)
	}
	if conflicts[0].ClaimedOwner != "Robustel" || conflicts[0].ActualOwner != "Milesight" {
		t.Errorf("unexpected conflict: %+v", conflicts[0])
	}
}

func TestExpand_DoesNotMixProductsIntoQuery(t *testing.T) {
	// Critical safety check: query expansion must NOT inject product model
	// numbers into BM25 queries (would corrupt retrieval). Only Form↔Form
	// expansion is allowed.
	cfg := fixtureAliases()
	seen := map[string]struct{}{}
	additions := cfg.Expand("Robustel 楼宇网关", seen)

	for _, a := range additions {
		switch a {
		case "EG5120", "R1511LG", "R1520LG", "RCMS", "EG71", "UG65":
			t.Errorf("Expand returned product %q — products MUST NOT enter query expansion", a)
		}
	}
	// 鲁邦通 (Robustel sibling form) is fine and expected.
	foundSibling := false
	for _, a := range additions {
		if a == "鲁邦通" {
			foundSibling = true
			break
		}
	}
	if !foundSibling {
		t.Errorf("expected 鲁邦通 in expansions, got %v", additions)
	}
}
