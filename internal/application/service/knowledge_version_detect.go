package service

// knowledge_version_detect.go — LLM-based document version extraction and
// duplicate detection for file-type knowledge.
//
// Problem addressed:
//   When the same product document is uploaded in multiple versions (e.g.
//   "R1511 Software Manual v1.0" followed by "R1511 Software Manual v5.5"),
//   WeKnora has no way to warn the user about the potential old-version
//   duplicate.  File-name heuristics fail for renamed documents; vector
//   similarity cannot distinguish version-upgrade from same-product-family.
//
// Solution:
//   During ProcessDocument, after chunks are available, feed the first N
//   chunks (cover page + opening sections) to the KB's summary LLM.  The
//   model extracts {product_model, doc_type, version, vendor} in a single
//   call (~2-3 s).  The result is written into knowledge.Metadata under the
//   key "_doc_version".  A second pass compares against all completed
//   documents in the same KB; if another doc has the same product_model +
//   doc_type but a different version, a structured warning is stored under
//   the key "_version_warning" in metadata.
//
// Graceful degradation:
//   Any error (no model configured, LLM timeout, JSON parse failure, DB
//   error) is logged at WARN level and silently skipped — document
//   processing is never blocked.

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	// docVersionDetectChunks is how many leading chunks are fed to the LLM.
	// Cover page + first section is almost always within the first 3 chunks.
	docVersionDetectChunks = 3

	// docVersionDetectMaxRunes caps the text sent to the LLM to avoid
	// exceeding small context windows.
	docVersionDetectMaxRunes = 4000

	// docVersionDetectTimeout is the per-call LLM timeout for metadata
	// extraction.  The call is intentionally cheap (small input, small
	// output), so 30 s is generous.
	docVersionDetectTimeout = 30 * time.Second

	// metaKeyDocVersion is the knowledge.Metadata key for extracted version info.
	metaKeyDocVersion = "_doc_version"

	// metaKeyVersionWarning is the knowledge.Metadata key for duplicate warning.
	metaKeyVersionWarning = "_version_warning"
)

// DocVersionInfo is the structured metadata extracted by the LLM from the
// opening sections of a document.
type DocVersionInfo struct {
	ProductModel string `json:"product_model"`
	DocType      string `json:"doc_type"`
	Version      string `json:"version"`
	Vendor       string `json:"vendor"`
}

// VersionDuplicateWarning is stored in knowledge.Metadata when an existing
// document in the same KB appears to be a different version of the same doc.
type VersionDuplicateWarning struct {
	// KnowledgeID of the potential duplicate.
	KnowledgeID string `json:"knowledge_id"`
	// Title of the potential duplicate.
	Title string `json:"title"`
	// Version string of the potential duplicate.
	Version string `json:"version"`
	// CurrentVersion is the version of the newly processed document.
	CurrentVersion string `json:"current_version"`
}

// extractAndStoreDocVersion runs LLM-based metadata extraction on the first
// N chunks of the document, persists the result in knowledge.Metadata, then
// checks for duplicate versions already present in the KB.
//
// The function is non-blocking on error — any failure is logged and skipped.
// It must be called after chunks are assembled and before processChunks.
func (s *knowledgeService) extractAndStoreDocVersion(
	ctx context.Context,
	kb *types.KnowledgeBase,
	knowledge *types.Knowledge,
	chunks []types.ParsedChunk,
) {
	// Only process file-type knowledge — manual / FAQ / URL passages are not
	// versioned product documents.
	if knowledge.Type != "file" {
		return
	}
	if len(chunks) == 0 {
		return
	}
	if kb.SummaryModelID == "" {
		logger.Debugf(ctx, "version detect: KB %s has no SummaryModelID, skipping", kb.ID)
		return
	}

	// ── Step 1: extract metadata from first N chunks ───────────────────────
	info, ok := s.extractDocVersionInfo(ctx, kb, knowledge, chunks)
	if !ok {
		return
	}

	// ── Step 2: persist _doc_version in knowledge.Metadata ─────────────────
	if err := s.mergeKnowledgeMetadata(ctx, knowledge, metaKeyDocVersion, info); err != nil {
		logger.Warnf(ctx, "version detect: failed to save _doc_version for knowledge %s: %v",
			knowledge.ID, err)
		return
	}

	// Only check duplicates when we have both product_model and doc_type —
	// without them the match would be too broad (false positives).
	if info.ProductModel == "" || info.DocType == "" {
		return
	}

	// ── Step 3: scan existing docs for potential duplicate ─────────────────
	warning := s.findVersionDuplicate(ctx, knowledge, info)
	if warning == nil {
		return
	}

	if err := s.mergeKnowledgeMetadata(ctx, knowledge, metaKeyVersionWarning, warning); err != nil {
		logger.Warnf(ctx, "version detect: failed to save _version_warning for knowledge %s: %v",
			knowledge.ID, err)
	} else {
		logger.Infof(ctx, "version detect: knowledge %s (%s v%s) may duplicate knowledge %s (%s v%s)",
			knowledge.ID, info.ProductModel, info.Version,
			warning.KnowledgeID, info.ProductModel, warning.Version)
	}
}

