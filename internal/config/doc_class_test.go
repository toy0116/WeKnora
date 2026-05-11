package config

import "testing"

func fixtureDocClassConfig() *DocClassConfig {
	// Mirrors a minimal subset of the production doc_classes.yaml so the
	// classifier algorithm is tested independently from the YAML file.
	cfg := &DocClassConfig{
		Classes: []DocClass{
			{
				Name: "product",
				TitlePatterns: []string{
					`(?i)^RT\d{3,}_DS_`,
					`(?i)^RT_(SM|UG)_`,
					`(?i)RobustOS.*(Manual|Guide)`,
					`(?i)\bDatasheet\b`,
				},
			},
			{
				Name: "competitive",
				TitlePatterns: []string{
					`(?i)竞品.{0,3}分析`,
					`(?i)基准.{0,3}分析`,
					`(?i)Litmus|Kepware|Teltonika`,
					`(?i)In-Depth Analysis of`,
				},
			},
			// research BEFORE strategy: "深度战略研究报告" is research first.
			{
				Name: "research",
				TitlePatterns: []string{
					`(?i)研究报告`,
					`(?i)深度.{0,2}(战略)?.{0,2}研究`,
				},
			},
			{
				Name: "strategy",
				TitlePatterns: []string{
					`(?i)战略|strategy`,
					`(?i)路线图`,
					`(?i)演进路.{0,3}线`,
					`(?i)\bGTM\b`,
				},
			},
			{
				Name: "training",
				TitlePatterns: []string{
					`(?i)培训.{0,2}手册`,
					`(?i)\bMDR\b`,
				},
			},
		},
	}
	cfg.Build()
	return cfg
}

func TestClassify_ProductFromDatasheetCode(t *testing.T) {
	cfg := fixtureDocClassConfig()
	got := cfg.Classify("RT137_DS_R1520LG_V1.0.5.pdf", "")
	if got != "product" {
		t.Errorf("expected product, got %q", got)
	}
}

func TestClassify_ProductFromUserGuide(t *testing.T) {
	cfg := fixtureDocClassConfig()
	got := cfg.Classify("RT_SM_RobustOS Pro Software Manual_V2.4.0.pdf", "")
	if got != "product" {
		t.Errorf("expected product, got %q", got)
	}
}

func TestClassify_StrategyFromRoadmap(t *testing.T) {
	cfg := fixtureDocClassConfig()
	for _, title := range []string{
		"打造核心竞争力：Robustel软硬件一体化战略与E2C Factory演进路线图.md",
		"破局之路：连接与计算时代的销售转型战略路线图.md",
		"“泰坦计划”：Robustel EG5120 工业边缘网关 GTM 市场进入策略与材料蓝图.md",
	} {
		if got := cfg.Classify(title, ""); got != "strategy" {
			t.Errorf("title=%q: expected strategy, got %q", title, got)
		}
	}
}

func TestClassify_CompetitiveFromBenchmark(t *testing.T) {
	cfg := fixtureDocClassConfig()
	for _, title := range []string{
		"Robustel边缘计算生态系统的演进路径——基于Litmus Edge的基准分析与借鉴策略.md",
		"An In-Depth Analysis of Teltonika RutOS_ Architecture, Evolution, and Market Positioning.md",
		"针对Robustel R1520LG LoRaWAN 网关的软件竞争力基准分析与战略路线图.md",
	} {
		if got := cfg.Classify(title, ""); got != "competitive" {
			t.Errorf("title=%q: expected competitive, got %q", title, got)
		}
	}
}

func TestClassify_ResearchFromTrendReport(t *testing.T) {
	cfg := fixtureDocClassConfig()
	got := cfg.Classify("2025年全球边缘计算与物联网融合深度战略研究报告.md", "")
	if got != "research" {
		t.Errorf("expected research, got %q", got)
	}
}

func TestClassify_TrainingFromMDR(t *testing.T) {
	cfg := fixtureDocClassConfig()
	got := cfg.Classify("《Robustel国际业务拓展专员 (MDR) 新人训练营》培训手册.md", "")
	if got != "training" {
		t.Errorf("expected training, got %q", got)
	}
}

func TestClassify_FirstMatchWinsOrder(t *testing.T) {
	// Title contains both a competitive marker ("基准分析") and a strategy
	// marker ("战略路线图"). Order in the fixture places competitive after
	// product but before strategy. Test that the first competitive match
	// wins over the later strategy match — a benchmark report is closer
	// to competitive than strategy even when it also discusses strategy.
	cfg := fixtureDocClassConfig()
	got := cfg.Classify("针对Robustel R1520LG LoRaWAN 网关的软件竞争力基准分析与战略路线图.md", "")
	if got != "competitive" {
		t.Errorf("first-match-wins should pick competitive, got %q", got)
	}
}

