package im

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// ── Bug #1 regression: first segment must have ≥1 row even with a long
// preamble ─────────────────────────────────────────────────────────────

func TestSplitLongReply_FirstSegmentHasAtLeastOneRowEvenWithLongPreamble(t *testing.T) {
	// Reproduces the f1dfa510 session bug: 4392-rune answer with a sizeable
	// markdown table prefaced by an intro paragraph. The old condition
	// `first.Len() > headerBudget` (bytes-vs-runes mix) caused the first
	// segment to bail out before adding any row when even the first row
	// would push the candidate over budget — so segment 1 was preamble+
	// header only ("特别短") while segments 2..N had multiple rows each.
	preamble := strings.Repeat("这是一段说明文本 with context to set up. ", 30) // ~1200 runes
	header := "| 公司 | Pitch |"
	separator := "|----|-------|"
	rows := []string{
		"| A | " + strings.Repeat("p", 1500) + " |", // huge row that alone overflows
		"| B | " + strings.Repeat("p", 1500) + " |",
		"| C | " + strings.Repeat("p", 1500) + " |",
	}
	content := preamble + "\n\n" + header + "\n" + separator + "\n" + strings.Join(rows, "\n")
	segs := SplitLongReply(content, 2000)

	if len(segs) < 2 {
		t.Fatalf("expected multi-segment split, got %d", len(segs))
	}
	// First segment must contain row A's distinguishing content, not just
	// preamble + header.
	if !strings.Contains(segs[0], "| A |") {
		t.Errorf("first segment missing row A — packing bug regressed:\n%s", segs[0])
	}
	// Sanity: all rows are present somewhere.
	joined := strings.Join(segs, "\n")
	for _, label := range []string{"| A |", "| B |", "| C |"} {
		if !strings.Contains(joined, label) {
			t.Errorf("row %q lost across segments", label)
		}
	}
}

// ── Bug #2 regression: inter-segment delay configurable ─────────────────

func TestSegmentDelayInterval_DefaultIsOneSecond(t *testing.T) {
	t.Setenv("WEKNORA_IM_SEGMENT_DELAY_MS", "")
	if got := segmentDelayInterval(); got != time.Second {
		t.Errorf("default delay: want 1s, got %v", got)
	}
}

func TestSegmentDelayInterval_EnvOverride(t *testing.T) {
	t.Setenv("WEKNORA_IM_SEGMENT_DELAY_MS", "500")
	if got := segmentDelayInterval(); got != 500*time.Millisecond {
		t.Errorf("env override: want 500ms, got %v", got)
	}
	t.Setenv("WEKNORA_IM_SEGMENT_DELAY_MS", "0")
	if got := segmentDelayInterval(); got != 0 {
		t.Errorf("zero env should disable delay, got %v", got)
	}
	t.Setenv("WEKNORA_IM_SEGMENT_DELAY_MS", "garbage")
	if got := segmentDelayInterval(); got != time.Second {
		t.Errorf("garbage env should fall back to default, got %v", got)
	}
	t.Setenv("WEKNORA_IM_SEGMENT_DELAY_MS", "-100")
	if got := segmentDelayInterval(); got != time.Second {
		t.Errorf("negative env should fall back to default, got %v", got)
	}
}

// ── Hybrid compact-mode delivery (single bubble when short, multi-bubble
// when long) ───────────────────────────────────────────────────────────

func TestOverflowBudgetFor_DefaultIsTwoThousandForWeComAndWeChat(t *testing.T) {
	// Make sure the env var doesn't leak from a prior test.
	t.Setenv("WEKNORA_IM_FRAME_BUDGET", "")
	if got := overflowBudgetFor("wecom"); got != 2000 {
		t.Errorf("wecom default budget: want 2000, got %d", got)
	}
	if got := overflowBudgetFor("wechat"); got != 2000 {
		t.Errorf("wechat default budget: want 2000, got %d", got)
	}
}

func TestOverflowBudgetFor_EnvOverride(t *testing.T) {
	// Empirical calibration: operator should be able to dial the budget
	// up or down without a rebuild based on what WeCom actually renders.
	t.Setenv("WEKNORA_IM_FRAME_BUDGET", "3500")
	if got := overflowBudgetFor("wecom"); got != 3500 {
		t.Errorf("env override ignored: want 3500, got %d", got)
	}
	// Garbage value → fall back to default (not zero), so a typo doesn't
	// silently kill the splitter entirely.
	t.Setenv("WEKNORA_IM_FRAME_BUDGET", "not-a-number")
	if got := overflowBudgetFor("wecom"); got != 2000 {
		t.Errorf("non-numeric env should fall back to default, got %d", got)
	}
	// Zero / negative also fall back — those would degenerate the splitter.
	t.Setenv("WEKNORA_IM_FRAME_BUDGET", "0")
	if got := overflowBudgetFor("wecom"); got != 2000 {
		t.Errorf("zero env should fall back to default, got %d", got)
	}
}

func TestOverflowBudgetFor_NonIMPlatformsReturnZero(t *testing.T) {
	// Other platforms don't have a known hard cap — don't split.
	// Env var must not leak into them either (only applies to wecom/wechat).
	t.Setenv("WEKNORA_IM_FRAME_BUDGET", "1234")
	for _, p := range []string{"feishu", "slack", "telegram", "mattermost", "", "unknown"} {
		if got := overflowBudgetFor(p); got != 0 {
			t.Errorf("platform=%q: expected zero budget (no split), got %d", p, got)
		}
	}
}

