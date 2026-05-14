// Package im — long-reply chunker for IM platforms with short per-message caps.
//
// Why this exists: WeCom (企业微信) group bot stream messages display at most
// ~700 visible characters in the chat UI — confirmed empirically on session
// 50e8531f where an 11,784-character reply was cut after 746 characters, mid
// markdown table (inside the first company's pitch). Without splitting the
// answer, every long IM reply (research summaries, multi-company pitch tables,
// regulatory analyses, …) gets silently truncated and the user sees only the
// first ~7% of the LLM's actual output.
//
// SplitLongReply takes a markdown answer and the per-segment character budget,
// and returns N ≥ 1 segments that each fit under the budget. Boundaries are
// chosen to minimise mid-thought / mid-table cuts:
//   - Markdown tables: split row-by-row; the header + separator are
//     re-emitted at the top of each segment for context.
//   - Code fences: kept intact (a code block that exceeds the budget will
//     overflow rather than be split — broken code is worse than spillover).
//   - Plain text: prefers paragraph breaks (\n\n), then line breaks (\n),
//     then sentence ends, then a hard cut as last resort.
//
// Each segment is meant to be prefixed by the caller with a `[N/M]\n` header
// when sending — the budget here covers the content only; reserve headroom
// at the call site.
package im

import (
	"strings"
	"unicode/utf8"
)

// SplitLongReply breaks a markdown answer into runeBudget-bounded segments.
//
// Returns a single-element slice containing content unchanged when the answer
// already fits. Otherwise returns ≥ 2 segments, each whose rune count does
// not exceed runeBudget, picking the highest-priority natural boundary
// available at or before the budget.
//
// runeBudget is measured in unicode runes (visible characters), not bytes —
// WeCom's display limit appears character-based, and budgeting in runes lets
// the caller think in "characters the user will see".
//
// Empty content → []string{""}; non-positive budget → []string{content}
// (i.e. no-op so callers don't crash on a bad config).
func SplitLongReply(content string, runeBudget int) []string {
	if runeBudget <= 0 {
		return []string{content}
	}
	if utf8.RuneCountInString(content) <= runeBudget {
		return []string{content}
	}

	// Detect a single dominant markdown table — the common case for the
	// pitch-table failure mode. When found, use table-aware splitting that
	// re-emits the header at every segment boundary. For mixed content
	// (prose + table), the table-aware path falls back to generic block
	// splitting for non-table sections.
	if segs, ok := splitTableAware(content, runeBudget); ok {
		return segs
	}

	return splitByBlocks(content, runeBudget)
}

