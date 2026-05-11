package chatpipeline

import (
	"context"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/searchutil"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
)

// PluginIntoChatMessage handles the transformation of search results into chat messages
type PluginIntoChatMessage struct {
	messageService interfaces.MessageService
	config         *config.Config
}

// NewPluginIntoChatMessage creates and registers a new PluginIntoChatMessage instance
func NewPluginIntoChatMessage(eventManager *EventManager, messageService interfaces.MessageService, cfg *config.Config) *PluginIntoChatMessage {
	res := &PluginIntoChatMessage{messageService: messageService, config: cfg}
	eventManager.Register(res)
	return res
}

// ActivationEvents returns the event types this plugin handles
func (p *PluginIntoChatMessage) ActivationEvents() []types.EventType {
	return []types.EventType{types.INTO_CHAT_MESSAGE}
}

// OnEvent processes the INTO_CHAT_MESSAGE event to format chat message content
func (p *PluginIntoChatMessage) OnEvent(ctx context.Context,
	eventType types.EventType, chatManage *types.ChatManage, next func() *PluginError,
) *PluginError {
	pipelineInfo(ctx, "IntoChatMessage", "input", map[string]interface{}{
		"session_id":       chatManage.SessionID,
		"merge_result_cnt": len(chatManage.MergeResult),
		"template_len":     len(chatManage.SummaryConfig.ContextTemplate),
	})

	// Separate FAQ and document results when FAQ priority is enabled
	var faqResults, docResults []*types.SearchResult
	var hasHighConfidenceFAQ bool

	if chatManage.FAQPriorityEnabled {
		for _, result := range chatManage.MergeResult {
			if result.ChunkType == string(types.ChunkTypeFAQ) {
				faqResults = append(faqResults, result)
				// Check if this FAQ has high confidence (above direct answer threshold)
				if result.Score >= chatManage.FAQDirectAnswerThreshold && !hasHighConfidenceFAQ {
					hasHighConfidenceFAQ = true
					pipelineInfo(ctx, "IntoChatMessage", "high_confidence_faq", map[string]interface{}{
						"chunk_id":  result.ID,
						"score":     fmt.Sprintf("%.4f", result.Score),
						"threshold": chatManage.FAQDirectAnswerThreshold,
					})
				}
			} else {
				docResults = append(docResults, result)
			}
		}
		pipelineInfo(ctx, "IntoChatMessage", "faq_separation", map[string]interface{}{
			"faq_count":           len(faqResults),
			"doc_count":           len(docResults),
			"has_high_confidence": hasHighConfidenceFAQ,
		})
	}

	// 验证用户查询的安全性
	safeQuery, isValid := utils.ValidateInput(chatManage.Query)
	if !isValid {
		pipelineWarn(ctx, "IntoChatMessage", "invalid_query", map[string]interface{}{
			"session_id": chatManage.SessionID,
		})
		return ErrTemplateExecute.WithError(fmt.Errorf("user query contains invalid content"))
	}

	// Intent-based no-search path: no retrieval results, but still render
	// through the context template so runtime metadata (current_time, etc.) is injected.
	if !chatManage.NeedsRetrieval() {
		userContent := safeQuery
		if rewrite := strings.TrimSpace(chatManage.RewriteQuery); rewrite != "" {
			if safeRewrite, ok := utils.ValidateInput(rewrite); ok {
				userContent = safeRewrite
			} else {
				pipelineWarn(ctx, "IntoChatMessage", "invalid_rewrite_query_fallback", map[string]interface{}{
					"session_id": chatManage.SessionID,
				})
			}
		}
		if chatManage.ImageDescription != "" && !chatManage.ChatModelSupportsVision {
			userContent += "\n\n[用户上传图片内容]\n" + chatManage.ImageDescription
		}
		if chatManage.QuotedContext != "" {
			userContent += "\n\n" + chatManage.QuotedContext
		}
		// Inject attachment content (documents, audio transcripts, etc.)
		if len(chatManage.Attachments) > 0 {
			userContent += chatManage.Attachments.BuildPrompt()
		}

		if tpl := chatManage.SummaryConfig.ContextTemplate; tpl != "" {
			chatManage.UserContent = types.RenderPromptPlaceholders(tpl, types.PlaceholderValues{
				"query":    userContent,
				"contexts": "",
				"language": chatManage.Language,
			})
		} else {
			chatManage.UserContent = userContent
		}

		pipelineInfo(ctx, "IntoChatMessage", "no_search_with_template", map[string]interface{}{
			"session_id":       chatManage.SessionID,
			"user_content_len": len(chatManage.UserContent),
			"has_template":     chatManage.SummaryConfig.ContextTemplate != "",
		})
		return next()
	}

	// Direction B: tag KB chunks whose entity family mismatches the query anchor.
	// This stampsMetadata["entity_mismatch"]="true" and Metadata["entity_owner"]="X"
	// on mismatched chunks BEFORE we render them into XML, so buildContextAttributes
	// can surface the signal to the LLM.
	if p.config != nil && p.config.EntityAliases != nil {
		tagEntityMismatches(ctx,
			chatManage.Query,
			chatManage.RewriteQuery,
			p.config.EntityAliases,
			chatManage.MergeResult,
		)
	}

	// Direction C: tag each KB chunk with its source-document doc_class
	// (product / strategy / competitive / research / training / solution).
	// Skips web-search chunks since classification keys off knowledge titles.
	// Sets r.Metadata["doc_class"] so buildContextAttributes can emit it.
	if p.config != nil && p.config.DocClasses != nil {
		for _, r := range chatManage.MergeResult {
			isWeb := strings.ToLower(r.KnowledgeSource) == "web_search" ||
				r.ChunkType == string(types.ChunkTypeWebSearch)
			if isWeb {
				continue
			}
			docName := r.KnowledgeTitle
			if docName == "" {
				docName = r.KnowledgeFilename
			}
			if cls := p.config.DocClasses.Classify(docName); cls != "" {
				r.Metadata = ensureMetadata(r.Metadata)
				r.Metadata["doc_class"] = cls
			}
		}
	}

	var contextsBuilder strings.Builder

	// Collect unique document metadata (title + description), once per knowledge
	allResults := chatManage.MergeResult
	if chatManage.FAQPriorityEnabled && len(faqResults) > 0 {
		allResults = append(faqResults, docResults...)
	}
	docHeader := buildDocumentHeader(allResults)
	if docHeader != "" {
		contextsBuilder.WriteString(docHeader)
		contextsBuilder.WriteString("\n")
	}

	// Build contexts string based on FAQ priority strategy
	if chatManage.FAQPriorityEnabled && len(faqResults) > 0 {
		contextsBuilder.WriteString("<source type=\"faq\" priority=\"high\">\n")
		for i, result := range faqResults {
			passage := getEnrichedPassageForChat(ctx, result)
			contextAttrs := buildContextAttributes(result)
			if hasHighConfidenceFAQ && i == 0 {
				contextsBuilder.WriteString(fmt.Sprintf("<context id=\"FAQ-%d\" match=\"exact\"%s>%s</context>\n", i+1, contextAttrs, passage))
			} else {
				contextsBuilder.WriteString(fmt.Sprintf("<context id=\"FAQ-%d\"%s>%s</context>\n", i+1, contextAttrs, passage))
			}
		}
		contextsBuilder.WriteString("</source>\n")

		if len(docResults) > 0 {
			contextsBuilder.WriteString("<source type=\"document\" priority=\"supplementary\">\n")
			for i, result := range docResults {
				passage := getEnrichedPassageForChat(ctx, result)
				contextAttrs := buildContextAttributes(result)
				contextsBuilder.WriteString(fmt.Sprintf("<context id=\"DOC-%d\"%s>%s</context>\n", i+1, contextAttrs, passage))
			}
			contextsBuilder.WriteString("</source>")
		}
	} else {
		for i, result := range chatManage.MergeResult {
			passage := getEnrichedPassageForChat(ctx, result)
			if i > 0 {
				contextsBuilder.WriteString("\n")
			}
			contextAttrs := buildContextAttributes(result)
			contextsBuilder.WriteString(fmt.Sprintf("<context id=\"%d\"%s>%s</context>", i+1, contextAttrs, passage))
		}
	}

	chatManage.RenderedContexts = contextsBuilder.String()

	// Replace placeholders in context template
	userContent := types.RenderPromptPlaceholders(chatManage.SummaryConfig.ContextTemplate, types.PlaceholderValues{
		"query":    safeQuery,
		"contexts": chatManage.RenderedContexts,
		"language": chatManage.Language,
	})

	// Append image description as text fallback only when the chat model cannot
	// process images directly. Vision-capable models see images via MultiContent.
	if chatManage.ImageDescription != "" && !chatManage.ChatModelSupportsVision {
		userContent += "\n\n[用户上传图片内容]\n" + chatManage.ImageDescription
	}
	if chatManage.QuotedContext != "" {
		userContent += "\n\n" + chatManage.QuotedContext
	}
	// Inject attachment content (documents, audio transcripts, etc.)
	if len(chatManage.Attachments) > 0 {
		userContent += chatManage.Attachments.BuildPrompt()
	}

	// Set formatted content back to chat management
	chatManage.UserContent = userContent
	pipelineInfo(ctx, "IntoChatMessage", "output", map[string]interface{}{
		"session_id":                 chatManage.SessionID,
		"user_content_len":           len(chatManage.UserContent),
		"faq_priority":               chatManage.FAQPriorityEnabled,
		"intent":                     chatManage.Intent,
		"image_description":          chatManage.ImageDescription,
		"chat_model_supports_vision": chatManage.ChatModelSupportsVision,
	})

	p.persistRenderedContent(ctx, chatManage)
	return next()
}

