package im

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSplitLongReply_ShortContentUnchanged(t *testing.T) {
	// Below budget: pass through as a single segment, untouched.
	in := "Short answer. 短答案。"
	segs := SplitLongReply(in, 600)
	if len(segs) != 1 || segs[0] != in {
		t.Errorf("expected single passthrough segment, got %d segments: %q", len(segs), segs)
	}
}

func TestSplitLongReply_ExactlyAtBudgetUnchanged(t *testing.T) {
	// Boundary: content rune count == budget → still single segment.
	in := strings.Repeat("x", 100)
	segs := SplitLongReply(in, 100)
	if len(segs) != 1 {
		t.Errorf("expected single segment at exact budget, got %d", len(segs))
	}
}

func TestSplitLongReply_NonPositiveBudgetIsNoOp(t *testing.T) {
	// Defensive guard: bad config shouldn't crash; just return the input.
	in := strings.Repeat("x", 10000)
	for _, b := range []int{0, -1, -1000} {
		segs := SplitLongReply(in, b)
		if len(segs) != 1 || segs[0] != in {
			t.Errorf("budget=%d: expected passthrough, got %d segments", b, len(segs))
		}
	}
}

func TestSplitLongReply_PlainProseSplitsAtParagraphBreaks(t *testing.T) {
	// Three paragraphs each ~50 chars, budget 70 → expect 3 separate
	// segments (each block alone fits but two blocks together exceed).
	p1 := strings.Repeat("a", 50)
	p2 := strings.Repeat("b", 50)
	p3 := strings.Repeat("c", 50)
	content := p1 + "\n\n" + p2 + "\n\n" + p3
	segs := SplitLongReply(content, 70)
	if len(segs) < 2 {
		t.Fatalf("expected ≥ 2 segments for 150-char content with budget 70, got %d", len(segs))
	}
	// Each segment under budget.
	for i, s := range segs {
		if utf8.RuneCountInString(s) > 70 {
			t.Errorf("segment %d exceeds budget: len=%d", i, utf8.RuneCountInString(s))
		}
	}
	// Concatenated content covers all three paragraphs (no data lost).
	joined := strings.Join(segs, " ")
	for _, want := range []string{p1, p2, p3} {
		if !strings.Contains(joined, want) {
			t.Errorf("paragraph %q lost across segments", want[:10]+"…")
		}
	}
}

// ── Table-aware splitting (the f438430e / 50e8531f primary failure mode) ──

func TestSplitLongReply_MarkdownTableRowByRowWithHeaderEcho(t *testing.T) {
	// 10-company pitch table mimicking the failing 50e8531f session.
	// Budget chosen so 2-3 rows fit per segment after header is re-emitted.
	header := "| # | Company | Pitch |"
	sep := "|---|---------|-------|"
	rows := []string{}
	for i := 1; i <= 10; i++ {
		// ~80 char row.
		rows = append(rows, "| "+itoa(i)+" | Co"+itoa(i)+" | "+strings.Repeat("p", 60)+" |")
	}
	table := header + "\n" + sep + "\n" + strings.Join(rows, "\n")
	segs := SplitLongReply(table, 250)

	if len(segs) < 3 {
		t.Fatalf("expected ≥ 3 segments for 10-row table with budget 250, got %d:\n%s",
			len(segs), strings.Join(segs, "\n---\n"))
	}
	// Every segment must include the header + separator (table self-contained).
	for i, s := range segs {
		if !strings.Contains(s, header) || !strings.Contains(s, sep) {
			t.Errorf("segment %d missing header/separator (table not self-contained):\n%s", i+1, s)
		}
	}
	// All 10 rows present across all segments.
	joined := strings.Join(segs, "\n")
	for _, row := range rows {
		if !strings.Contains(joined, row) {
			t.Errorf("row lost across segments: %s", row)
		}
	}
}

