package config

import (
	"reflect"
	"sort"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// Helper: build a minimal wiki entity page.
func mkPage(slug, title string, aliases []string, outLinks []string) *types.WikiPage {
	return &types.WikiPage{
		Slug:     slug,
		Title:    title,
		PageType: "entity",
		Aliases:  types.StringArray(aliases),
		OutLinks: types.StringArray(outLinks),
	}
}

func TestMergeFromWiki_AddsAliasesToMatchedBrand(t *testing.T) {
	cfg := &EntityAliasConfig{
		Groups: []EntityAliasGroup{
			{Forms: []string{"Robustel"}}, // declared brand, no Chinese form yet
		},
	}
	cfg.Build()

	pages := []*types.WikiPage{
		// Brand page: matches Forms[0] "Robustel" via title. Its own aliases
		// contribute new forms.
		mkPage("entity/robustel", "Robustel", []string{"鲁邦通", "Guangzhou Robustel Ltd"}, nil),
	}

	formsAdded, productsAdded := cfg.MergeFromWiki(pages)
	if formsAdded != 2 {
		t.Errorf("formsAdded = %d, want 2", formsAdded)
	}
	if productsAdded != 0 {
		t.Errorf("productsAdded = %d, want 0", productsAdded)
	}

	got := cfg.Groups[0].Forms
	sort.Strings(got)
	want := []string{"Guangzhou Robustel Ltd", "Robustel", "鲁邦通"}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Forms after merge = %v, want %v", got, want)
	}
}

func TestMergeFromWiki_AddsProductsViaOutLinks(t *testing.T) {
	cfg := &EntityAliasConfig{
		Groups: []EntityAliasGroup{
			{Forms: []string{"Robustel", "鲁邦通"}, Products: []string{"R1520LG"}},
		},
	}
	cfg.Build()

	pages := []*types.WikiPage{
		mkPage("entity/robustel", "Robustel", []string{"鲁邦通"}, nil),
		// Product pages: their OutLinks point at the brand page, AND their
		// titles look like SKUs → become products under this brand.
		mkPage("entity/lg5100", "LG5100", nil, []string{"entity/robustel"}),
		mkPage("entity/eg5120p", "EG5120P", nil, []string{"entity/robustel"}),
		mkPage("entity/robustel-r1511e", "Robustel R1511e", []string{"R1511e"}, []string{"entity/robustel"}),
		// Non-product page: title is a concept, not a SKU. Even though it
		// links to the brand it must NOT be added as a product.
		mkPage("entity/lorawan-gateway", "LoRaWAN Gateway", nil, []string{"entity/robustel"}),
	}

	_, productsAdded := cfg.MergeFromWiki(pages)
	if productsAdded < 3 {
		t.Errorf("productsAdded = %d, want >= 3 (LG5100, EG5120P, R1511e)", productsAdded)
	}

	gotProducts := cfg.Groups[0].Products
	contains := func(needle string) bool {
		for _, p := range gotProducts {
			if p == needle {
				return true
			}
		}
		return false
	}
	for _, want := range []string{"LG5100", "EG5120P", "R1511e", "R1520LG"} {
		if !contains(want) {
			t.Errorf("Products missing %q, got %v", want, gotProducts)
		}
	}
	if contains("LoRaWAN Gateway") {
		t.Errorf("LoRaWAN Gateway should not be added as a product, got %v", gotProducts)
	}
}

func TestMergeFromWiki_DoesNotTouchTechnologyGroups(t *testing.T) {
	// Wiki has lots of "concept" pages that are technologies; they must
	// not be auto-promoted to brand-or-technology groups in yaml. The
	// merge function only augments existing brand groups.
	cfg := &EntityAliasConfig{
		Groups: []EntityAliasGroup{
			{Forms: []string{"LoRaWAN", "LoRa"}, Kind: "technology"},
		},
	}
	cfg.Build()

	pages := []*types.WikiPage{
		mkPage("entity/lorawan", "LoRaWAN", []string{"LoRa WAN", "low-power-wide-area"}, nil),
	}
	formsAdded, productsAdded := cfg.MergeFromWiki(pages)
	if formsAdded != 0 || productsAdded != 0 {
		t.Errorf("technology group should be skipped, got forms+%d products+%d",
			formsAdded, productsAdded)
	}
}

func TestMergeFromWiki_DedupesAgainstYAML(t *testing.T) {
	// yaml already has "鲁邦通" + "Robustel"; wiki repeats them.
	// Merge must not produce duplicates.
	cfg := &EntityAliasConfig{
		Groups: []EntityAliasGroup{
			{Forms: []string{"鲁邦通", "Robustel"}, Products: []string{"EG5120"}},
		},
	}
	cfg.Build()
	pages := []*types.WikiPage{
		mkPage("entity/robustel", "Robustel", []string{"鲁邦通"}, nil),
		// Product whose title duplicates an existing yaml product.
		mkPage("entity/eg5120", "EG5120", nil, []string{"entity/robustel"}),
	}
	formsAdded, productsAdded := cfg.MergeFromWiki(pages)
	if formsAdded != 0 {
		t.Errorf("formsAdded = %d, want 0 (everything already in yaml)", formsAdded)
	}
	if productsAdded != 0 {
		t.Errorf("productsAdded = %d, want 0 (everything already in yaml)", productsAdded)
	}
	// Original list should be untouched.
	if len(cfg.Groups[0].Forms) != 2 || len(cfg.Groups[0].Products) != 1 {
		t.Errorf("groups mutated: forms=%v products=%v",
			cfg.Groups[0].Forms, cfg.Groups[0].Products)
	}
}