// extractDocVersionInfo calls the summary LLM with the leading chunks and
// parses the JSON response into DocVersionInfo.
func (s *knowledgeService) extractDocVersionInfo(
	ctx context.Context,
	kb *types.KnowledgeBase,
	knowledge *types.Knowledge,
	chunks []types.ParsedChunk,
) (DocVersionInfo, bool) {
	// Build input text from first N chunks, capped at docVersionDetectMaxRunes.
	var sb strings.Builder
	limit := docVersionDetectChunks
	if limit > len(chunks) {
		limit = len(chunks)
	}
	for i := 0; i < limit; i++ {
		sb.WriteString(chunks[i].Content)
		sb.WriteString("\n\n")
		if sb.Len() >= docVersionDetectMaxRunes {
			break
		}
	}
	text := []rune(sb.String())
	if len(text) > docVersionDetectMaxRunes {
		text = text[:docVersionDetectMaxRunes]
	}
	if len(text) == 0 {
		return DocVersionInfo{}, false
	}

	chatModel, err := s.modelService.GetChatModel(ctx, kb.SummaryModelID)
	if err != nil {
		logger.Warnf(ctx, "version detect: failed to get chat model for KB %s: %v", kb.ID, err)
		return DocVersionInfo{}, false
	}

	prompt := types.RenderPromptPlaceholders(agent.DocVersionDetectPrompt, types.PlaceholderValues{
		"content": string(text),
	})

	callCtx, callCancel := context.WithTimeout(context.Background(), docVersionDetectTimeout)
	defer callCancel()

	thinking := false
	resp, err := chatModel.Chat(callCtx, []chat.Message{
		{Role: "user", Content: prompt},
	}, &chat.ChatOptions{
		Temperature: 0.1,
		MaxTokens:   128,
		Thinking:    &thinking,
	})
	if err != nil {
		logger.Warnf(ctx, "version detect: LLM call failed for knowledge %s: %v", knowledge.ID, err)
		return DocVersionInfo{}, false
	}

	// Strip markdown fences if the model added them despite instructions.
	raw := strings.TrimSpace(resp.Content)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	var info DocVersionInfo
	if err := json.Unmarshal([]byte(raw), &info); err != nil {
		logger.Warnf(ctx, "version detect: JSON parse failed for knowledge %s (raw=%q): %v",
			knowledge.ID, raw, err)
		return DocVersionInfo{}, false
	}

	// Normalize: lowercase + trim for consistent comparison later.
	info.ProductModel = strings.TrimSpace(info.ProductModel)
	info.DocType = strings.TrimSpace(info.DocType)
	info.Version = strings.TrimSpace(info.Version)
	info.Vendor = strings.TrimSpace(info.Vendor)

	logger.Infof(ctx, "version detect: knowledge %s extracted — model=%q type=%q ver=%q vendor=%q",
		knowledge.ID, info.ProductModel, info.DocType, info.Version, info.Vendor)
	return info, true
}

// findVersionDuplicate queries all completed documents in the same KB and
// returns a warning if one matches product_model + doc_type but differs in
// version.
func (s *knowledgeService) findVersionDuplicate(
	ctx context.Context,
	knowledge *types.Knowledge,
	info DocVersionInfo,
) *VersionDuplicateWarning {
	existing, err := s.repo.ListKnowledgeByKnowledgeBaseID(ctx, knowledge.TenantID, knowledge.KnowledgeBaseID)
	if err != nil {
		logger.Warnf(ctx, "version detect: failed to list KB knowledge for duplicate check: %v", err)
		return nil
	}

	modelNorm := strings.ToLower(strings.ReplaceAll(info.ProductModel, " ", ""))
	typeNorm := strings.ToLower(strings.ReplaceAll(info.DocType, " ", ""))

	for _, k := range existing {
		if k.ID == knowledge.ID {
			continue
		}
		if k.ParseStatus != "completed" {
			continue
		}
		if k.Type != "file" {
			continue
		}

		meta := k.GetMetadata()
		raw, ok := meta[metaKeyDocVersion]
		if !ok {
			continue
		}
		var other DocVersionInfo
		if err := json.Unmarshal([]byte(raw), &other); err != nil {
			continue
		}

		otherModelNorm := strings.ToLower(strings.ReplaceAll(other.ProductModel, " ", ""))
		otherTypeNorm := strings.ToLower(strings.ReplaceAll(other.DocType, " ", ""))
		if otherModelNorm != modelNorm || otherTypeNorm != typeNorm {
			continue
		}
		if other.Version == info.Version {
			// Same version — exact duplicate (handled elsewhere or intentional).
			continue
		}

		return &VersionDuplicateWarning{
			KnowledgeID:    k.ID,
			Title:          k.Title,
			Version:        other.Version,
			CurrentVersion: info.Version,
		}
	}
	return nil
}

// mergeKnowledgeMetadata reads the current knowledge.Metadata JSON, adds or
// overwrites a single key with the marshaled value, then persists via
// UpdateKnowledge.  It also updates the in-memory knowledge.Metadata so
// subsequent code sees the updated value.
func (s *knowledgeService) mergeKnowledgeMetadata(
	ctx context.Context,
	knowledge *types.Knowledge,
	key string,
	value any,
) error {
	// Parse existing metadata into a generic map.
	existing := make(map[string]json.RawMessage)
	if len(knowledge.Metadata) > 0 {
		_ = json.Unmarshal(knowledge.Metadata, &existing)
	}

	// Marshal new value.
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	existing[key] = encoded

	// Write back.
	merged, err := json.Marshal(existing)
	if err != nil {
		return err
	}
	knowledge.Metadata = types.JSON(merged)
	knowledge.UpdatedAt = time.Now()

	dbCtx, dbCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer dbCancel()
	return s.repo.UpdateKnowledge(dbCtx, knowledge)
}
