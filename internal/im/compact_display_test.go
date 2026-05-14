package im

import (
	"strings"
	"testing"
	"unicode/utf8"
)

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
