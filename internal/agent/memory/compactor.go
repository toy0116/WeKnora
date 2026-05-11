// Package agentmemory — role-asymmetric compaction for long IM conversations.
//
// Problem this solves:
//   In a long IM session (e.g. WeChat Work group), each new turn's prompt
//   includes the full prior assistant-message text. If an earlier turn
//   contained a hallucination ("EG71 is a Robustel product"), that text
//   re-enters context every subsequent turn as a STATEMENT FROM THE MODEL.
//   The model treats its own past output as a strong factual prior — even
//   stronger than current-turn retrieved evidence. Successive turns
//   reinforce the same hallucination. This is the "conversation poisoning"
//   pattern observed in production wecom sessions where the same query was
//   asked 4× and produced the same EG71 misattribution each time.
//
// First-principles fix:
//   Past user messages are INTENT signals (what they asked, format
//   preferences) and should stay verbatim — they don't claim facts.
//   Past assistant messages contain FACT CLAIMS that this turn must
//   re-verify against current retrieval. Trusting them as-is violates
//   the RAG "evidence-first" principle.
//
//   Therefore: strip every past assistant message down to a structured
//   outline that preserves intent + format + what was looked at, but
//   removes the specific factual content. The next turn sees enough
//   continuity to follow up ("add another column to that table") but
//   cannot cite or rely on previously-generated specifics.
//
// Difference from the existing Consolidator:
//   Consolidator (consolidator.go) bulk-summarises OLDER messages when
//   token budget overflows, and its system prompt explicitly says
//   "preserve all key facts" — which BAKES hallucinations into the
//   summary. RoleAsymmetricCompactor operates per-message regardless of
//   token pressure, ALWAYS, with a prompt that explicitly STRIPS
//   factual claims. The two run in series: compact first, then
//   consolidate if still over budget.

package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
)

// compactorSystemPrompt instructs the LLM to extract a structured outline
// of an assistant message that's safe to feed back into future-turn context.
//
// Critical design decisions in the prompt:
//   - Output is XML, not free text — easier for downstream LLM to parse
//     and harder for it to confuse the outline with a factual statement.
//   - Explicit "strip ALL ENTITY ATTRIBUTIONS" instruction — this is the
//     hallucination class we're defending against.
//   - "high-level structure, no specific claims" forces the summariser
//     to abstract away facts (the failure mode of the existing
//     Consolidator is that it preserves them).
//   - "future turns will treat your outline as the ONLY thing they know"
//     primes the summariser to be conservative.
const compactorSystemPrompt = `You are an "outline extractor" preparing structured summaries of past AI assistant answers for use as context in future conversation turns.

Extract ONLY:
  - The user's intent (what they asked for, in concise form)
  - The output format chosen (table, list, paragraph, code, mixed)
  - A high-level outline of WHAT was answered (no specific factual claims)
  - The citations referenced if visible (KB doc names, wiki slugs)

STRIP entirely:
  - Specific facts the assistant stated about products, specs, features
  - Numbers, model names, technical attributes
  - ALL ENTITY ATTRIBUTIONS (whose product / whose feature / what brand owns what) — these may have been hallucinated and the next turn must re-verify
  - Any "I confirmed that X is Y" / "research shows Z" / "据资料 ..." style claims
  - Verbatim quotes that look like product specs

Output in this EXACT XML format (no markdown, no preamble, no explanation):

<assistant_turn outcome="answered|refused|partial">
  <user_intent>...</user_intent>
  <output_format>table|list|paragraph|code|mixed</output_format>
  <answer_outline>describe the STRUCTURE of the answer — "compared three products across N dimensions", "listed the user's 10 customers with a pitch each", "explained the EPDK compliance tiers" — NOT the specific claims</answer_outline>
  <citations_used>doc filenames or slugs only, one per line; empty if none visible</citations_used>
</assistant_turn>

CRITICAL: future turns will treat your outline as the only summary of the past response. Do NOT preserve factual claims that need re-verification.`

// Default token cap on the LLM output. The outline structure is small
// (~5 fields), 600 tokens is more than enough.
const compactorMaxTokens = 600

// Default per-message extraction timeout. The LLM call is small but we
// don't want a single slow message to block the whole turn.
const compactorTimeout = 30 * time.Second

// Minimum content length that triggers extraction. Short assistant
// messages (acknowledgements, brief refusals) don't carry the hallucination
// payload we're defending against, and aren't worth the LLM cost.
const compactorMinContentLen = 300

// Maximum concurrent extraction goroutines per batch. Caps fan-out on
// the chat-model provider so a large history doesn't overwhelm rate
// limits. 10 is conservative — provider clients typically support 50+.
const compactorMaxConcurrency = 10

