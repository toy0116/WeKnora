package memory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

// countingStubChat counts how many LLM calls fire so cache-hit tests can
// distinguish "cached" from "freshly invoked".
type countingStubChat struct {
	response string
	err      error
	calls    int32
}

func (s *countingStubChat) Chat(_ context.Context, _ []chat.Message, _ *chat.ChatOptions) (*types.ChatResponse, error) {
	atomic.AddInt32(&s.calls, 1)
	if s.err != nil {
		return nil, s.err
	}
	return &types.ChatResponse{Content: s.response}, nil
}

func (s *countingStubChat) ChatStream(context.Context, []chat.Message, *chat.ChatOptions) (<-chan types.StreamResponse, error) {
	return nil, nil
}

func (s *countingStubChat) GetModelID() string   { return "test-stub" }
func (s *countingStubChat) GetModelName() string { return "test-stub" }

// longAssistantBody returns content of at least compactorMinContentLen so
// the message qualifies for compaction.
func longAssistantBody(prefix string) string {
	return prefix + strings.Repeat(" filler text that pushes the message past the min-length threshold.", 8)
}

func TestCompactor_NilModelPassesThrough(t *testing.T) {
	c := NewRoleAsymmetricCompactor(nil)
	in := []chat.Message{{Role: "assistant", Content: longAssistantBody("hello")}}
	out := c.Compact(context.Background(), in)
	if !messagesEqual(in, out) {
		t.Errorf("nil chat model must pass through, got %+v", out)
	}
}

func TestCompactor_UserMessagesUntouched(t *testing.T) {
	stub := &countingStubChat{response: "<assistant_turn outcome=\"answered\">...</assistant_turn>"}
	c := NewRoleAsymmetricCompactor(stub)

	in := []chat.Message{
		{Role: "user", Content: longAssistantBody("user content that is also long")},
		{Role: "system", Content: longAssistantBody("system prompt content")},
		{Role: "tool", Content: longAssistantBody("tool result content")},
	}
	out := c.Compact(context.Background(), in)
	if !messagesEqual(in, out) {
		t.Errorf("non-assistant messages must pass through unchanged")
	}
	if atomic.LoadInt32(&stub.calls) != 0 {
		t.Errorf("no LLM calls expected for non-assistant messages, got %d", stub.calls)
	}
}

func TestCompactor_ShortAssistantSkipped(t *testing.T) {
	stub := &countingStubChat{response: "<assistant_turn>...</assistant_turn>"}
	c := NewRoleAsymmetricCompactor(stub)

	in := []chat.Message{{Role: "assistant", Content: "short reply"}}
	out := c.Compact(context.Background(), in)
	if !messagesEqual(in, out) {
		t.Errorf("short assistant message must skip compaction")
	}
	if atomic.LoadInt32(&stub.calls) != 0 {
		t.Errorf("no LLM call expected for short msg, got %d", stub.calls)
	}
}

func TestCompactor_LongAssistantReplacedWithOutline(t *testing.T) {
	outline := `<assistant_turn outcome="answered">
  <user_intent>询问产品列表</user_intent>
  <output_format>table</output_format>
  <answer_outline>列举了三款产品的硬件规格表</answer_outline>
  <citations_used></citations_used>
</assistant_turn>`
	stub := &countingStubChat{response: outline}
	c := NewRoleAsymmetricCompactor(stub)

	original := longAssistantBody("Robustel EG5120 supports 5G and Modbus...")
	in := []chat.Message{{Role: "assistant", Content: original}}
	out := c.Compact(context.Background(), in)

	if out[0].Content != outline {
		t.Errorf("expected outline replacement, got %q", out[0].Content)
	}
	if atomic.LoadInt32(&stub.calls) != 1 {
		t.Errorf("expected 1 LLM call, got %d", stub.calls)
	}
}