// persistRenderedContent asynchronously writes the RAG-augmented UserContent back
// to the user message so that subsequent conversation turns can see the full
// retrieval context in history.
func (p *PluginIntoChatMessage) persistRenderedContent(ctx context.Context, chatManage *types.ChatManage) {
	if chatManage.UserMessageID == "" || chatManage.UserContent == "" {
		pipelineInfo(ctx, "IntoChatMessage", "persist_rendered_content_skip", map[string]interface{}{
			"session_id":       chatManage.SessionID,
			"user_message_id":  chatManage.UserMessageID,
			"has_user_content": chatManage.UserContent != "",
			"reason":           "empty_id_or_content",
		})
		return
	}
	if chatManage.UserContent == chatManage.Query {
		return
	}
	pipelineInfo(ctx, "IntoChatMessage", "persist_rendered_content", map[string]interface{}{
		"session_id":           chatManage.SessionID,
		"user_message_id":      chatManage.UserMessageID,
		"rendered_content_len": len(chatManage.UserContent),
	})
	bgCtx := context.WithoutCancel(ctx)
	go func() {
		if err := p.messageService.UpdateMessageRenderedContent(
			bgCtx, chatManage.SessionID, chatManage.UserMessageID, chatManage.UserContent,
		); err != nil {
			pipelineWarn(bgCtx, "IntoChatMessage", "persist_rendered_content_error", map[string]interface{}{
				"session_id":      chatManage.SessionID,
				"user_message_id": chatManage.UserMessageID,
				"error":           err.Error(),
			})
		}
	}()
}

