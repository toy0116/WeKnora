package chat

import (
	"context"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/provider"
	modelutils "github.com/Tencent/WeKnora/internal/models/utils"
	"github.com/google/uuid"
	"github.com/sashabaranov/go-openai"
)

// ProviderSpec describes provider-specific behavior for chat completions.
// Each spec is registered with a ProviderName and optionally a model matcher.
type ProviderSpec struct {
	Provider provider.ProviderName
	// ModelMatcher: if non-nil, this spec only applies when the model name matches.
	// Used for sub-provider routing (e.g. Qwen3 within Aliyun).
	ModelMatcher func(modelName string) bool
	// RequestCustomizer: provider-specific request modification.
	RequestCustomizer func(req *openai.ChatCompletionRequest, opts *ChatOptions, isStream bool) (any, bool)
	// EndpointCustomizer: provider-specific endpoint URL override.
	EndpointCustomizer func(baseURL string, modelID string, isStream bool) string
	// HeaderCustomizer: provider-specific raw HTTP header customization.
	HeaderCustomizer func(chat *RemoteAPIChat, req *http.Request, body []byte) error
}

// chatProviderSpecs is the ordered list of provider specs.
// Order matters: more specific specs (with ModelMatcher) should come before generic ones.
var chatProviderSpecs = []ProviderSpec{
	// WeKnoraCloud
	{
		Provider:          provider.ProviderWeKnoraCloud,
		RequestCustomizer: weKnoraCloudRequestCustomizer,
		EndpointCustomizer: func(baseURL string, _ string, _ bool) string {
			return strings.TrimRight(baseURL, "/") + "/api/v1/chat/completions"
		},
		HeaderCustomizer: weKnoraCloudHeaderCustomizer,
	},
	// Aliyun Qwen Thinking Models (must be before generic Aliyun)
	{
		Provider:          provider.ProviderAliyun,
		ModelMatcher:      func(name string) bool { return provider.IsQwenThinkingModel(name) },
		RequestCustomizer: qwenThinkingRequestCustomizer,
	},
	// LKEAP
	{
		Provider:          provider.ProviderLKEAP,
		RequestCustomizer: lkeapRequestCustomizer,
	},
	// DeepSeek
	{
		Provider:          provider.ProviderDeepSeek,
		RequestCustomizer: deepseekRequestCustomizer,
	},
	// Generic (vLLM)
	{
		Provider:          provider.ProviderGeneric,
		RequestCustomizer: genericRequestCustomizer,
	},
	// Volcengine (火山引擎 Ark)
	{
		Provider:          provider.ProviderVolcengine,
		RequestCustomizer: volcengineRequestCustomizer,
	},
	// NVIDIA
	{
		Provider:          provider.ProviderNvidia,
		RequestCustomizer: genericRequestCustomizer,
	},
	// Note: MiMo (小米) doesn't need a per-request customizer — its
	// reasoning_content round-trip is handled by ConvertMessages, and its
	// legacy-session compatibility is handled by sanitizeMimoMessages
	// (called from BuildChatCompletionRequest). See chat_provider_spec.go
	// "MiMo compatibility helpers" section below for context.
	// MiniMax (M2.x family) · force reasoning_split=true so thinking
	// flows through the standard reasoning_content field instead of
	// being embedded in content as <think>...</think> tags. WeKnora's
	// existing reasoning_content parser then displays it separately
	// (same path as MiMo / DeepSeek). Without this, the raw <think>
	// blocks leak into the user-visible answer.
	{
		Provider:          provider.ProviderMiniMax,
		RequestCustomizer: minimaxRequestCustomizer,
	},
}

// findProviderSpec finds the matching spec for the given provider and model name.
func findProviderSpec(providerName provider.ProviderName, modelName string) *ProviderSpec {
	for i := range chatProviderSpecs {
		spec := &chatProviderSpecs[i]
		if spec.Provider != providerName {
			continue
		}
		if spec.ModelMatcher != nil && !spec.ModelMatcher(modelName) {
			continue
		}
		return spec
	}
	return nil
}

// --- Type definitions (moved from wrapper files) ---

// QwenChatCompletionRequest Qwen 模型的自定义请求结构体
type QwenChatCompletionRequest struct {
	openai.ChatCompletionRequest
	EnableThinking *bool `json:"enable_thinking,omitempty"`
}

// ThinkingConfig 思维链配置（LKEAP / Volcengine 等通用格式）
type ThinkingConfig struct {
	Type string `json:"type"` // "enabled" 或 "disabled"
}

// ThinkingChatCompletionRequest 带 thinking 字段的自定义请求结构体
// 适用于 LKEAP、Volcengine 等使用 { "thinking": { "type": "enabled" } } 格式的 provider
type ThinkingChatCompletionRequest struct {
	openai.ChatCompletionRequest
	Thinking *ThinkingConfig `json:"thinking,omitempty"`
}

