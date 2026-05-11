// Package config — wiki brand auto-discovery.
//
// Second pass over wiki entity pages (after MergeFromWiki augments
// yaml-declared groups). Finds candidate brand entities that aren't yet
// declared in yaml and adds them as runtime-only "wiki-auto" groups so:
//
//   - DetectGroups / DetectFormGroups / DetectAttributionConflicts pick
//     them up immediately for the entity-mismatch checks (no yaml edit
//     required to start tracking a newly-encountered competitor).
//
//   - The Web UI surfaces them in a dedicated read-only section with an
//     ❌ Ignore button (writes to entity_alias_denylist.yaml) so the
//     user can suppress false positives without touching yaml.
//
// Brand-identification heuristic (inDegree-based):
//
//	A wiki entity X is a brand candidate iff:
//	  1. ≥ N product-shaped entity pages list X among their first
//	     `brandLinkPositionCutoff` (5) OutLinks. (N defaults to 3.)
//	  2. X.Title is NOT itself SKU-shaped (a product can't be a brand).
//	  3. X.Title is NOT in the technology/concept denylist (LoRaWAN,
//	     IoT, MQTT, RobustOS, etc.).
//	  4. X is NOT covered by an existing yaml-declared brand group
//	     (whose Forms match X.Title / X.Aliases).
//	  5. X.Slug is NOT in the user denylist (AliasDenylistConfig).
//
// The threshold + position cutoff combination filters out the two big
// false-positive families seen in real wiki data:
//   - protocols (L2TP, RS485): many products mention them but they're
//     deep in OutLinks (past position cutoff) → caught by linksIntoBrand
//   - tech concepts (RobustOS, LoRaWAN): listed early in product OutLinks
//     and pass the inDegree check, but caught by technology denylist.

package config

