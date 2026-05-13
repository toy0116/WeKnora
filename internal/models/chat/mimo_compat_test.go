package chat

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/models/provider"
	"github.com/stretchr/testify/assert"
)

// ── ConvertMessages: reasoning_content round-trip ──
//
// MiMo and DeepSeek V3.2+ thinking-mode reject multi-turn requests where
// the prior assistant message lacks its reasoning_content. We now rely on
// go-openai's native ReasoningContent field (replaces our old custom raw-
// HTTP MiMo request struct). These tests pin the forward path so a future
// refactor of ConvertMessages can't silently drop the field.

func TestConvertMessages_ForwardsReasoningContentOnAssistantTurn(t *testing.T) {
	c := newTestRemoteChat(t)
	msgs := []Message{
		{Role: "user", Content: "what is the weather?"},
		{
			Role:             "assistant",
			Content:          "Let me check.",
			ReasoningContent: "User asks for weather; I should call the tool.",
			ToolCalls: []ToolCall{
				{ID: "call_1", Type: "function",
					Function: FunctionCall{Name: "weather", Arguments: `{"city":"SF"}`}},
			},
		},
		{Role: "tool", ToolCallID: "call_1", Name: "weather", Content: "72F sunny"},
	}
	out := c.ConvertMessages(msgs)
	// Find the assistant message in the output and verify reasoning_content
	// made it onto the OpenAI-shaped struct.
	var found bool
	for _, m := range out {
		if m.Role == "assistant" {
			found = true
			assert.Equal(t, "User asks for weather; I should call the tool.", m.ReasoningContent,
				"assistant ReasoningContent must round-trip through ConvertMessages")
		}
	}
	assert.True(t, found, "no assistant message in output")
}

func TestConvertMessages_SkipsReasoningContentOnNonAssistantTurns(t *testing.T) {
	c := newTestRemoteChat(t)
	msgs := []Message{
		// User and tool messages should never get ReasoningContent set on
		// the wire — only assistant turns carry the API's reasoning_content.
		{Role: "user", Content: "hi", ReasoningContent: "should not leak"},
		{Role: "tool", ToolCallID: "x", Name: "z", Content: "result",
			ReasoningContent: "should not leak"},
	}
	out := c.ConvertMessages(msgs)
	for _, m := range out {
		assert.Empty(t, m.ReasoningContent,
			"non-assistant turn must not carry reasoning_content (role=%s)", m.Role)
	}
}

func TestConvertMessages_AssistantWithEmptyReasoningStaysEmpty(t *testing.T) {
	// Legacy / fresh assistant message with no reasoning_content yet — must
	// not produce a spurious empty-string field (omitempty handles JSON, but
	// the Go struct should also stay zero so downstream byte-equality on
	// "had a reasoning_content" branches still work).
	c := newTestRemoteChat(t)
	msgs := []Message{
		{Role: "assistant", Content: "hi"},
	}
	out := c.ConvertMessages(msgs)
	assert.Equal(t, "", out[0].ReasoningContent)
}

// ── sanitizeMimoMessages: legacy-session compatibility ──
//
// Sessions started before the ReasoningContent fix landed have stored
// assistant messages with tool_calls but no reasoning_content. Even on
// iteration 0 the entire conversation history is sent to MiMo, which
// rejects any request where an assistant+tool_calls pair lacks
// reasoning_content. We drop those message groups entirely so the request
// stops 400-ing — context loss is preferable to a stuck session.

func TestSanitizeMimoMessages_DropsOrphanAssistantToolGroups(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "Q1"},
		{
			// Bad: tool_calls without reasoning_content. Must be dropped
			// along with its tool result.
			Role: "assistant", Content: "thinking",
			ToolCalls: []ToolCall{{ID: "bad_1", Type: "function",
				Function: FunctionCall{Name: "x"}}},
		},
		{Role: "tool", ToolCallID: "bad_1", Content: "bad_result"},
		{Role: "user", Content: "Q2"},
	}
	out := sanitizeMimoMessages(msgs)
	assert.Len(t, out, 2, "should drop both the bad assistant + its tool result")
	assert.Equal(t, "Q1", out[0].Content)
	assert.Equal(t, "Q2", out[1].Content)
}