// MiniMaxChatCompletionRequest MiniMax M2.x 系列的自定义请求结构体
// reasoning_split=true 让 thinking 输出到独立的 reasoning_content 字段，
// 避免默认行为下思考内容被 <think>...</think> 标签嵌入 content 流。
type MiniMaxChatCompletionRequest struct {
	openai.ChatCompletionRequest
	ReasoningSplit bool `json:"reasoning_split"`
}

// --- Customizer functions ---

// weKnoraCloudRequestCustomizer 构造 WeKnoraCloud 请求。
// WeKnoraCloud 走 OpenAI 兼容格式，除了 MultiContent 需要降级为纯文本 Content 之外，
// 其他字段（tools / tool_choice / parallel_tool_calls / response_format / stream_options 等）直接透传，
// 以保证 function calling 等能力可用。
func weKnoraCloudRequestCustomizer(req *openai.ChatCompletionRequest, _ *ChatOptions, isStream bool) (any, bool) {
	cloudReq := *req
	cloudReq.Stream = isStream
	cloudReq.Messages = convertToWeKnoraCloudMessagesFromOpenAI(req.Messages)
	return cloudReq, true
}

func weKnoraCloudHeaderCustomizer(chat *RemoteAPIChat, req *http.Request, body []byte) error {
	requestID := uuid.NewString()
	headers := modelutils.Sign(chat.appID, chat.appSecret, requestID, string(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return nil
}

// convertToWeKnoraCloudMessagesFromOpenAI 将 MultiContent 降级为纯文本，
// 其它字段（tool_calls / tool_call_id / name 等）完全保留，保证 tool 协议正常。
func convertToWeKnoraCloudMessagesFromOpenAI(messages []openai.ChatCompletionMessage) []openai.ChatCompletionMessage {
	result := make([]openai.ChatCompletionMessage, 0, len(messages))
	for _, m := range messages {
		msg := m
		if msg.Content == "" && len(msg.MultiContent) > 0 {
			var textParts []string
			for _, part := range msg.MultiContent {
				if part.Type == openai.ChatMessagePartTypeText && part.Text != "" {
					textParts = append(textParts, part.Text)
				}
			}
			msg.Content = strings.Join(textParts, "\n")
			msg.MultiContent = nil
		}
		result = append(result, msg)
	}
	return result
}

// qwenThinkingRequestCustomizer 自定义 Qwen 系列（阿里云）模型的思考请求
func qwenThinkingRequestCustomizer(
	req *openai.ChatCompletionRequest, opts *ChatOptions, isStream bool,
) (any, bool) {
	if !isStream {
		// Qwen3 模型在非流式请求时需要显式禁用 thinking
		qwenReq := QwenChatCompletionRequest{
			ChatCompletionRequest: *req,
		}
		enableThinking := false
		qwenReq.EnableThinking = &enableThinking
		return qwenReq, true
	}

	// 流式请求：根据 opts.Thinking 启用思考
	qwenReq := QwenChatCompletionRequest{
		ChatCompletionRequest: *req,
	}
	thinking := false
	if opts != nil && opts.Thinking != nil {
		thinking = *opts.Thinking
	}
	qwenReq.EnableThinking = &thinking

	// 必须返回 true 以使用 raw HTTP，否则 SDK 会过滤掉 enable_thinking 字段
	return qwenReq, true
}

// lkeapRequestCustomizer 自定义 LKEAP 请求
// 仅对 DeepSeek V3.x 系列模型设置 thinking 参数；R1 系列默认开启思维链
// 参考：https://cloud.tencent.com/document/product/1772/115963
func lkeapRequestCustomizer(
	req *openai.ChatCompletionRequest, opts *ChatOptions, _ bool,
) (any, bool) {
	modelName := req.Model
	if !strings.Contains(strings.ToLower(modelName), "deepseek-v3") || opts == nil || opts.Thinking == nil {
		return nil, false
	}

	lkeapReq := ThinkingChatCompletionRequest{
		ChatCompletionRequest: *req,
	}

	thinkingType := "disabled"
	if *opts.Thinking {
		thinkingType = "enabled"
	}
	lkeapReq.Thinking = &ThinkingConfig{Type: thinkingType}

	return lkeapReq, true
}

// deepseekRequestCustomizer 自定义 DeepSeek 请求
// DeepSeek 模型不支持 tool_choice 参数，需要清除
func deepseekRequestCustomizer(
	req *openai.ChatCompletionRequest, opts *ChatOptions, _ bool,
) (any, bool) {
	if opts != nil && opts.ToolChoice != "" {
		logger.Infof(context.Background(), "deepseek model, skip tool_choice")
		req.ToolChoice = nil
	}
	return nil, false
}

// genericRequestCustomizer 自定义 Generic 请求
// Generic provider（如 vLLM）使用 ChatTemplateKwargs 传递 thinking 参数
func genericRequestCustomizer(
	req *openai.ChatCompletionRequest, opts *ChatOptions, _ bool,
) (any, bool) {
	thinking := false
	if opts != nil && opts.Thinking != nil {
		thinking = *opts.Thinking
	}
	req.ChatTemplateKwargs = map[string]interface{}{
		"enable_thinking": thinking,
	}
	return req, true
}

// minimaxRequestCustomizer 自定义 MiniMax 请求
// MiniMax M2.x 系列默认会进行思考，但若不显式开启 reasoning_split，思考内容会以
// <think>...</think> 标签嵌入到 content 中（streaming chunks 也是这样），导致用户在
// WeKnora UI 上看到的回答前半段全是模型内心独白。
// 我们对 MiniMax-M* 前缀的模型一律开启 reasoning_split=true，让思考走标准的
// reasoning_content 字段，与 MiMo / DeepSeek 行为一致 · WeKnora 的 ChatStream /
// processRawHTTPStream 自带 reasoning_content 解析逻辑，前端能将它单独折叠展示。
// 对非 M 系列（如 MiniMax-Text-01 等历史模型）跳过，避免触发 400。
func minimaxRequestCustomizer(
	req *openai.ChatCompletionRequest, _ *ChatOptions, _ bool,
) (any, bool) {
	if !strings.HasPrefix(req.Model, "MiniMax-M") {
		return nil, false
	}
	return MiniMaxChatCompletionRequest{
		ChatCompletionRequest: *req,
		ReasoningSplit:        true,
	}, true
}

// volcengineRequestCustomizer 自定义火山引擎请求
// 火山引擎使用 thinking 参数控制深度思考，格式同 LKEAP: { "type": "enabled"/"disabled" }
func volcengineRequestCustomizer(
	req *openai.ChatCompletionRequest, opts *ChatOptions, _ bool,
) (any, bool) {
	if opts == nil || opts.Thinking == nil {
		return nil, false
	}

	vcReq := ThinkingChatCompletionRequest{
		ChatCompletionRequest: *req,
	}

	thinkingType := "disabled"
	if *opts.Thinking {
		thinkingType = "enabled"
	}
	vcReq.Thinking = &ThinkingConfig{Type: thinkingType}

	return vcReq, true
}

// --- MiMo (小米) compatibility helpers ---
//
// MiMo's thinking-mode multi-turn API requires that every assistant message
// carrying tool_calls also includes its reasoning_content; otherwise the API
// rejects the request with HTTP 400. The forward path is handled by
// remote_api.go.ConvertMessages, which forwards msg.ReasoningContent into
// the standard openai.ChatCompletionMessage.ReasoningContent field
// (go-openai v1.40+ supports it natively, so we don't need a MiMo-specific
// request struct or raw-HTTP customizer anymore).
//
// The legacy-session sanitizer below stays — it drops orphan
// assistant→tool_call groups where the assistant message lacks
// reasoning_content (sessions started before 73cbfc35 have no
// reasoning_content stored on disk). Without this, those sessions would
// keep 400-ing until the user manually started a new conversation.
//
// Reference: https://platform.xiaomimimo.com/docs/zh-CN/usage-guide/passing-back-reasoning_content

// sanitizeMimoMessages drops assistant→tool message groups where the
// assistant message contains tool_calls but no ReasoningContent. Called by
// remote_api.go.BuildChatCompletionRequest immediately before ConvertMessages
// when the provider is MiMo. No-op on the fast path when no offending
// messages exist.
func sanitizeMimoMessages(msgs []Message) []Message {
	// Collect tool-call IDs from problematic assistant messages.
	badCallIDs := map[string]bool{}
	skipIdx := map[int]bool{}

	for i, m := range msgs {
		if m.Role == "assistant" && len(m.ToolCalls) > 0 && m.ReasoningContent == "" {
			skipIdx[i] = true
			for _, tc := range m.ToolCalls {
				badCallIDs[tc.ID] = true
			}
		}
	}

	// Also drop the tool-result messages whose call ID was marked bad.
	for i, m := range msgs {
		if m.Role == "tool" && badCallIDs[m.ToolCallID] {
			skipIdx[i] = true
		}
	}

	if len(skipIdx) == 0 {
		return msgs // fast path: nothing to remove
	}

	result := make([]Message, 0, len(msgs)-len(skipIdx))
	for i, m := range msgs {
		if !skipIdx[i] {
			result = append(result, m)
		}
	}
	return result
}
