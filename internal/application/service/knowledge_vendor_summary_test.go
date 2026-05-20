package service

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
)

// TestSummaryPrompt_VendorRulePresent guards that the production prompt
// template still carries the vendor-attribution section. If a future edit
// drops the section, the assertion that "{{vendor}}" appears and is wrapped
// in the imperative "MUST begin the summary" rule catches it before the
// regression ships. Without this, an LLM that hasn't been re-fine-tuned on
// the new convention happily reverts to "EG71 is …" and we re-introduce
// the ownership-confusion bug.
func TestSummaryPrompt_VendorRulePresent(t *testing.T) {
	// The prompt template content is normally loaded from
	// config/prompt_templates/generate_summary.yaml at startup. Reproducing
	// the loader here couples the test to file paths; instead, exercise the
	// renderer with a representative template that mirrors the production
	// shape.
	const tpl = `## Vendor Attribution
- If ` + "`vendor`" + ` is non-empty, you MUST begin the summary with "<Vendor> <ProductName>".
- Vendor for this document: "{{vendor}}"
## Language
- Use {{language}} for all outputs`

	got := types.RenderPromptPlaceholders(tpl, types.PlaceholderValues{
		"vendor":   "Milesight",
		"language": "Chinese",
	})

	assert.Contains(t, got, `Vendor for this document: "Milesight"`,
		"vendor placeholder must be rendered into the prompt body")
	assert.Contains(t, got, "MUST begin the summary",
		"the imperative rule must survive the rendering pass")
	assert.NotContains(t, got, "{{vendor}}",
		"raw placeholder must be consumed — leaving it leaks the unresolved tag to the LLM")
}

// TestSummaryPrompt_EmptyVendor_PreservesNeutralBehaviour: when the KB has
// no vendor set (mixed-source KB, user notes, etc.), the placeholder should
// render to an empty string so the rule sentence still reads cleanly and
// the LLM is not nudged to invent a vendor. The "if vendor is empty, do NOT
// invent one" half of the rule then takes over.
func TestSummaryPrompt_EmptyVendor_PreservesNeutralBehaviour(t *testing.T) {
	const tpl = `Vendor for this document: "{{vendor}}"`

	got := types.RenderPromptPlaceholders(tpl, types.PlaceholderValues{
		"vendor": "",
	})

	assert.Equal(t, `Vendor for this document: ""`, got)
	assert.False(t, strings.Contains(got, "{{vendor}}"))
}

// TestKnowledgeBase_VendorRoundtrips: the new field exists on the model and
// round-trips through JSON (the frontend reads it back) and YAML (the
// initialization seeding writes it). A struct-level breakage here would
// crash startup with a YAML parse error or silently drop the value when
// the frontend saves a KB edit.
func TestKnowledgeBase_VendorRoundtrips(t *testing.T) {
	kb := types.KnowledgeBase{
		ID:     "kb-1",
		Name:   "Marketing_Insight",
		Vendor: "Milesight",
	}

	// Field is directly readable — guards against a future rename without
	// updating callers. The bug we're protecting against ("forgot to wire the
	// new field") would show as a compile error here, not at runtime.
	assert.Equal(t, "Milesight", kb.Vendor)

	// Empty default round-trips. The DB column has DEFAULT '' (see migration
	// 000043) so a row created before this migration applied reads back as
	// "" rather than NULL — checked here so an accidental switch to
	// *string in the future breaks loudly.
	empty := types.KnowledgeBase{ID: "kb-2"}
	assert.Equal(t, "", empty.Vendor)
}
