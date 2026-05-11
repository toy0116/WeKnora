package config

import (
	"reflect"
	"sort"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// fixtureWithYAMLBrand builds an EntityAliasConfig with Robustel declared
// in yaml, used as the baseline state for auto-discover tests.
func fixtureWithYAMLBrand() *EntityAliasConfig {
	cfg := &EntityAliasConfig{
		Groups: []EntityAliasGroup{
			{Forms: []string{"Robustel", "鲁邦通"}, Products: []string{"EG5120"}},
			{Forms: []string{"LoRaWAN", "LoRa"}, Kind: "technology"},
		},
	}
	cfg.Build()
	return cfg
}

func TestAutoDiscover_PromotesUndeclaredBrandAboveThreshold(t *testing.T) {
	cfg := fixtureWithYAMLBrand()

	// Three product pages all link to entity/teltonika as their first
	// outlink — high inDegree, title is brand-shaped, not in yaml. Should
	// be promoted.
	pages := []*types.WikiPage{
		mkPage("entity/teltonika", "Teltonika", []string{"Teltonika Networks"}, nil),
		mkPage("entity/rut906", "RUT906", nil, []string{"entity/teltonika"}),
		mkPage("entity/rut956", "RUT956", nil, []string{"entity/teltonika"}),
		mkPage("entity/trb500", "TRB500", nil, []string{"entity/teltonika"}),
	}

	added := cfg.AutoDiscoverFromWiki(pages, nil, 3)
	if added != 1 {
		t.Fatalf("added=%d, want 1", added)
	}

	rt := cfg.RuntimeGroups()
	var teltonika *EntityAliasGroup
	for i := range rt {
		if rt[i].Source == "wiki-auto" && rt[i].WikiSlug == "entity/teltonika" {
			teltonika = &rt[i]
			break
		}
	}
	if teltonika == nil {
		t.Fatalf("expected wiki-auto Teltonika group, runtime=%+v", rt)
	}
	// Forms should include the title + the alias.
	gotForms := append([]string(nil), teltonika.Forms...)
	sort.Strings(gotForms)
	wantForms := []string{"Teltonika", "Teltonika Networks"}
	sort.Strings(wantForms)
	if !reflect.DeepEqual(gotForms, wantForms) {
		t.Errorf("Forms = %v, want %v", gotForms, wantForms)
	}
	// Products are the three RUT/TRB pages.
	gotProducts := append([]string(nil), teltonika.Products...)
	sort.Strings(gotProducts)
	wantProducts := []string{"RUT906", "RUT956", "TRB500"}
	sort.Strings(wantProducts)
	if !reflect.DeepEqual(gotProducts, wantProducts) {
		t.Errorf("Products = %v, want %v", gotProducts, wantProducts)
	}
}

func TestAutoDiscover_BelowThresholdNotPromoted(t *testing.T) {
	cfg := fixtureWithYAMLBrand()
	// Only 2 products link to the candidate — below default threshold of 3.
	pages := []*types.WikiPage{
		mkPage("entity/quectel", "Quectel", nil, nil),
		mkPage("entity/eg91", "EG91", nil, []string{"entity/quectel"}),
		mkPage("entity/eg95", "EG95", nil, []string{"entity/quectel"}),
	}
	added := cfg.AutoDiscoverFromWiki(pages, nil, 3)
	if added != 0 {
		t.Errorf("expected no promotion below threshold, got added=%d", added)
	}
}

func TestAutoDiscover_SkipsYAMLDeclaredBrands(t *testing.T) {
	// Robustel is already in yaml → must NOT be re-promoted as wiki-auto.
	cfg := fixtureWithYAMLBrand()
	pages := []*types.WikiPage{
		mkPage("entity/robustel", "Robustel", []string{"鲁邦通"}, nil),
		mkPage("entity/r1520lg", "R1520LG", nil, []string{"entity/robustel"}),
		mkPage("entity/lg5100", "LG5100", nil, []string{"entity/robustel"}),
		mkPage("entity/eg5120", "EG5120", nil, []string{"entity/robustel"}),
	}
	added := cfg.AutoDiscoverFromWiki(pages, nil, 3)
	if added != 0 {
		t.Errorf("yaml-declared brand must not become wiki-auto, got added=%d runtime=%+v",
			added, cfg.RuntimeGroups())
	}
}

func TestAutoDiscover_SkipsTechnologyConcepts(t *testing.T) {
	// "RobustOS" is referenced by many products (it's the firmware they
	// all run on), but it's a technology, not a brand. Must NOT promote.
	cfg := fixtureWithYAMLBrand()
	pages := []*types.WikiPage{
		mkPage("entity/robustos", "RobustOS", nil, nil),
		mkPage("entity/r1520lg", "R1520LG", nil, []string{"entity/robustos"}),
		mkPage("entity/lg5100", "LG5100", nil, []string{"entity/robustos"}),
		mkPage("entity/eg5120", "EG5120", nil, []string{"entity/robustos"}),
		mkPage("entity/r3000", "R3000", nil, []string{"entity/robustos"}),
	}
	added := cfg.AutoDiscoverFromWiki(pages, nil, 3)
	if added != 0 {
		t.Errorf("technology entity must not be promoted, got added=%d", added)
	}
}

func TestAutoDiscover_SkipsYAMLTechnologyKindForms(t *testing.T) {
	// LoRaWAN is declared as Kind=technology in yaml; any wiki page with
	// that title must NOT become a brand even if it gets many product
	// outlinks.
	cfg := fixtureWithYAMLBrand()
	pages := []*types.WikiPage{
		mkPage("entity/lorawan", "LoRaWAN", nil, nil),
		mkPage("entity/eg5120", "EG5120", nil, []string{"entity/lorawan"}),
		mkPage("entity/r1520lg", "R1520LG", nil, []string{"entity/lorawan"}),
		mkPage("entity/lg5100", "LG5100", nil, []string{"entity/lorawan"}),
	}
	added := cfg.AutoDiscoverFromWiki(pages, nil, 3)
	if added != 0 {
		t.Errorf("yaml-technology form must not be promoted, got added=%d", added)
	}
}

func TestAutoDiscover_SkipsDenylistedSlugs(t *testing.T) {
	cfg := fixtureWithYAMLBrand()
	denylist := &AliasDenylistConfig{IgnoredSlugs: []string{"entity/aws-iot-core"}}
	denylist.Build()

	pages := []*types.WikiPage{
		mkPage("entity/aws-iot-core", "AWS IoT Core", nil, nil),
		mkPage("entity/eg5120", "EG5120", nil, []string{"entity/aws-iot-core"}),
		mkPage("entity/r1520lg", "R1520LG", nil, []string{"entity/aws-iot-core"}),
		mkPage("entity/lg5100", "LG5100", nil, []string{"entity/aws-iot-core"}),
		mkPage("entity/r3000", "R3000", nil, []string{"entity/aws-iot-core"}),
	}
	added := cfg.AutoDiscoverFromWiki(pages, denylist, 3)
	if added != 0 {
		t.Errorf("denylisted slug must not promote, got added=%d", added)
	}
}

func TestAutoDiscover_SkipsCandidateWhoseTitleIsSKU(t *testing.T) {
	// A SKU-shaped entity that happens to be linked from many other SKUs
	// (e.g. one product extends another) must not be misclassified as
	// brand.
	cfg := fixtureWithYAMLBrand()
	pages := []*types.WikiPage{
		mkPage("entity/eg5120", "EG5120", nil, nil),
		mkPage("entity/eg5120p", "EG5120P", nil, []string{"entity/eg5120"}),
		mkPage("entity/eg5121", "EG5121", nil, []string{"entity/eg5120"}),
		mkPage("entity/eg5125", "EG5125", nil, []string{"entity/eg5120"}),
	}
	added := cfg.AutoDiscoverFromWiki(pages, nil, 3)
	if added != 0 {
		t.Errorf("SKU-titled candidate must not become a brand, got added=%d", added)
	}
}

func TestAutoDiscover_PositionCutoffPreventsProtocolPromotion(t *testing.T) {
	// Protocol entities like LoRaWAN concept may be linked by many products
	// but always deep in OutLinks. Since AutoDiscover honors the same
	// brandLinkPositionCutoff (5) the false positive is filtered.
	cfg := fixtureWithYAMLBrand()
	// Synthetic non-tech entity whose page would otherwise pass title
	// filters, but every product links to it at index 5+ (past cutoff).
	pages := []*types.WikiPage{
		mkPage("entity/myco", "MyCo Inc", nil, nil),
		mkPage("entity/p1", "P1500", nil, []string{
			"concept/a", "concept/b", "concept/c", "concept/d", "concept/e",
			"entity/myco",
		}),
		mkPage("entity/p2", "P2500", nil, []string{
			"concept/a", "concept/b", "concept/c", "concept/d", "concept/e",
			"entity/myco",
		}),
		mkPage("entity/p3", "P3500", nil, []string{
			"concept/a", "concept/b", "concept/c", "concept/d", "concept/e",
			"entity/myco",
		}),
	}
	added := cfg.AutoDiscoverFromWiki(pages, nil, 3)
	if added != 0 {
		t.Errorf("brand link past cutoff must not contribute to inDegree, got added=%d runtime=%+v",
			added, cfg.RuntimeGroups())
	}
}

func TestAutoDiscover_DoesNotMutateYAMLGroups(t *testing.T) {
	cfg := fixtureWithYAMLBrand()
	preYAMLForms := append([]string(nil), cfg.Groups[0].Forms...)
	preYAMLProducts := append([]string(nil), cfg.Groups[0].Products...)

	pages := []*types.WikiPage{
		mkPage("entity/teltonika", "Teltonika", nil, nil),
		mkPage("entity/rut906", "RUT906", nil, []string{"entity/teltonika"}),
		mkPage("entity/rut956", "RUT956", nil, []string{"entity/teltonika"}),
		mkPage("entity/trb500", "TRB500", nil, []string{"entity/teltonika"}),
	}
	cfg.AutoDiscoverFromWiki(pages, nil, 3)

	if !reflect.DeepEqual(cfg.Groups[0].Forms, preYAMLForms) {
		t.Errorf("Groups[0].Forms mutated: %v vs %v", cfg.Groups[0].Forms, preYAMLForms)
	}
	if !reflect.DeepEqual(cfg.Groups[0].Products, preYAMLProducts) {
		t.Errorf("Groups[0].Products mutated: %v vs %v", cfg.Groups[0].Products, preYAMLProducts)
	}
}

func TestAutoDiscover_DenylistAppendRoundTrip(t *testing.T) {
	dl := &AliasDenylistConfig{}
	dl.Build()
	if dl.IsIgnored("entity/x") {
		t.Errorf("empty denylist must not match anything")
	}

	// Append uses persist() which writes to dl.path; tests leave path empty
	// → persist is a no-op, but the in-memory state still updates.
	if err := dl.Append("entity/aws-iot-core"); err != nil {
		t.Fatalf("Append failed: %v", err)
	}
	if !dl.IsIgnored("entity/aws-iot-core") {
		t.Errorf("after Append, slug must be ignored")
	}
	// Idempotent — adding the same slug again returns nil and doesn't
	// duplicate.
	if err := dl.Append("entity/aws-iot-core"); err != nil {
		t.Errorf("second Append failed: %v", err)
	}
	if len(dl.IgnoredSlugs) != 1 {
		t.Errorf("expected 1 entry after duplicate Append, got %d", len(dl.IgnoredSlugs))
	}
	// Remove drops the entry.
	if err := dl.Remove("entity/aws-iot-core"); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}
	if dl.IsIgnored("entity/aws-iot-core") {
		t.Errorf("after Remove, slug must not be ignored")
	}
}