// splitTableAware handles the "one big markdown table possibly surrounded by
// prose" case. Detects the table by scanning for the GitHub-flavored markdown
// pattern: a header row (`| ... |`), a separator row (`| --- | ... |`), and
// one-or-more data rows. Splits row-by-row, re-emitting header+separator at
// the start of each post-first segment.
//
// Returns (segments, true) when a table was detected and successfully split;
// (nil, false) when there's no table to honor and the caller should fall
// back to generic block splitting.
func splitTableAware(content string, runeBudget int) ([]string, bool) {
	lines := strings.Split(content, "\n")
	headerIdx := -1
	separatorIdx := -1
	dataStartIdx := -1
	dataEndIdx := -1

	for i := 0; i < len(lines)-1; i++ {
		if isTableRow(lines[i]) && isTableSeparator(lines[i+1]) {
			headerIdx = i
			separatorIdx = i + 1
			dataStartIdx = i + 2
			break
		}
	}
	if headerIdx < 0 {
		return nil, false
	}
	// Find the last data row of the table.
	dataEndIdx = dataStartIdx
	for j := dataStartIdx; j < len(lines); j++ {
		if !isTableRow(lines[j]) {
			break
		}
		dataEndIdx = j + 1
	}
	if dataEndIdx <= dataStartIdx {
		// Empty table — fall through.
		return nil, false
	}

	preamble := strings.Join(lines[:headerIdx], "\n")
	header := lines[headerIdx]
	separator := lines[separatorIdx]
	dataRows := lines[dataStartIdx:dataEndIdx]
	trailer := strings.Join(lines[dataEndIdx:], "\n")

	// Multi-section answers (e.g. spec sheets with one tiny table per
	// section + multiple <kb ... /> citations + bullet summaries) DON'T
	// belong on the table-aware path. Detection: if the first detected
	// table's body is small relative to the whole answer, the table isn't
	// the dominant content — splitByBlocks gives more balanced segments.
	//
	// Reproducer that motivated this guard: f1dfa510 R1520LG spec query.
	// Full answer 5382 runes, but first table ("硬件系统") had 3 data rows
	// (~150 runes) — the rest was 9 other sections + citations + bullets,
	// which got piled into "trailer". Table-aware path then produced an
	// unbalanced split with segment 1 = preamble + tiny table + 0 rows
	// (special-case packing) while trailer was sliced by splitByBlocks
	// into the remaining N-1 segments.
	//
	// Threshold: table body < 30% of total runes → not table-dominant.
	tableRunes := utf8.RuneCountInString(strings.Join(dataRows, "\n"))
	totalRunes := utf8.RuneCountInString(content)
	if tableRunes*100/maxInt(totalRunes, 1) < 30 {
		return nil, false
	}

	headerBlock := header + "\n" + separator + "\n"

	var segments []string
	// First segment: preamble + header + as many rows as fit.
	//
	// Guarantee at least one row in the first segment even when preamble is
	// already eating most of the budget. The old check `first.Len() > headerBudget`
	// compared bytes-of-preamble-plus-header against runes-of-header — which
	// is essentially always true once preamble is non-empty, causing the loop
	// to bail out before adding ANY row when even the first row would push the
	// candidate over budget. That bug produced the "first segment is empty
	// rows" / "first bubble very short" symptom reported on the 4392-rune
	// reply that split into 4 segments. Now we use rowIdx>0 to require at
	// least one row before honoring the budget cap.
	first := strings.Builder{}
	if strings.TrimSpace(preamble) != "" {
		first.WriteString(preamble)
		first.WriteString("\n\n")
	}
	first.WriteString(headerBlock)
	rowIdx := 0
	for rowIdx < len(dataRows) {
		candidate := first.String() + dataRows[rowIdx] + "\n"
		if rowIdx > 0 && utf8.RuneCountInString(candidate) > runeBudget {
			break
		}
		first.WriteString(dataRows[rowIdx])
		first.WriteString("\n")
		rowIdx++
		// Hard stop if a single row exceeded budget — emit anyway, the
		// alternative (silent drop) is worse than overflow.
		if utf8.RuneCountInString(first.String()) > runeBudget {
			break
		}
	}
	segments = append(segments, strings.TrimRight(first.String(), "\n"))

	// Subsequent segments: header re-emitted + rows that fit.
	for rowIdx < len(dataRows) {
		seg := strings.Builder{}
		seg.WriteString(headerBlock)
		seg.WriteString(dataRows[rowIdx])
		seg.WriteString("\n")
		rowIdx++
		for rowIdx < len(dataRows) {
			candidate := seg.String() + dataRows[rowIdx] + "\n"
			if utf8.RuneCountInString(candidate) > runeBudget {
				break
			}
			seg.WriteString(dataRows[rowIdx])
			seg.WriteString("\n")
			rowIdx++
		}
		segments = append(segments, strings.TrimRight(seg.String(), "\n"))
	}

	// Trailer (anything after the table): split by paragraphs and append as
	// new segments so it isn't lost.
	if strings.TrimSpace(trailer) != "" {
		trailerSegs := splitByBlocks(trailer, runeBudget)
		segments = append(segments, trailerSegs...)
	}

	return segments, true
}

// splitByBlocks is the prose path: split content at \n\n boundaries, then at
// \n for over-long blocks, then sentence ends, then a hard byte cut as last
// resort. Each segment's rune count is bounded by runeBudget except when a
// single indivisible unit (e.g. a code fence) exceeds the budget — in that
// case the unit is kept intact and the segment overflows.
func splitByBlocks(content string, runeBudget int) []string {
	blocks := strings.Split(content, "\n\n")
	var segments []string
	var current strings.Builder

	flush := func() {
		s := strings.TrimRight(current.String(), "\n")
		if s != "" {
			segments = append(segments, s)
		}
		current.Reset()
	}

	for _, block := range blocks {
		blockRunes := utf8.RuneCountInString(block)
		// If single block exceeds budget, flush current and split the block.
		if blockRunes > runeBudget {
			flush()
			for _, sub := range splitOversizedBlock(block, runeBudget) {
				segments = append(segments, sub)
			}
			continue
		}
		// Would adding this block exceed the budget? If yes, flush and start
		// a new segment with this block.
		candidate := current.String()
		if candidate != "" {
			candidate += "\n\n"
		}
		candidate += block
		if utf8.RuneCountInString(candidate) > runeBudget {
			flush()
		}
		if current.Len() > 0 {
			current.WriteString("\n\n")
		}
		current.WriteString(block)
	}
	flush()
	if len(segments) == 0 {
		return []string{content}
	}
	return segments
}