// RoleAsymmetricCompactor compresses each assistant message in a history
// into a structured outline that preserves intent + format + citations
// but strips factual claims. User / system / tool messages pass through
// unchanged.
//
// Cache: outlines are keyed by SHA-256 of the input content for the
// lifetime of the process. Re-compaction across multiple turns of the
// same session is free after the first compute. Cache loss on restart
// is acceptable — outlines are deterministic and re-derive cheaply.
type RoleAsymmetricCompactor struct {
	chatModel chat.Chat

	// cache stores outline strings keyed by hex(sha256(content)).
	cache sync.Map // map[string]string
}

// NewRoleAsymmetricCompactor returns a compactor backed by the given chat
// model. Pass a small/cheap model — Haiku 4.5 or similar — since this runs
// once per qualifying assistant message at session-load time.
func NewRoleAsymmetricCompactor(chatModel chat.Chat) *RoleAsymmetricCompactor {
	return &RoleAsymmetricCompactor{chatModel: chatModel}
}

// Compact returns a new slice where every assistant message of length
// >= compactorMinContentLen has been replaced with its structured outline.
// User, system, tool, and short assistant messages pass through unchanged.
//
// Fail-open: if extraction fails for a given message (LLM error, timeout,
// nil chat model) the ORIGINAL content is preserved. The caller can opt
// into stricter behaviour later if needed.
//
// Concurrency: assistant messages are extracted in parallel up to
// compactorMaxConcurrency at a time. For a 36-assistant-message wecom
// session this brings end-to-end latency from ~30 s (sequential) to
// ~3-5 s (parallel batched).
func (c *RoleAsymmetricCompactor) Compact(
	ctx context.Context,
	messages []chat.Message,
) []chat.Message {
	if c == nil || c.chatModel == nil || len(messages) == 0 {
		return messages
	}

	// Identify which indices need extraction.
	type task struct {
		idx     int
		content string
	}
	tasks := make([]task, 0, len(messages))
	for i, m := range messages {
		if m.Role != "assistant" {
			continue
		}
		if len(m.Content) < compactorMinContentLen {
			continue
		}
		tasks = append(tasks, task{idx: i, content: m.Content})
	}
	if len(tasks) == 0 {
		return messages
	}

	// Bounded parallel extraction.
	sem := make(chan struct{}, compactorMaxConcurrency)
	results := make([]string, len(tasks))
	var wg sync.WaitGroup
	wg.Add(len(tasks))
	for ti := range tasks {
		go func(ti int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			outline, err := c.extractCached(ctx, tasks[ti].content)
			if err != nil {
				logger.Warnf(ctx, "[Compactor] extraction failed for message %d: %v (keeping raw)",
					tasks[ti].idx, err)
				return // results[ti] stays ""
			}
			results[ti] = outline
		}(ti)
	}
	wg.Wait()

	// Build output slice — copy through unchanged for non-assistant and
	// failed extractions, substitute outline where successful.
	out := make([]chat.Message, len(messages))
	copy(out, messages)
	compacted := 0
	for ti, r := range results {
		if r == "" {
			continue
		}
		out[tasks[ti].idx].Content = r
		compacted++
	}
	if compacted > 0 {
		logger.Infof(ctx, "[Compactor] Compacted %d/%d assistant messages",
			compacted, len(tasks))
	}
	return out
}

// extractCached returns the outline for the given content, hitting the
// in-memory cache if available.
func (c *RoleAsymmetricCompactor) extractCached(
	ctx context.Context, content string,
) (string, error) {
	hash := sha256.Sum256([]byte(content))
	key := hex.EncodeToString(hash[:])
	if v, ok := c.cache.Load(key); ok {
		return v.(string), nil
	}
	outline, err := c.extract(ctx, content)
	if err != nil {
		return "", err
	}
	c.cache.Store(key, outline)
	return outline, nil
}

// extract performs one LLM call to derive the outline of `content`.
func (c *RoleAsymmetricCompactor) extract(
	ctx context.Context, content string,
) (string, error) {
	if c.chatModel == nil {
		return "", fmt.Errorf("compactor: no chat model configured")
	}
	callCtx, cancel := context.WithTimeout(ctx, compactorTimeout)
	defer cancel()
	resp, err := c.chatModel.Chat(callCtx, []chat.Message{
		{Role: "system", Content: compactorSystemPrompt},
		{Role: "user", Content: content},
	}, &chat.ChatOptions{
		Temperature: 0.1,
		MaxTokens:   compactorMaxTokens,
	})
	if err != nil {
		return "", err
	}
	if resp == nil {
		return "", fmt.Errorf("compactor: nil chat response")
	}
	if resp.Content == "" {
		return "", fmt.Errorf("compactor: empty chat response")
	}
	return resp.Content, nil
}