func TestMergeFromWiki_NoMatchingPageLeavesGroupAlone(t *testing.T) {
	cfg := &EntityAliasConfig{
		Groups: []EntityAliasGroup{
			{Forms: []string{"Honeywell"}}, // declared brand
		},
	}
	cfg.Build()

	pages := []*types.WikiPage{
		mkPage("entity/robustel", "Robustel", nil, nil),
		mkPage("entity/lg5100", "LG5100", nil, []string{"entity/robustel"}),
	}
	formsAdded, productsAdded := cfg.MergeFromWiki(pages)
	if formsAdded != 0 || productsAdded != 0 {
		t.Errorf("unmatched group must not grow, got forms+%d products+%d",
			formsAdded, productsAdded)
	}
}

func TestMergeFromWiki_SKURegexFiltersConceptTitles(t *testing.T) {
	// candidateProductsFromPage uses skuPattern to filter — verify a
	// pure concept title doesn't slip through even when linked to a brand.
	cfg := &EntityAliasConfig{
		Groups: []EntityAliasGroup{{Forms: []string{"Robustel"}}},
	}
	cfg.Build()

	pages := []*types.WikiPage{
		mkPage("entity/robustel", "Robustel", nil, nil),
		mkPage("entity/ethernet", "Ethernet", nil, []string{"entity/robustel"}),
		mkPage("entity/robustos", "RobustOS", nil, []string{"entity/robustel"}),
		mkPage("entity/r1520lg", "R1520LG", nil, []string{"entity/robustel"}),
	}
	_, productsAdded := cfg.MergeFromWiki(pages)
	if productsAdded != 1 {
		t.Errorf("only R1520LG should be a product, got productsAdded=%d products=%v",
			productsAdded, cfg.Groups[0].Products)
	}
}

func TestMergeFromWiki_RejectsProtocolEntitiesByDenylist(t *testing.T) {
	// L2TP, WPA2, IP30, RS485 are "entity" pages in production wiki but
	// they're protocols/standards, not products. Even when their outLinks
	// include the brand entity (because the brand uses the protocol), they
	// must NOT be promoted to products.
	cfg := &EntityAliasConfig{
		Groups: []EntityAliasGroup{{Forms: []string{"Robustel"}}},
	}
	cfg.Build()

	pages := []*types.WikiPage{
		mkPage("entity/robustel", "Robustel", nil, nil),
		mkPage("entity/l2tp", "L2TP", []string{"Layer 2 Tunneling Protocol"}, []string{"entity/robustel"}),
		mkPage("entity/wpa2", "WPA2", nil, []string{"entity/robustel"}),
		mkPage("entity/ip30", "IP30", nil, []string{"entity/robustel"}),
		mkPage("entity/rs485", "RS485", nil, []string{"entity/robustel"}),
		mkPage("entity/rk3562", "RK3562", nil, []string{"entity/robustel"}),
		// A real product mixed in — must still pass.
		mkPage("entity/lg5100", "LG5100", nil, []string{"entity/robustel"}),
	}
	_, productsAdded := cfg.MergeFromWiki(pages)
	if productsAdded != 1 {
		t.Errorf("only LG5100 should pass the denylist; got productsAdded=%d products=%v",
			productsAdded, cfg.Groups[0].Products)
	}
}

func TestMergeFromWiki_RejectsBrandLinkBuriedDeepInOutLinks(t *testing.T) {
	// Position-aware filter: a protocol entity whose outLinks DO contain
	// the brand, but only after a long list of concept links, must NOT
	// be promoted. Mirrors production data for entity/l2tp where
	// entity/milesight appears at index ~7.
	cfg := &EntityAliasConfig{
		Groups: []EntityAliasGroup{{Forms: []string{"Robustel"}}},
	}
	cfg.Build()

	// Fake SKU title (passes regex + not in denylist) but brand link is at
	// index 6 — past the cutoff. Must be rejected.
	pages := []*types.WikiPage{
		mkPage("entity/robustel", "Robustel", nil, nil),
		mkPage("entity/lg5100", "LG5100", nil, []string{"entity/robustel"}),
		mkPage("entity/x4500", "X4500", nil, []string{
			"concept/a", "concept/b", "concept/c", "concept/d", "concept/e",
			"concept/f", "entity/robustel", // index 6 — too deep
		}),
	}
	_, productsAdded := cfg.MergeFromWiki(pages)
	if productsAdded != 1 {
		t.Errorf("only LG5100 should pass position check; got productsAdded=%d products=%v",
			productsAdded, cfg.Groups[0].Products)
	}
}

func TestMergeFromWiki_NilSafeAndEmptyInputs(t *testing.T) {
	var nilCfg *EntityAliasConfig
	if f, p := nilCfg.MergeFromWiki([]*types.WikiPage{}); f != 0 || p != 0 {
		t.Errorf("nil cfg must be no-op")
	}

	cfg := &EntityAliasConfig{Groups: []EntityAliasGroup{{Forms: []string{"X"}}}}
	cfg.Build()
	if f, p := cfg.MergeFromWiki(nil); f != 0 || p != 0 {
		t.Errorf("nil pages must be no-op")
	}
	if f, p := cfg.MergeFromWiki([]*types.WikiPage{}); f != 0 || p != 0 {
		t.Errorf("empty pages must be no-op")
	}
}