// splitOversizedBlock breaks a single block (one that exceeds the budget on
// its own — typically a long prose paragraph) into runeBudget-bounded pieces.
// Tries line breaks, then sentence ends, then a hard rune cut.
func splitOversizedBlock(block string, runeBudget int) []string {
	lines := strings.Split(block, "\n")
	if len(lines) > 1 {
		var out []string
		var cur strings.Builder
		flush := func() {
			s := strings.TrimRight(cur.String(), "\n")
			if s != "" {
				out = append(out, s)
			}
			cur.Reset()
		}
		for _, line := range lines {
			if utf8.RuneCountInString(line) > runeBudget {
				flush()
				out = append(out, splitOversizedLine(line, runeBudget)...)
				continue
			}
			candidate := cur.String()
			if candidate != "" {
				candidate += "\n"
			}
			candidate += line
			if utf8.RuneCountInString(candidate) > runeBudget {
				flush()
			}
			if cur.Len() > 0 {
				cur.WriteString("\n")
			}
			cur.WriteString(line)
		}
		flush()
		if len(out) > 0 {
			return out
		}
	}
	return splitOversizedLine(block, runeBudget)
}

// splitOversizedLine handles a single line longer than the budget. Tries
// sentence-end boundaries (./。/!/?/！/？), then a hard rune cut.
func splitOversizedLine(line string, runeBudget int) []string {
	sentenceEnds := []rune{'.', '。', '!', '！', '?', '？', ';', '；'}
	isEnd := func(r rune) bool {
		for _, e := range sentenceEnds {
			if r == e {
				return true
			}
		}
		return false
	}
	runes := []rune(line)
	if len(runes) <= runeBudget {
		return []string{line}
	}
	var out []string
	start := 0
	for start < len(runes) {
		end := start + runeBudget
		if end >= len(runes) {
			out = append(out, string(runes[start:]))
			break
		}
		// Walk back from end to find a sentence boundary in the last 30% of
		// the budget window — short enough to keep segments roughly even.
		cut := end
		for k := end; k > start+runeBudget*7/10; k-- {
			if isEnd(runes[k]) {
				cut = k + 1
				break
			}
		}
		out = append(out, strings.TrimSpace(string(runes[start:cut])))
		start = cut
	}
	return out
}

// maxInt returns the larger of a, b. Local helper to avoid importing
// "math" in this small file. The standard `max` builtin (Go 1.21+) is
// fine but pre-1.21 deployments still build cleanly with this.
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// isTableRow returns true when the trimmed line looks like a GFM table row:
// starts and ends with `|`, has at least one inner `|`.
func isTableRow(line string) bool {
	t := strings.TrimSpace(line)
	if len(t) < 3 || !strings.HasPrefix(t, "|") || !strings.HasSuffix(t, "|") {
		return false
	}
	// At least one pipe inside.
	inner := t[1 : len(t)-1]
	return strings.Contains(inner, "|")
}

// isTableSeparator returns true when the line is a GFM table separator row
// (the `| --- | :---: |` line between header and data).
func isTableSeparator(line string) bool {
	t := strings.TrimSpace(line)
	if !isTableRow(t) {
		return false
	}
	// Strip pipes, check every cell is `---`-like with optional colons.
	cells := strings.Split(strings.Trim(t, "|"), "|")
	if len(cells) == 0 {
		return false
	}
	for _, c := range cells {
		c = strings.TrimSpace(c)
		if c == "" {
			return false
		}
		// Allow :--- / ---: / :---: / --- patterns.
		c = strings.TrimPrefix(c, ":")
		c = strings.TrimSuffix(c, ":")
		if c == "" {
			return false
		}
		for _, r := range c {
			if r != '-' {
				return false
			}
		}
	}
	return true
}