import (
	"regexp"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// DefaultAutoDiscoverThreshold is how many product pages must independently
// point at a candidate before it gets promoted to a brand group. Chosen as 3
// to balance precision (don't promote 1-2 mention coincidences) against
// recall (don't miss small competitor brands with only a few product pages).
const DefaultAutoDiscoverThreshold = 3

// AutoDiscoverFromWiki appends wiki-auto-discovered brand groups to
// runtimeGroups. Yaml Groups is NEVER modified — auto-discovered brands
// are runtime-only by design (the user can promote them to yaml manually
// if they want to customise the entry).
//
// Returns the count of new groups added. Idempotent across multiple calls
// in the same Build cycle as long as the same denylist and pages are passed.
//
// `threshold` controls the minimum inDegree; pass 0 to use the default.
func (c *EntityAliasConfig) AutoDiscoverFromWiki(
	pages []*types.WikiPage,
	denylist *AliasDenylistConfig,
	threshold int,
) (added int) {
	if c == nil || len(pages) == 0 {
		return 0
	}
	if threshold <= 0 {
		threshold = DefaultAutoDiscoverThreshold
	}

	// Ensure runtimeGroups exists. If callers invoke this directly without
	// going through Build (e.g. tests), bootstrap from Groups.
	if len(c.runtimeGroups) == 0 && len(c.Groups) > 0 {
		c.runtimeGroups = make([]EntityAliasGroup, len(c.Groups))
		for i, g := range c.Groups {
			c.runtimeGroups[i] = EntityAliasGroup{
				Forms:    append([]string(nil), g.Forms...),
				Products: append([]string(nil), g.Products...),
				Kind:     g.Kind,
			}
		}
	}

	// Pre-build the set of slugs already covered by yaml-declared brand
	// groups (so we don't auto-duplicate what MergeFromWiki already
	// handles).
	yamlBrandSlugs := collectYamlBrandSlugs(c.Groups, pages)

	// Pass 1: count outLink targets from product-shaped pages. Apply the
	// position cutoff so a protocol page being mentioned 10 outlinks deep
	// by many products doesn't accumulate fake inDegree.
	inDegree := make(map[string]int)
	productsBySlug := make(map[string][]string) // target slug → product titles
	for _, p := range pages {
		if p == nil || !isLikelyProduct(p.Title) {
			continue
		}
		for i, link := range p.OutLinks {
			if i >= brandLinkPositionCutoff {
				break
			}
			inDegree[link]++
			productsBySlug[link] = append(productsBySlug[link], p.Title)
		}
	}

	// Pass 2: promote candidates that pass every filter.
	bySlug := make(map[string]*types.WikiPage, len(pages))
	for _, p := range pages {
		if p != nil && p.Slug != "" {
			bySlug[p.Slug] = p
		}
	}

	for slug, count := range inDegree {
		if count < threshold {
			continue
		}
		if _, alreadyYaml := yamlBrandSlugs[slug]; alreadyYaml {
			continue
		}
		if denylist != nil && denylist.IsIgnored(slug) {
			continue
		}
		page := bySlug[slug]
		if page == nil {
			// Slug was referenced but the page itself isn't in our
			// PageType=entity scan (e.g. concept/ slugs). Skip.
			continue
		}
		if isLikelyProduct(page.Title) {
			continue // candidate is itself another product
		}
		if c.isKnownTechnology(page.Title) {
			continue
		}

		forms := candidateFormsFromPage(page)
		if len(forms) == 0 {
			continue
		}
		products := uniqueStrings(productsBySlug[slug])

		c.runtimeGroups = append(c.runtimeGroups, EntityAliasGroup{
			Forms:    forms,
			Products: products,
			Kind:     "brand",
			Source:   "wiki-auto",
			WikiSlug: slug,
		})
		added++
	}
	return added
}

// collectYamlBrandSlugs returns the wiki slugs already covered by an existing
// yaml brand group. Membership = title or alias matches any of the yaml
// group's Forms (case-insensitive whole-string).
func collectYamlBrandSlugs(groups []EntityAliasGroup, pages []*types.WikiPage) map[string]struct{} {
	yamlForms := make(map[string]struct{})
	for _, g := range groups {
		if !g.IsBrand() {
			continue
		}
		for _, f := range g.Forms {
			if f != "" {
				yamlForms[strings.ToLower(f)] = struct{}{}
			}
		}
	}
	out := make(map[string]struct{})
	for _, p := range pages {
		if p == nil || p.Slug == "" {
			continue
		}
		for _, cand := range candidateFormsFromPage(p) {
			if _, ok := yamlForms[strings.ToLower(cand)]; ok {
				out[p.Slug] = struct{}{}
				break
			}
		}
	}
	return out
}

// isKnownTechnology reports whether the title is a protocol / standard /
// concept / region / certification / generic-noun entity that should never
// become a brand, regardless of inDegree from product pages.
//
// Filter layers, evaluated in order:
//  1. yaml-declared technology groups (Kind=technology) — user-extensible.
//  2. nonBrandTitleSet — hardcoded common false-positive titles seen in
//     production wiki data (chipset vendors, generic doc-structure labels,
//     cellular generations, components).
//  3. nonBrandPatterns — regex catch-all for cellular generations, region
//     codes, IEC/EN/IP certifications, version strings, etc.
func (c *EntityAliasConfig) isKnownTechnology(title string) bool {
	// Layer 1: yaml technology groups.
	for _, g := range c.Groups {
		if g.IsBrand() {
			continue
		}
		for _, f := range g.Forms {
			if strings.EqualFold(f, title) {
				return true
			}
		}
	}
	// Layer 2: hardcoded denylist of common non-brand titles.
	if _, ok := nonBrandTitleSet[title]; ok {
		return true
	}
	// Layer 3: regex patterns (cellular gens, region codes, certifications).
	for _, re := range nonBrandPatterns {
		if re.MatchString(title) {
			return true
		}
	}
	return false
}

// nonBrandTitleSet catalogues common wiki entity titles that pass the
// inDegree-from-products test but are not brands. Calibrated against the
// 55 candidates the v1 heuristic surfaced on the real KB (Teltonika was
// the one true positive; the other 54 fell into one of these buckets).
var nonBrandTitleSet = map[string]struct{}{
	// Generic networking / cellular concepts
	"Device": {}, "Sensor": {}, "Router": {}, "Firmware": {}, "Cellular": {},
	"TCP": {}, "UDP": {}, "WAN": {}, "LAN": {}, "WLAN": {},
	"NFC": {}, "eSIM": {}, "GPS": {}, "RAM": {}, "Flash": {},
	"Ethernet": {}, "Wi-Fi": {}, "Bluetooth": {},
	"Dimension": {}, "Weight": {}, "Specifications": {},

	// Cellular generations / radio access tech
	"LTE": {}, "LTE FDD": {}, "LTE TDD": {}, "GPRS": {}, "GSM": {}, "UMTS": {},
	"WCDMA": {}, "HSPA": {}, "EDGE": {}, "5G NR": {},
	"4G/3G/2G": {}, "4G/LTE": {}, "4G LTE": {}, "Cat M1": {},

	// Doc structure / generic catalogue terms
	"规格": {}, "订购信息": {}, "尺寸": {}, "重量": {}, "Ordering Information": {},

	// Operating systems / software stacks
	"RobustOS": {}, "RobustOS Pro": {}, "RobustVPN": {}, "RobustLink": {},
	"Operation Console": {},
	"Linux": {}, "Debian": {}, "Ubuntu": {}, "Docker": {},
	"Python": {}, "Node.js": {}, "Java": {}, "C": {}, "C++": {},
	"WeKnora": {}, "Neo4j": {}, "Vue3": {}, "S3": {},
	"ChirpStack": {}, "Node-RED": {}, "Niagara": {},

	// Cloud platforms (integrations, not competitors)
	"AWS": {}, "AWS IoT Core": {}, "Azure": {}, "Azure IoT Hub": {}, "GCP": {},
	"RCMS Cloud": {}, "Edge2Cloud": {},

	// Industrial protocols / standards
	"PLC": {}, "DCS": {}, "SCADA": {},
	"Modbus": {}, "BACnet": {}, "KNX": {}, "OPC UA": {},
	"RS-485": {}, "RS-232": {}, "RS-422": {},

	// Components / silicon vendors
	"ARM Cortex-A7": {}, "ARM Cortex-A53": {}, "ARM Cortex-A55": {},
	"NXP i.MX 6ULL": {}, "NXP i.MX 8M": {},
	"Semtech": {}, "Semtech SX1302": {}, "SX1302系列": {},

	// Internal product family terms
	"工业路由器": {}, "工业边缘计算网关": {}, "EG系列边缘计算网关": {},
	"网络分段": {}, "Smart Roaming": {}, "Smart Roaming V3": {},

	// Misc that surfaced
	"Edge": {}, "AI": {}, "ML": {}, "Starlink": {},
}

// nonBrandPatterns catches structural false positives that don't enumerate
// well (e.g. every cellular generation, every region code, every IEC
// standard). Anchored at start of title so "Teltonika" isn't accidentally
// caught by a "Cellular" pattern.
var nonBrandPatterns = []*regexp.Regexp{
	// Cellular generations: 2G, 3G, 4G, 5G optionally followed by /LTE etc.
	regexp.MustCompile(`^[2-5]G([/\s].*)?$`),
	// Region / country codes that production wiki actually surfaced
	regexp.MustCompile(`^(EMEA|LATAM|ANZ|SEA|ASIA|NA|EU|US|JP|CN|UK)$`),
	// Chinese country names
	regexp.MustCompile(`^(日本|马来西亚|中国|美国|欧洲|印度|韩国|越南|泰国|印尼|新加坡)$`),
	// Ingress protection ratings
	regexp.MustCompile(`^IP[0-9]{2}$`),
	// Certifications / standards bodies
	regexp.MustCompile(`^(IEC|EN|GSMA|FCC|CE|UKCA|RoHS|ATEX|RCM)[\s-]`),
	// Debian / Ubuntu version strings
	regexp.MustCompile(`^(Debian|Ubuntu|CentOS|RHEL)\s+\d`),
}

// uniqueStrings dedupes a slice preserving order.
func uniqueStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
