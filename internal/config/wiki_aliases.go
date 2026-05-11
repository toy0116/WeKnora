// Package config — merge wiki entity pages into the entity-alias config.
//
// Why this exists (档2 of the maintenance-burden reduction):
//   The wiki already encodes brand↔alias and brand→product relationships:
//   - An entity page's Title + Aliases list defines the entity's names.
//   - An entity page's OutLinks include links to a parent brand entity
//     when the page is about a product owned by that brand.
//   Maintaining the same data again in entity_aliases.yaml is duplicate
//   bookkeeping. This file lets the wiki be the source of truth: yaml
//   declares which brand groups exist (which entities to track), the
//   wiki contributes the actual aliases and products.
//
// Resolution behaviour:
//   - yaml is the "declared brands" list; only groups already in yaml
//     can be augmented. Wiki-only entities are ignored to keep the
//     surface explicit.
//   - Forms added from wiki are additive — yaml-declared forms stay,
//     wiki contributes alternates we haven't seen yet.
//   - Products added from wiki are also additive. They're discovered
//     by following OutLinks from product entity pages back to the
//     brand page.
//   - The Kind classification (brand / technology) stays governed by
//     yaml. Technology groups are not augmented from wiki since the
//     OutLinks-based product discovery doesn't apply.

package config

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// MergeFromWiki augments runtimeGroups (NOT yaml-on-disk Groups) with
// aliases + products harvested from wiki entity pages. Pages should already
// be filtered to PageType="entity" — see main.go bootstrap for the
// enumeration code.
//
// The yaml-on-disk Groups slice is left UNTOUCHED. The Web-UI settings
// page reads Groups for display and writes it on save, so wiki-discovered
// SKUs never accidentally get persisted into entity_aliases.yaml. The
// retrieval pipeline reads runtimeGroups via DetectGroups /
// DetectFormGroups / DetectAttributionConflicts, so it sees the merged
// view.
//
// Returns counts (forms_added, products_added) so callers can log how
// much the merge contributed. Safe to call multiple times — duplicates
// against runtimeGroups entries (which include both yaml + prior wiki
// additions) are skipped.
//
// Typically invoked indirectly via c.Build() through the RuntimeRefresh
// callback that main.go bootstrap installs. Direct callers must
// initialise runtimeGroups first (call c.Build() once before calling
// MergeFromWiki).
//
// Algorithm (per BRAND group declared in yaml):
//  1. Find wiki entity pages whose Title OR Aliases matches any of the
//     group's existing forms (case-insensitive whole-string).
//     These are the brand's own wiki pages.
//  2. Harvest those pages' Title + Aliases as candidate new forms.
//     Dedup against runtimeGroups[gi].Forms; add the rest there.
//  3. Build the brand-slug set and scan again: entity pages with
//     OutLinks targeting a brand-slug at position 0–4 AND a SKU-shaped
//     title AND not on the protocol denylist become product candidates.
//  4. Dedup against runtimeGroups[gi].Products; add the rest there.
func (c *EntityAliasConfig) MergeFromWiki(pages []*types.WikiPage) (formsAdded, productsAdded int) {
	if c == nil || len(c.Groups) == 0 || len(pages) == 0 {
		return 0, 0
	}
	// If Build hasn't been called yet, runtimeGroups is empty — bootstrap
	// it from Groups so the merge has something to write to. Normal flow
	// goes through Build → RuntimeRefresh → MergeFromWiki, so runtimeGroups
	// is already populated; this branch handles direct test callers.
	if len(c.runtimeGroups) != len(c.Groups) {
		c.runtimeGroups = make([]EntityAliasGroup, len(c.Groups))
		for i, g := range c.Groups {
			c.runtimeGroups[i] = EntityAliasGroup{
				Forms:    append([]string(nil), g.Forms...),
				Products: append([]string(nil), g.Products...),
				Kind:     g.Kind,
			}
		}
	}

	// Index pages by slug for O(1) lookup.
	bySlug := make(map[string]*types.WikiPage, len(pages))
	for _, p := range pages {
		if p == nil || p.Slug == "" {
			continue
		}
		bySlug[p.Slug] = p
	}

	for gi := range c.Groups {
		// Mutating target — the runtime view.
		g := &c.runtimeGroups[gi]
		if !g.IsBrand() {
			// Technology / concept groups don't carry products and the
			// OutLinks-based discovery doesn't apply.
			continue
		}

		// Lower-cased existing forms for membership tests.
		existingForms := make(map[string]struct{}, len(g.Forms))
		for _, f := range g.Forms {
			existingForms[strings.ToLower(f)] = struct{}{}
		}
		existingProducts := make(map[string]struct{}, len(g.Products))
		for _, p := range g.Products {
			existingProducts[strings.ToLower(p)] = struct{}{}
		}

		// Step 1 + 2: find wiki pages that are about this brand.
		brandSlugs := make(map[string]struct{})
		for _, p := range pages {
			if p == nil {
				continue
			}
			if !pageMatchesAnyForm(p, g.Forms) {
				continue
			}
			brandSlugs[p.Slug] = struct{}{}

			// Step 3: harvest the brand page's title + aliases as forms.
			for _, cand := range candidateFormsFromPage(p) {
				key := strings.ToLower(cand)
				if _, ok := existingForms[key]; ok {
					continue
				}
				g.Forms = append(g.Forms, cand)
				existingForms[key] = struct{}{}
				formsAdded++
			}
		}

		if len(brandSlugs) == 0 {
			continue // No wiki entity matches this group — nothing to merge.
		}

		// Step 4 + 5: any entity page whose OutLinks point at a known
		// brand slug AND whose title looks like a SKU is a product.
		for _, p := range pages {
			if p == nil {
				continue
			}
			// Don't re-classify the brand pages themselves as products.
			if _, isBrand := brandSlugs[p.Slug]; isBrand {
				continue
			}
			if !linksIntoBrand(p, brandSlugs) {
				continue
			}
			for _, cand := range candidateProductsFromPage(p) {
				key := strings.ToLower(cand)
				if _, ok := existingProducts[key]; ok {
					continue
				}
				g.Products = append(g.Products, cand)
				existingProducts[key] = struct{}{}
				productsAdded++
			}
		}
	}

	return formsAdded, productsAdded
}

