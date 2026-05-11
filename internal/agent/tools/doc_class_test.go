package tools

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
)

func testDocClasses() *config.DocClassConfig {
	cfg := &config.DocClassConfig{
		Classes: []config.DocClass{
			{Name: "product", TitlePatterns: []string{`(?i)^RT\d{3,}_DS_`, `(?i)Datasheet`}},
			{Name: "strategy", TitlePatterns: []string{`(?i)路线图`, `(?i)战略`}},
			{Name: "competitive", TitlePatterns: []string{`(?i)Litmus|基准.{0,3}分析`}},
		},
	}
	cfg.Build()
	return cfg
}

func TestBuildDocClassAttr_ProductTitle(t *testing.T) {
	cfg := testDocClasses()
	got := BuildDocClassAttr("RT137_DS_R1520LG_V1.0.5.pdf", cfg)
	if !strings.Contains(got, `doc_class="product"`) {
		t.Errorf("expected product attr, got %q", got)
	}
	// Leading space is part of the contract — caller can splice without
	// thinking about whitespace.
	if got != "" && got[0] != ' ' {
		t.Errorf("attr must start with a space, got %q", got)
	}
}

func TestBuildDocClassAttr_StrategyTitle(t *testing.T) {
	cfg := testDocClasses()
	got := BuildDocClassAttr("打造核心竞争力：Robustel软硬件一体化战略与E2C Factory演进路线图.md", cfg)
	// Either "strategy" or "competitive" could fire — strategy is listed
	// first in fixture, so it wins. Just verify it's NOT product or empty.
	if got == "" {
		t.Errorf("strategy title must classify, got empty")
	}
	if strings.Contains(got, `doc_class="product"`) {
		t.Errorf("strategy title must not classify as product, got %q", got)
	}
}

func TestBuildDocClassAttr_CompetitiveTitle(t *testing.T) {
	cfg := testDocClasses()
	got := BuildDocClassAttr("Robustel边缘计算生态系统的演进路径——基于Litmus Edge的基准分析与借鉴策略.md", cfg)
	// First match in fixture order: strategy fires first ("路线图" matches
	// "演进路径"? actually no — "演进路径" not "路线图"). Let's check:
	// the title has "演进路径" not "演进路线图" — no strategy hit. But it
	// has "基准分析" → competitive.
	if !strings.Contains(got, `doc_class="competitive"`) {
		t.Errorf("expected competitive, got %q", got)
	}
}

func TestBuildDocClassAttr_NilClassifierIsNoOp(t *testing.T) {
	if got := BuildDocClassAttr("anything.pdf", nil); got != "" {
		t.Errorf("nil classifier must produce empty, got %q", got)
	}
}

func TestBuildDocClassAttr_UnclassifiedTitleIsEmpty(t *testing.T) {
	cfg := testDocClasses()
	if got := BuildDocClassAttr("random-untagged-note.md", cfg); got != "" {
		t.Errorf("unclassified title must produce empty, got %q", got)
	}
}
