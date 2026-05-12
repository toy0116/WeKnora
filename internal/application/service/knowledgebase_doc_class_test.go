package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// stubKBRepoForDocClass is a single-method stub of
// interfaces.KnowledgeBaseRepository. Only ListKnowledgeBases is exercised by
// RefreshDocClassKBDefaults; every other method panics so an accidental
// dependency expansion would surface immediately rather than hiding behind a
// nil-method false-pass.
type stubKBRepoForDocClass struct {
	interfaces.KnowledgeBaseRepository
	kbs    []*types.KnowledgeBase
	listEr error
	calls  int
}

func (s *stubKBRepoForDocClass) ListKnowledgeBases(ctx context.Context) ([]*types.KnowledgeBase, error) {
	s.calls++
	return s.kbs, s.listEr
}

// newDocClassFromYAML builds an in-memory DocClassConfig that mirrors the
// shape of doc_classes.yaml — title patterns plus kb_defaults — so the test
// covers the full Classify resolution order: title-pattern wins first,
// kb_defaults supplies the fallback only after RefreshDocClassKBDefaults
// has populated the runtime map.
func newDocClassFromYAML() *config.DocClassConfig {
	c := &config.DocClassConfig{
		Classes: []config.DocClass{
			{Name: "product", TitlePatterns: []string{`(?i)\bDatasheet\b`}},
		},
		KBDefaults: map[string]string{
			"Robustel_Datasheet": "product",
			"Marketing_Insight":  "competitive",
		},
	}
	c.Build()
	return c
}

func TestRefreshDocClassKBDefaults_PopulatesKBIDMapFromYAMLNames(t *testing.T) {
	dc := newDocClassFromYAML()

	// Title-pattern path works regardless of refresh (sanity check).
	if got := dc.Classify("eg71-datasheet-en.pdf", ""); got != "product" {
		t.Fatalf("title pattern broken: got %q want product", got)
	}

	// Before refresh: kb_defaults map is empty — fallback misses.
	if got := dc.Classify("notes.md", "kb-A-uuid"); got != "" {
		t.Fatalf("pre-refresh kb fallback must miss, got %q", got)
	}

	svc := &knowledgeBaseService{
		repo: &stubKBRepoForDocClass{kbs: []*types.KnowledgeBase{
			{ID: "kb-A-uuid", Name: "Robustel_Datasheet"},
			{ID: "kb-B-uuid", Name: "Marketing_Insight"},
			{ID: "kb-C-uuid", Name: "Unmapped"},
		}},
		cfg: &config.Config{DocClasses: dc},
	}

	svc.RefreshDocClassKBDefaults(context.Background())

	// After refresh: kb_defaults fallback resolves by KB ID.
	if got := dc.Classify("untitled.md", "kb-A-uuid"); got != "product" {
		t.Errorf("kb-A (Robustel_Datasheet) should default to product, got %q", got)
	}
	if got := dc.Classify("untitled.md", "kb-B-uuid"); got != "competitive" {
		t.Errorf("kb-B (Marketing_Insight) should default to competitive, got %q", got)
	}
	if got := dc.Classify("untitled.md", "kb-C-uuid"); got != "" {
		t.Errorf("kb-C (Unmapped) must stay empty, got %q", got)
	}

	// Title pattern still wins over kb_defaults — eg71-datasheet falls in kb-B
	// (Marketing_Insight=competitive) but the title pattern says product.
	if got := dc.Classify("eg71-datasheet-en.pdf", "kb-B-uuid"); got != "product" {
		t.Errorf("title pattern must win over kb_defaults, got %q", got)
	}
}

func TestRefreshDocClassKBDefaults_NoOpWhenDocClassesAbsent(t *testing.T) {
	// Deployment without doc_classes.yaml — refresh must not call into the
	// repository (cheaper, and avoids surprising error logs on startup).
	stub := &stubKBRepoForDocClass{}
	svc := &knowledgeBaseService{
		repo: stub,
		cfg:  &config.Config{DocClasses: nil},
	}
	svc.RefreshDocClassKBDefaults(context.Background())
	if stub.calls != 0 {
		t.Errorf("expected zero repo calls when DocClasses=nil, got %d", stub.calls)
	}
}

func TestRefreshDocClassKBDefaults_NoOpWhenKBDefaultsEmpty(t *testing.T) {
	// doc_classes.yaml present but with no kb_defaults section: the title-
	// pattern-only path is already self-sufficient; refresh skips the DB read.
	dc := &config.DocClassConfig{
		Classes:    []config.DocClass{{Name: "product", TitlePatterns: []string{`(?i)\bDatasheet\b`}}},
		KBDefaults: map[string]string{}, // explicitly empty
	}
	dc.Build()
	stub := &stubKBRepoForDocClass{}
	svc := &knowledgeBaseService{repo: stub, cfg: &config.Config{DocClasses: dc}}
	svc.RefreshDocClassKBDefaults(context.Background())
	if stub.calls != 0 {
		t.Errorf("expected zero repo calls when KBDefaults empty, got %d", stub.calls)
	}
}

func TestRefreshDocClassKBDefaults_ListFailureIsNonFatal(t *testing.T) {
	// If ListKnowledgeBases fails (DB hiccup at startup, etc.), the runtime
	// kb_defaults map stays as-is and the next mutation will retry. Refresh
	// must never panic or propagate.
	dc := newDocClassFromYAML()
	stub := &stubKBRepoForDocClass{listEr: errors.New("db unavailable")}
	svc := &knowledgeBaseService{repo: stub, cfg: &config.Config{DocClasses: dc}}
	svc.RefreshDocClassKBDefaults(context.Background())
	// Title-pattern path remains functional.
	if got := dc.Classify("eg71-Datasheet.pdf", ""); got != "product" {
		t.Errorf("title pattern broken after list failure: got %q", got)
	}
}

func TestRefreshDocClassKBDefaults_NilCfgSafe(t *testing.T) {
	// Defensive guard: tests / future call sites may construct a service
	// without cfg. The refresh must be a silent no-op rather than crash.
	stub := &stubKBRepoForDocClass{}
	svc := &knowledgeBaseService{repo: stub, cfg: nil}
	svc.RefreshDocClassKBDefaults(context.Background())
	if stub.calls != 0 {
		t.Errorf("nil cfg must short-circuit, got %d repo calls", stub.calls)
	}
}