// pageMatchesAnyForm returns true when any of the brand's declared forms
// appears in the page's title or aliases (case-insensitive, whole-string).
func pageMatchesAnyForm(p *types.WikiPage, forms []string) bool {
	if p == nil {
		return false
	}
	titleLower := strings.ToLower(p.Title)
	aliasesLower := make([]string, 0, len(p.Aliases))
	for _, a := range p.Aliases {
		aliasesLower = append(aliasesLower, strings.ToLower(a))
	}
	for _, f := range forms {
		fl := strings.ToLower(f)
		if fl == "" {
			continue
		}
		if titleLower == fl {
			return true
		}
		for _, a := range aliasesLower {
			if a == fl {
				return true
			}
		}
	}
	return false
}

// candidateFormsFromPage returns Title + Aliases of a wiki page as a
// candidate-forms list. Empty strings + single-rune aliases are filtered.
//
// The single-rune filter defends against degenerate wiki entity-extraction
// output that captures a stray letter (e.g. "M" got auto-extracted as a
// Milesight alias from a sentence-initial letter "Milesight…"). Real
// brand names are never one character long — ABB / ZTE / 星纵 etc. are
// all ≥ 2.
func candidateFormsFromPage(p *types.WikiPage) []string {
	if p == nil {
		return nil
	}
	out := make([]string, 0, 1+len(p.Aliases))
	if t := strings.TrimSpace(p.Title); t != "" && runeLen(t) >= 2 {
		out = append(out, t)
	}
	for _, a := range p.Aliases {
		a = strings.TrimSpace(a)
		if a != "" && runeLen(a) >= 2 {
			out = append(out, a)
		}
	}
	return out
}

// runeLen returns the rune count of s — needed because single-CJK-char
// strings have len(s) ≥ 3 (UTF-8) but should be rune-counted as 1.
func runeLen(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}

// linksIntoBrand returns true when one of the page's FIRST FEW outlinks
// targets a brand slug. The position check is the key filter against false
// positives: a real product's first outlink is its parent brand (or a
// closely-related entity), while a shared protocol like L2TP or RS485
// links to other protocols first and only mentions brands later down its
// outlinks list because those brands happen to use the protocol.
//
// Production data observation: the actual product pages (LG5100, R1520LG,
// Robustel R1511e) have their brand entity at OutLinks index 0–4. The
// protocol pages (entity/l2tp, entity/rs485, entity/ip30) have concepts
// at the top of their outlinks; brand mentions appear at index 7+, if at
// all. Threshold of 5 catches all observed product cases and filters all
// observed protocol false positives.
const brandLinkPositionCutoff = 5