func TestCompactor_CacheReusesOutline(t *testing.T) {
	stub := &countingStubChat{response: "<assistant_turn>cached</assistant_turn>"}
	c := NewRoleAsymmetricCompactor(stub)

	body := longAssistantBody("identical content across multiple turns")
	for i := 0; i < 5; i++ {
		c.Compact(context.Background(), []chat.Message{{Role: "assistant", Content: body}})
	}
	// 5 compactions of identical content → 1 LLM call (cache hits).
	if got := atomic.LoadInt32(&stub.calls); got != 1 {
		t.Errorf("expected 1 LLM call (cache), got %d", got)
	}
}

func TestCompactor_FailOpenOnError(t *testing.T) {
	stub := &countingStubChat{err: errors.New("simulated provider failure")}
	c := NewRoleAsymmetricCompactor(stub)

	body := longAssistantBody("Robustel R1520LG specs...")
	in := []chat.Message{{Role: "assistant", Content: body}}
	out := c.Compact(context.Background(), in)

	// Fail-open: original content preserved when LLM errors.
	if out[0].Content != body {
		t.Errorf("fail-open expected, got mutation %q", out[0].Content)
	}
}

func TestCompactor_EmptyResponseTreatedAsFailure(t *testing.T) {
	stub := &countingStubChat{response: ""}
	c := NewRoleAsymmetricCompactor(stub)

	body := longAssistantBody("any content")
	in := []chat.Message{{Role: "assistant", Content: body}}
	out := c.Compact(context.Background(), in)

	if out[0].Content != body {
		t.Errorf("empty LLM response must trigger fail-open, got %q", out[0].Content)
	}
}

func TestCompactor_ParallelExtraction(t *testing.T) {
	// 12 unique long messages run via the compactor. With compactorMaxConcurrency=10
	// the calls fan out; result counts must match unique inputs.
	stub := &countingStubChat{response: "<assistant_turn>parallel</assistant_turn>"}
	c := NewRoleAsymmetricCompactor(stub)

	in := make([]chat.Message, 12)
	for i := range in {
		in[i] = chat.Message{
			Role:    "assistant",
			Content: longAssistantBody("unique content " + fmt.Sprint(i)),
		}
	}
	out := c.Compact(context.Background(), in)
	for i := range out {
		if !strings.Contains(out[i].Content, "<assistant_turn>parallel") {
			t.Errorf("msg %d not compacted: %q", i, out[i].Content)
		}
	}
	if atomic.LoadInt32(&stub.calls) != 12 {
		t.Errorf("expected 12 LLM calls (no cache collisions), got %d", stub.calls)
	}
}

func TestCompactor_MixedHistoryPreservesOrderAndRoles(t *testing.T) {
	stub := &countingStubChat{response: "<assistant_turn>X</assistant_turn>"}
	c := NewRoleAsymmetricCompactor(stub)

	in := []chat.Message{
		{Role: "system", Content: longAssistantBody("system")},
		{Role: "user", Content: "first user question (short ok)"},
		{Role: "assistant", Content: longAssistantBody("assistant reply 1")},
		{Role: "user", Content: "follow-up"},
		{Role: "assistant", Content: longAssistantBody("assistant reply 2")},
	}
	out := c.Compact(context.Background(), in)
	if len(out) != len(in) {
		t.Fatalf("length mismatch: %d vs %d", len(out), len(in))
	}
	for i, m := range out {
		if m.Role != in[i].Role {
			t.Errorf("role at %d changed: %q → %q", i, in[i].Role, m.Role)
		}
	}
	// User + system content untouched.
	if out[0].Content != in[0].Content {
		t.Errorf("system content mutated")
	}
	if out[1].Content != in[1].Content {
		t.Errorf("user content mutated")
	}
	// Assistant content replaced.
	if !strings.Contains(out[2].Content, "<assistant_turn>X") {
		t.Errorf("assistant 1 not compacted: %q", out[2].Content)
	}
	if !strings.Contains(out[4].Content, "<assistant_turn>X") {
		t.Errorf("assistant 2 not compacted: %q", out[4].Content)
	}
}

// messagesEqual is a small helper for Content+Role identity comparison.
func messagesEqual(a, b []chat.Message) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Role != b[i].Role || a[i].Content != b[i].Content {
			return false
		}
	}
	return true
}