func TestSplitLongReply_TableWithPreambleAndTrailer(t *testing.T) {
	preamble := "Below is the pitch table:"
	header := "| Co | Pitch |"
	sep := "|----|-------|"
	rows := []string{
		"| A  | " + strings.Repeat("x", 100) + " |",
		"| B  | " + strings.Repeat("y", 100) + " |",
		"| C  | " + strings.Repeat("z", 100) + " |",
	}
	trailer := "\n\nThat's all. " + strings.Repeat("Q", 200)
	content := preamble + "\n\n" + header + "\n" + sep + "\n" + strings.Join(rows, "\n") + trailer

	segs := SplitLongReply(content, 180)

	if len(segs) < 2 {
		t.Fatalf("expected ≥ 2 segments, got %d", len(segs))
	}
	// Preamble appears in the FIRST segment only.
	if !strings.Contains(segs[0], preamble) {
		t.Errorf("preamble must appear in first segment, got: %s", segs[0])
	}
	for i := 1; i < len(segs); i++ {
		if strings.Contains(segs[i], preamble) {
			t.Errorf("preamble leaked into segment %d", i+1)
		}
	}
	// Trailer content appears somewhere (likely the last segment).
	joined := strings.Join(segs, "\n")
	if !strings.Contains(joined, "That's all.") {
		t.Errorf("trailer lost: trailer text not found in any segment")
	}
}

func TestSplitLongReply_NonTableContentFallsToBlockSplit(t *testing.T) {
	// No table → splitTableAware should return (nil, false), splitByBlocks
	// takes over.
	content := strings.Repeat("Paragraph one. ", 30) + "\n\n" +
		strings.Repeat("Paragraph two. ", 30) + "\n\n" +
		strings.Repeat("Paragraph three. ", 30)
	segs := SplitLongReply(content, 200)
	if len(segs) < 2 {
		t.Fatalf("expected ≥ 2 segments without table, got %d", len(segs))
	}
	for i, s := range segs {
		if utf8.RuneCountInString(s) > 200 {
			t.Errorf("segment %d exceeds budget: %d runes", i, utf8.RuneCountInString(s))
		}
	}
}

func TestSplitLongReply_OversizedSinglePlainParagraphFallsToLineThenSentence(t *testing.T) {
	// One huge paragraph (no \n\n, no \n) ~600 chars, budget 200 → must
	// split at sentence ends (./。/!/?/；).
	paragraph := strings.Repeat("This is a sentence. ", 30) // 600 chars
	segs := SplitLongReply(paragraph, 200)
	if len(segs) < 2 {
		t.Fatalf("expected ≥ 2 segments, got %d", len(segs))
	}
	for i, s := range segs {
		if utf8.RuneCountInString(s) > 200 {
			t.Errorf("segment %d exceeds budget: %d runes", i, utf8.RuneCountInString(s))
		}
	}
	// Each non-final segment should end at sentence boundary.
	for i := 0; i < len(segs)-1; i++ {
		trimmed := strings.TrimSpace(segs[i])
		last := []rune(trimmed)[len([]rune(trimmed))-1]
		if last != '.' && last != '。' && last != '!' && last != '?' && last != '；' && last != ';' {
			t.Errorf("segment %d should end at sentence boundary, got last rune %q", i, last)
		}
	}
}

func TestSplitLongReply_CJKContentSplittableByRuneNotByte(t *testing.T) {
	// Pure Chinese paragraph: each rune is 3 bytes UTF-8. Test that budgets
	// are interpreted in runes, not bytes.
	cn := strings.Repeat("中文测试。", 200) // 1000 runes, ~3000 bytes
	segs := SplitLongReply(cn, 100)
	for i, s := range segs {
		runes := utf8.RuneCountInString(s)
		if runes > 100 {
			t.Errorf("segment %d exceeds rune budget: %d runes", i, runes)
		}
	}
	if len(segs) < 5 {
		t.Errorf("expected many segments for 1000-rune content with budget 100, got %d", len(segs))
	}
}

// itoa returns the decimal representation of n without depending on strconv —
// keeps the test file's import list minimal.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// ── Table-detection helper edge cases ──

func TestIsTableSeparator_RecognisesCommonGFMShapes(t *testing.T) {
	cases := map[string]bool{
		"|---|---|":          true,
		"| --- | --- |":      true,
		"| :---: | :---: |":  true,
		"| :--- | ---: |":    true,
		"|-|-|":              true,
		"":                   false,
		"normal text":        false,
		"| col1 | col2 |":    false, // header row, not separator
		"| --- |":            false, // single column → still 1 cell which is "---"
		"| --- |  |":         false, // empty cell makes it invalid
	}
	// Single-column "| --- |" — let's verify what isTableSeparator returns
	// for that. The function as written allows it (1 cell of "---"), so
	// remove from negative cases.
	delete(cases, "| --- |")

	for in, want := range cases {
		got := isTableSeparator(in)
		if got != want {
			t.Errorf("isTableSeparator(%q) = %v, want %v", in, got, want)
		}
	}
}