func TestClassify_UnclassifiedReturnsEmpty(t *testing.T) {
	cfg := fixtureDocClassConfig()
	for _, title := range []string{
		"",
		"some-random-markdown-note.md",
		"鲁邦通知识库.md",
	} {
		if got := cfg.Classify(title, ""); got != "" {
			t.Errorf("title=%q: expected empty, got %q", title, got)
		}
	}
}

func TestClassify_NilSafe(t *testing.T) {
	var cfg *DocClassConfig
	if got := cfg.Classify("anything", ""); got != "" {
		t.Errorf("nil config must return empty, got %q", got)
	}
}

func TestClassify_KBDefaultFallbackWhenTitleUnmatched(t *testing.T) {
	cfg := fixtureDocClassConfig()
	cfg.KBDefaults = map[string]string{
		"Robustel_Datasheet": "product",
		"Robustel_Strategy":  "strategy",
		"Marketing_Insight":  "research",
		"TY_Notes":           "internal",
	}
	cfg.ResolveKBDefaults([]KBSummary{
		{ID: "kb-1", Name: "Robustel_Datasheet"},
		{ID: "kb-2", Name: "Robustel_Strategy"},
		{ID: "kb-3", Name: "Marketing_Insight"},
		{ID: "kb-4", Name: "TY_Notes"},
		{ID: "kb-5", Name: "Robustel_Inbox"}, // not in defaults
	})

	// Untagged title in Robustel_Datasheet KB falls back to product.
	if got := cfg.Classify("some-untagged-note.md", "kb-1"); got != "product" {
		t.Errorf("KB fallback to product failed, got %q", got)
	}
	// Untagged title in Marketing_Insight → research.
	if got := cfg.Classify("行业某分析报告.md", "kb-3"); got != "research" {
		// Note: "分析报告" matches research pattern, so this might fire title.
		// Use a definitively-untagged title:
		if got := cfg.Classify("一些杂记.md", "kb-3"); got != "research" {
			t.Errorf("KB fallback to research failed, got %q", got)
		}
	}
	// Untagged title in unknown KB → unclassified.
	if got := cfg.Classify("anything.md", "kb-5"); got != "" {
		t.Errorf("unknown KB must not classify, got %q", got)
	}
	// kbID empty → no KB fallback.
	if got := cfg.Classify("anything.md", ""); got != "" {
		t.Errorf("empty kbID must not classify, got %q", got)
	}
}

func TestClassify_TitlePatternBeatsKBDefault(t *testing.T) {
	// A strategy roadmap title sitting in Robustel_Datasheet (kb_default=
	// product) must still classify as strategy — the title is more specific
	// than the KB default.
	cfg := fixtureDocClassConfig()
	cfg.KBDefaults = map[string]string{"Robustel_Datasheet": "product"}
	cfg.ResolveKBDefaults([]KBSummary{{ID: "kb-1", Name: "Robustel_Datasheet"}})

	got := cfg.Classify("Robustel软硬件一体化战略与E2C Factory演进路线图.md", "kb-1")
	if got != "strategy" {
		t.Errorf("title pattern must beat KB default, got %q", got)
	}
}

func TestResolveKBDefaults_RebuildOnRename(t *testing.T) {
	// Renaming a KB requires re-calling ResolveKBDefaults. Verify that
	// re-resolution correctly drops the old mapping.
	cfg := &DocClassConfig{
		Classes:    []DocClass{},
		KBDefaults: map[string]string{"Robustel_Strategy": "strategy"},
	}
	cfg.Build()

	cfg.ResolveKBDefaults([]KBSummary{{ID: "kb-1", Name: "Robustel_Strategy"}})
	if got := cfg.Classify("note.md", "kb-1"); got != "strategy" {
		t.Errorf("pre-rename: expected strategy, got %q", got)
	}
	// Rename Robustel_Strategy → Strategy_Archive. Rebuild.
	cfg.ResolveKBDefaults([]KBSummary{{ID: "kb-1", Name: "Strategy_Archive"}})
	if got := cfg.Classify("note.md", "kb-1"); got != "" {
		t.Errorf("post-rename: expected empty (new name not in yaml), got %q", got)
	}
}

func TestClassify_InvalidPatternSkipped(t *testing.T) {
	// A malformed pattern in one class must not break classification for
	// other classes. The bad pattern is silently skipped by Build.
	cfg := &DocClassConfig{
		Classes: []DocClass{
			{Name: "bad", TitlePatterns: []string{`[unclosed`}}, // invalid RE2
			{Name: "good", TitlePatterns: []string{`(?i)valid`}},
		},
	}
	cfg.Build()
	if got := cfg.Classify("Valid title", ""); got != "good" {
		t.Errorf("expected good (bad pattern skipped), got %q", got)
	}
}