func TestSanitizeMimoMessages_KeepsAssistantWithReasoning(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "Q"},
		{
			Role: "assistant", Content: "thinking",
			ReasoningContent: "the model's chain of thought",
			ToolCalls: []ToolCall{{ID: "good_1", Type: "function",
				Function: FunctionCall{Name: "y"}}},
		},
		{Role: "tool", ToolCallID: "good_1", Content: "good_result"},
	}
	out := sanitizeMimoMessages(msgs)
	assert.Len(t, out, 3, "valid groups must pass through untouched")
}

func TestSanitizeMimoMessages_KeepsAssistantWithoutToolCalls(t *testing.T) {
	// An assistant turn that just answers (no tool_calls) is fine even
	// without reasoning_content — MiMo only enforces the field for
	// tool-calling turns.
	msgs := []Message{
		{Role: "user", Content: "Q"},
		{Role: "assistant", Content: "A"}, // no reasoning, no tools
	}
	out := sanitizeMimoMessages(msgs)
	assert.Len(t, out, 2)
}

func TestSanitizeMimoMessages_DropsOnlyOrphanedToolResults(t *testing.T) {
	// Mixed history: one bad group + one good group. Drop only the bad one
	// (assistant + its specific tool_call_id), not the good group's tool
	// results.
	msgs := []Message{
		{Role: "user", Content: "Q1"},
		{
			Role:      "assistant", Content: "thinking-bad",
			ToolCalls: []ToolCall{{ID: "bad", Type: "function"}},
		},
		{Role: "tool", ToolCallID: "bad", Content: "bad_result"},
		{Role: "user", Content: "Q2"},
		{
			Role: "assistant", Content: "thinking-good",
			ReasoningContent: "thoughts",
			ToolCalls:        []ToolCall{{ID: "good", Type: "function"}},
		},
		{Role: "tool", ToolCallID: "good", Content: "good_result"},
	}
	out := sanitizeMimoMessages(msgs)
	assert.Len(t, out, 4)
	// Verify the good tool result survives + the bad one is gone.
	var sawGoodTool, sawBadTool bool
	for _, m := range out {
		if m.Role == "tool" && m.ToolCallID == "good" {
			sawGoodTool = true
		}
		if m.Role == "tool" && m.ToolCallID == "bad" {
			sawBadTool = true
		}
	}
	assert.True(t, sawGoodTool, "good tool result must survive")
	assert.False(t, sawBadTool, "bad tool result must be dropped")
}

func TestSanitizeMimoMessages_EmptyAndSingleMessage(t *testing.T) {
	// Defensive: empty + single-message inputs must not panic.
	assert.Empty(t, sanitizeMimoMessages(nil))
	assert.Empty(t, sanitizeMimoMessages([]Message{}))
	single := []Message{{Role: "user", Content: "Q"}}
	assert.Equal(t, single, sanitizeMimoMessages(single))
}

// ── BuildChatCompletionRequest: provider gate ──
//
// sanitizeMimoMessages must only run for the MiMo provider; for everyone
// else it'd silently drop legitimate tool_call assistant messages that
// don't (and shouldn't) carry reasoning_content.

func TestBuildChatCompletionRequest_SanitizesOnlyForMimo(t *testing.T) {
	t.Run("non-mimo passes legacy tool_call messages through", func(t *testing.T) {
		c := newTestRemoteChat(t)
		c.provider = provider.ProviderOpenAI
		msgs := []Message{
			{Role: "user", Content: "Q"},
			{Role: "assistant", Content: "thinking",
				ToolCalls: []ToolCall{{ID: "x", Type: "function",
					Function: FunctionCall{Name: "tool"}}}},
			{Role: "tool", ToolCallID: "x", Content: "result"},
		}
		req := c.BuildChatCompletionRequest(msgs, nil, false)
		assert.Len(t, req.Messages, 3, "OpenAI must not have its tool history sanitized")
	})

	t.Run("mimo drops the orphan tool_call group", func(t *testing.T) {
		c := newTestRemoteChat(t)
		c.provider = provider.ProviderMimo
		msgs := []Message{
			{Role: "user", Content: "Q"},
			{Role: "assistant", Content: "thinking",
				ToolCalls: []ToolCall{{ID: "x", Type: "function",
					Function: FunctionCall{Name: "tool"}}}},
			{Role: "tool", ToolCallID: "x", Content: "result"},
		}
		req := c.BuildChatCompletionRequest(msgs, nil, false)
		assert.Len(t, req.Messages, 1, "MiMo must drop the orphan assistant + tool")
		assert.Equal(t, "user", req.Messages[0].Role)
	})
}