// Sanity check that the existing SplitLongReply works at the new default
// budget. Calibrates expectations: a 6000-rune answer should split into
// 3 segments at budget 2000, not 10.
func TestSplitLongReply_RealisticReplyAtDefaultBudget(t *testing.T) {
	// Mimic a typical pitch-table response that hit the WeCom budget
	// historically (median historical reply: 2762 chars).
	answer := strings.Repeat("产品规格说明 product specs and bullet point details. ", 100) // ~6500 runes
	segs := SplitLongReply(answer, 2000)
	// Should split into 3-5 segments at 2000-budget (not the 10+ we'd
	// get at the old 600 budget).
	if len(segs) < 2 || len(segs) > 6 {
		t.Errorf("expected 2-6 segments for ~6500-rune answer at budget=2000, got %d", len(segs))
	}
	for i, s := range segs {
		if utf8.RuneCountInString(s) > 2000 {
			t.Errorf("segment %d exceeds budget: %d runes", i, utf8.RuneCountInString(s))
		}
	}
}

func TestSplitLongReply_ShortAnswerStaysSingleAtDefaultBudget(t *testing.T) {
	// 32% of historical wecom replies are < 700 chars — these MUST stay
	// single-segment at the new default budget so the bubble UX is clean.
	short := "Robustel EG5120 是一款工业边缘计算网关，集成 NPU + 蜂窝 + Wi-Fi 6。" + strings.Repeat("规格 ", 100) // ~500 runes
	segs := SplitLongReply(short, 2000)
	if len(segs) != 1 {
		t.Errorf("short answer (<budget) must stay single bubble, got %d segments", len(segs))
	}
}

// ── Legacy truncateForCompactDisplay tests (deprecated path) ─────────────
//
// The function still exists as a stub but isn't on the runtime path anymore.
// These tests pin the deprecated contract for now; remove together with the
// function in a follow-up.

// Compact-mode display path: WeCom / WeChat bubble has a hard ~746-char
// rendering cap. Instead of chunking long answers into N bubbles (the old
// SplitLongReply path, which gave a noisy UX), we now hide the thinking
// process entirely and replace the bubble with the final answer in a
// single atomic frame. If the final answer still exceeds the budget,
// truncateForCompactDisplay caps it and appends a footer pointing the
// user to the web UI.

func TestTruncateForCompactDisplay_ShortAnswerUnchanged(t *testing.T) {
	in := "Robustel EG5120 是一款工业边缘计算网关。"
	out := truncateForCompactDisplay(in, 600)
	if out != in {
		t.Errorf("short answer must pass through unchanged\nwant: %q\ngot:  %q", in, out)
	}
}

func TestTruncateForCompactDisplay_ExactlyBudgetUnchanged(t *testing.T) {
	in := strings.Repeat("x", 600)
	out := truncateForCompactDisplay(in, 600)
	if out != in {
		t.Errorf("at-budget answer must pass through unchanged")
	}
}

func TestTruncateForCompactDisplay_LongAnswerTruncatedWithFooter(t *testing.T) {
	// 2000-rune answer (mixed Chinese + English so we exercise the rune
	// counting path). Budget 600 means head ≤ 520 runes (600 - 80 footer
	// reserve), and the footer must mention the full length.
	body := strings.Repeat("规格说明text content. ", 100) // ~2200 runes
	out := truncateForCompactDisplay(body, 600)

	if utf8.RuneCountInString(out) > 600 {
		t.Errorf("output exceeds budget: %d runes", utf8.RuneCountInString(out))
	}
	if !strings.Contains(out, "完整回答共") {
		t.Errorf("expected truncation footer, got tail: %q", out[len(out)-200:])
	}
	if !strings.Contains(out, "Web 端") {
		t.Errorf("footer must point user to Web UI, got: %s", out[len(out)-200:])
	}
	// Footer reports the FULL rune count, not the truncated head's count.
	fullLen := utf8.RuneCountInString(body)
	if !strings.Contains(out, "共 "+itoa(fullLen)+" 字") {
		t.Errorf("footer must include full length %d, got tail: %q", fullLen, out[len(out)-200:])
	}
}

func TestTruncateForCompactDisplay_CJKBudgetedByRuneNotByte(t *testing.T) {
	// 1000 Chinese chars ≈ 3000 UTF-8 bytes. Budget is in RUNES, so 600
	// budget means ≤ 600 visible characters regardless of byte length.
	in := strings.Repeat("中文规格内容。", 200) // 1400 runes
	out := truncateForCompactDisplay(in, 600)
	got := utf8.RuneCountInString(out)
	if got > 600 {
		t.Errorf("rune budget violated: got %d runes, budget 600", got)
	}
}

func TestTruncateForCompactDisplay_PathologicallySmallBudget(t *testing.T) {
	// Budget smaller than footer reserve (80). Should hard-truncate without
	// footer rather than producing garbage. Defensive guard.
	in := strings.Repeat("x", 200)
	out := truncateForCompactDisplay(in, 30)
	if utf8.RuneCountInString(out) > 30 {
		t.Errorf("hard truncation must respect budget even when < footer reserve, got %d", utf8.RuneCountInString(out))
	}
	if strings.Contains(out, "Web 端") {
		t.Errorf("footer must NOT appear when budget too small to fit it")
	}
}

func TestIsCompactPlatform_WeComAndWeChatOnly(t *testing.T) {
	cases := map[string]bool{
		"wecom":      true,
		"wechat":     true,
		"feishu":     false,
		"slack":      false,
		"telegram":   false,
		"mattermost": false,
		"":           false,
		"unknown":    false,
	}
	for p, want := range cases {
		if got := isCompactPlatform(p); got != want {
			t.Errorf("isCompactPlatform(%q) = %v, want %v", p, got, want)
		}
	}
}