func linksIntoBrand(p *types.WikiPage, brandSlugs map[string]struct{}) bool {
	for i, link := range p.OutLinks {
		if i >= brandLinkPositionCutoff {
			return false
		}
		if _, ok := brandSlugs[link]; ok {
			return true
		}
	}
	return false
}

// productTitleDenylist filters out widely-cited protocols, standards, and
// IT acronyms that pattern-match as SKUs (e.g. "L2TP" has letters+digits)
// but are obviously not vendor products. Kept small and conservative —
// only the false positives we've actually observed in production.
var productTitleDenylist = map[string]struct{}{
	// VPN / tunneling protocols
	"L2TP": {}, "PPTP": {}, "IPSec": {}, "GRE": {}, "OpenVPN": {}, "WireGuard": {},
	// Wireless security
	"WPA": {}, "WPA2": {}, "WPA3": {}, "WEP": {}, "AES": {}, "TKIP": {},
	// Serial / physical interfaces
	"RS232": {}, "RS-232": {}, "RS485": {}, "RS-485": {}, "RS422": {}, "RS-422": {},
	"USB2": {}, "USB3": {},
	// Ingress protection ratings
	"IP30": {}, "IP54": {}, "IP65": {}, "IP67": {}, "IP68": {},
	// Cellular / radio
	"LTE": {}, "GSM": {}, "WCDMA": {}, "GPRS": {}, "EDGE": {}, "HSPA": {},
	"4G": {}, "5G": {}, "3G": {}, "2G": {},
	// Networking
	"TCP": {}, "UDP": {}, "DHCP": {}, "DNS": {}, "NAT": {}, "VPN": {}, "VLAN": {},
	"HTTPS": {}, "MQTT": {}, "SNMP": {},
	// SoC / commodity hardware references
	"RK3562": {}, "RK3568": {}, "RK3588": {}, "MT7621": {}, "MT7981": {},
}

// skuPattern is the heuristic for "this entity page's title is a product
// SKU". Matches things like "R1520LG", "EG5120", "LG5100", "RCMS",
// "R3000 LG", "EV8100", "Robustel R1511e". Conservative — must contain
// at least one digit and start with a capital letter to filter out plain
// concept entities like "Ethernet" or "RobustOS".
var skuPattern = regexp.MustCompile(`^[A-Z][A-Za-z0-9]*\d[A-Za-z0-9 -]*$|^[A-Z][a-z]+ +[A-Z][A-Za-z0-9]*\d`)

// isLikelyProduct returns true when a candidate title passes both the
// SKU shape pattern and the protocol/standard denylist filter.
func isLikelyProduct(s string) bool {
	if s == "" || !skuPattern.MatchString(s) {
		return false
	}
	if _, denied := productTitleDenylist[s]; denied {
		return false
	}
	// Case-folded variants of the denylist — covers "L2tp", "wpa2" etc.
	if _, denied := productTitleDenylist[strings.ToUpper(s)]; denied {
		return false
	}
	return true
}

// candidateProductsFromPage returns the page's title (and SKU-shaped
// aliases) when they look like product SKUs and aren't on the
// protocol/standard denylist.
func candidateProductsFromPage(p *types.WikiPage) []string {
	if p == nil {
		return nil
	}
	var out []string
	title := strings.TrimSpace(p.Title)
	if isLikelyProduct(title) {
		out = append(out, title)
	}
	// Aliases that are pure SKUs are useful too (e.g. an alias list
	// containing the bare model number alongside the longer marketing
	// name).
	for _, a := range p.Aliases {
		a = strings.TrimSpace(a)
		if !isLikelyProduct(a) {
			continue
		}
		// Skip if equal to title (already added).
		if strings.EqualFold(a, title) {
			continue
		}
		out = append(out, a)
	}
	return out
}

// MergeSummary captures merge stats for logging.
type MergeSummary struct {
	FormsAdded    int
	ProductsAdded int
}

// String formats a MergeSummary for log output.
func (s MergeSummary) String() string {
	return fmt.Sprintf("forms+%d products+%d", s.FormsAdded, s.ProductsAdded)
}