// buildDocumentHeader generates a source inventory listing each unique document
// or web search source used in the retrieval results.
//
// The header is split into two clearly labelled sections:
//   - <knowledge_base_documents>: chunks from internal KB files
//   - <web_search_sources>:       chunks from live web search
//
// This gives the LLM an upfront map of ALL sources so it can maintain strict
// entity-fact binding (e.g., never attribute EBDS facts to Airicom just because
// both appear in the same context window).
func buildDocumentHeader(results []*types.SearchResult) string {
	type kbDoc struct {
		title       string
		description string
	}
	type webSource struct {
		title string
		url   string
	}

	seenKB := make(map[string]struct{})
	seenWeb := make(map[string]struct{})
	var kbDocs []kbDoc
	var webSources []webSource

	for _, r := range results {
		isWeb := strings.ToLower(r.KnowledgeSource) == "web_search" || r.ChunkType == string(types.ChunkTypeWebSearch)

		if isWeb {
			// Use URL as dedup key; fall back to KnowledgeID
			key := ""
			if r.Metadata != nil {
				key = r.Metadata["url"]
			}
			if key == "" {
				key = r.KnowledgeID
			}
			if key == "" {
				continue
			}
			if _, ok := seenWeb[key]; ok {
				continue
			}
			seenWeb[key] = struct{}{}
			title := r.KnowledgeTitle
			if title == "" && r.Metadata != nil {
				title = r.Metadata["title"]
			}
			webSources = append(webSources, webSource{title: title, url: key})
		} else {
			if r.KnowledgeID == "" {
				continue
			}
			if _, ok := seenKB[r.KnowledgeID]; ok {
				continue
			}
			seenKB[r.KnowledgeID] = struct{}{}
			title := r.KnowledgeTitle
			if title == "" {
				title = r.KnowledgeFilename
			}
			if title == "" {
				continue
			}
			kbDocs = append(kbDocs, kbDoc{title: title, description: r.KnowledgeDescription})
		}
	}

	if len(kbDocs) == 0 && len(webSources) == 0 {
		return ""
	}

	var b strings.Builder
	if len(kbDocs) > 0 {
		b.WriteString("<knowledge_base_documents>\n")
		for _, d := range kbDocs {
			b.WriteString("<document>\n")
			b.WriteString(fmt.Sprintf("<title>%s</title>\n", d.title))
			if d.description != "" {
				b.WriteString(fmt.Sprintf("<description>%s</description>\n", d.description))
			}
			b.WriteString("</document>\n")
		}
		b.WriteString("</knowledge_base_documents>")
	}
	if len(webSources) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("<web_search_sources>\n")
		for _, s := range webSources {
			b.WriteString("<source>\n")
			if s.title != "" {
				b.WriteString(fmt.Sprintf("<title>%s</title>\n", s.title))
			}
			b.WriteString(fmt.Sprintf("<url>%s</url>\n", s.url))
			b.WriteString("</source>\n")
		}
		b.WriteString("</web_search_sources>")
	}
	return b.String()
}

// buildContextAttributes returns an XML attribute string that annotates each
// context chunk with its source type and identity. This explicit provenance
// labelling helps the LLM maintain strict entity-fact binding when KB chunks
// and web search chunks about different entities land in the same context window.
//
// Examples:
//
//	KB result  → source_type="knowledge_base" source_doc="关于LoRaWAN机遇.md"
//	Web result → source_type="web_search" source_title="Airicom official site" source_url="https://..."
func buildContextAttributes(r *types.SearchResult) string {
	isWeb := strings.ToLower(r.KnowledgeSource) == "web_search" || r.ChunkType == string(types.ChunkTypeWebSearch)
	if isWeb {
		url := ""
		if r.Metadata != nil {
			url = r.Metadata["url"]
		}
		title := r.KnowledgeTitle
		if title == "" && r.Metadata != nil {
			title = r.Metadata["title"]
		}
		if url != "" {
			return fmt.Sprintf(` source_type="web_search" source_title="%s" source_url="%s"`,
				escapeXMLAttr(title), escapeXMLAttr(url))
		}
		return ` source_type="web_search"`
	}

	// Knowledge base result — base attributes.
	docName := r.KnowledgeTitle
	if docName == "" {
		docName = r.KnowledgeFilename
	}
	var base string
	if docName != "" {
		base = fmt.Sprintf(` source_type="knowledge_base" source_doc="%s"`, escapeXMLAttr(docName))
	} else {
		base = ` source_type="knowledge_base"`
	}

	// Append entity-mismatch signal when the tagger has flagged this chunk.
	// The LLM sees entity_owner="Milesight" entity_mismatch="true" and can apply
	// the Generation Task Guard (Rule 7) to avoid silently copying competitor specs.
	if r.Metadata != nil && r.Metadata["entity_mismatch"] == "true" {
		owner := r.Metadata["entity_owner"]
		if owner != "" {
			base += fmt.Sprintf(` entity_owner="%s" entity_mismatch="true"`, escapeXMLAttr(owner))
		} else {
			base += ` entity_mismatch="true"`
		}
	}
	// Append doc_class when the chat-pipeline classifier has stamped it. The
	// LLM applies Rule 8 (doc-class trust hierarchy) to weigh product chunks
	// against strategy / competitive / research / training / solution chunks.
	if r.Metadata != nil {
		if cls := r.Metadata["doc_class"]; cls != "" {
			base += fmt.Sprintf(` doc_class="%s"`, escapeXMLAttr(cls))
		}
	}
	return base
}

// escapeXMLAttr escapes characters that must not appear raw inside an XML attribute value.
func escapeXMLAttr(s string) string {
	s = strings.ReplaceAll(s, `&`, `&amp;`)
	s = strings.ReplaceAll(s, `"`, `&quot;`)
	s = strings.ReplaceAll(s, `<`, `&lt;`)
	s = strings.ReplaceAll(s, `>`, `&gt;`)
	return s
}

// getEnrichedPassageForChat 合并Content和ImageInfo的文本内容，为聊天消息准备
func getEnrichedPassageForChat(ctx context.Context, result *types.SearchResult) string {
	// 如果没有图片信息，直接返回内容
	if result.Content == "" && result.ImageInfo == "" {
		return ""
	}

	// 如果只有内容，没有图片信息
	if result.ImageInfo == "" {
		return result.Content
	}

	// 处理图片信息并与内容合并
	return enrichContentWithImageInfo(ctx, result.Content, result.ImageInfo)
}

// enrichContentWithImageInfo delegates to the shared searchutil implementation.
func enrichContentWithImageInfo(_ context.Context, content string, imageInfoJSON string) string {
	return searchutil.EnrichContentWithImageInfo(content, imageInfoJSON)
}
