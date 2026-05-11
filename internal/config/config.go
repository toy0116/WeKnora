package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

// Config 应用程序总配置
type Config struct {
	Conversation    *ConversationConfig    `yaml:"conversation"     json:"conversation"`
	Server          *ServerConfig          `yaml:"server"           json:"server"`
	KnowledgeBase   *KnowledgeBaseConfig   `yaml:"knowledge_base"   json:"knowledge_base"`
	Tenant          *TenantConfig          `yaml:"tenant"           json:"tenant"`
	OIDCAuth        *OIDCAuthConfig        `yaml:"oidc_auth"        json:"oidc_auth"`
	Models          []ModelConfig          `yaml:"models"           json:"models"`
	VectorDatabase  *VectorDatabaseConfig  `yaml:"vector_database"  json:"vector_database"`
	DocReader       *DocReaderConfig       `yaml:"docreader"        json:"docreader"`
	StreamManager   *StreamManagerConfig   `yaml:"stream_manager"   json:"stream_manager"`
	ExtractManager  *ExtractManagerConfig  `yaml:"extract"          json:"extract"`
	WebSearch       *WebSearchConfig       `yaml:"web_search"       json:"web_search"`
	PromptTemplates *PromptTemplatesConfig `yaml:"prompt_templates" json:"prompt_templates"`
	IM              *IMConfig              `yaml:"im"               json:"im"`
	Agent           *AgentConfig           `yaml:"agent"            json:"agent"`
	EntityAliases   *EntityAliasConfig     `yaml:"-"                json:"entity_aliases,omitempty"`
	AliasDenylist   *AliasDenylistConfig   `yaml:"-"                json:"alias_denylist,omitempty"`
	DocClasses      *DocClassConfig        `yaml:"-"                json:"doc_classes,omitempty"`
	// ConfigDir is the directory that contains config.yaml; set at load time.
	// Used by handlers that need to read/write sibling config files at runtime.
	ConfigDir string `yaml:"-" json:"-"`
}

// AgentConfig represents the global agent settings.
type AgentConfig struct {
	// LLMCallTimeout is the default timeout for a single LLM call in seconds.
	// Default: 120 (standard agents) or 300 (can be overridden by Env).
	LLMCallTimeout int `yaml:"llm_call_timeout" json:"llm_call_timeout"`
}

// IMConfig configures the IM integration service.
// All fields are optional — zero values fall back to built-in defaults so
// existing deployments need no config changes.
type IMConfig struct {
	// Workers is the number of concurrent QA worker goroutines per instance.
	// Default: 5.
	Workers int `yaml:"workers" json:"workers"`
	// GlobalMaxWorkers is the maximum number of QA requests that can execute
	// concurrently across ALL instances. Enforced via a Redis counter; when the
	// global limit is reached, local workers wait until a slot opens.
	// Requires Redis — ignored in single-instance mode.
	// 0 (default) means no global limit.
	GlobalMaxWorkers int `yaml:"global_max_workers" json:"global_max_workers"`
	// MaxQueueSize is the maximum number of pending QA requests per instance.
	// Default: 50.
	MaxQueueSize int `yaml:"max_queue_size" json:"max_queue_size"`
	// MaxPerUser limits how many requests a single user can have queued globally.
	// Default: 3.
	MaxPerUser int `yaml:"max_per_user" json:"max_per_user"`
	// RateLimitWindow is the sliding window duration for per-user rate limiting.
	// Default: 60s.
	RateLimitWindow time.Duration `yaml:"rate_limit_window" json:"rate_limit_window"`
	// RateLimitMax is the maximum number of requests allowed per window per user.
	// Default: 10.
	RateLimitMax int `yaml:"rate_limit_max" json:"rate_limit_max"`
}

// DocReaderConfig configures the document parser client (gRPC or HTTP).
type DocReaderConfig struct {
	// Addr: for gRPC it is the server address (e.g. "localhost:50051"); for HTTP it is the base URL (e.g. "http://localhost:8080").
	Addr string `yaml:"addr" json:"addr"`
	// Transport: "grpc" (default) or "http"
	Transport string `yaml:"transport" json:"transport"`
}

type VectorDatabaseConfig struct {
	Driver string `yaml:"driver" json:"driver"`
}

// ConversationConfig 对话服务配置
type ConversationConfig struct {
	MaxRounds            int            `yaml:"max_rounds"                       json:"max_rounds"`
	KeywordThreshold     float64        `yaml:"keyword_threshold"                json:"keyword_threshold"`
	EmbeddingTopK        int            `yaml:"embedding_top_k"                  json:"embedding_top_k"`
	VectorThreshold      float64        `yaml:"vector_threshold"                 json:"vector_threshold"`
	RerankTopK           int            `yaml:"rerank_top_k"                     json:"rerank_top_k"`
	RerankThreshold      float64        `yaml:"rerank_threshold"                 json:"rerank_threshold"`
	FallbackStrategy     string         `yaml:"fallback_strategy"                json:"fallback_strategy"`
	FallbackResponse     string         `yaml:"fallback_response"                json:"fallback_response"`
	EnableRewrite        bool           `yaml:"enable_rewrite"                   json:"enable_rewrite"`
	EnableQueryExpansion bool           `yaml:"enable_query_expansion"           json:"enable_query_expansion"`
	EnableRerank         bool           `yaml:"enable_rerank"                    json:"enable_rerank"`
	Summary              *SummaryConfig `yaml:"summary"                          json:"summary"`

	// Prompt template ID fields — resolved to text by backfillConversationDefaults
	FallbackPromptID             string `yaml:"fallback_prompt_id"                json:"fallback_prompt_id"`
	RewritePromptID              string `yaml:"rewrite_prompt_id"                 json:"rewrite_prompt_id"`
	GenerateSessionTitlePromptID string `yaml:"generate_session_title_prompt_id"  json:"generate_session_title_prompt_id"`
	GenerateSummaryPromptID      string `yaml:"generate_summary_prompt_id"        json:"generate_summary_prompt_id"`
	ExtractEntitiesPromptID      string `yaml:"extract_entities_prompt_id"        json:"extract_entities_prompt_id"`
	ExtractRelationshipsPromptID string `yaml:"extract_relationships_prompt_id"   json:"extract_relationships_prompt_id"`
	GenerateQuestionsPromptID    string `yaml:"generate_questions_prompt_id"      json:"generate_questions_prompt_id"`

	// Resolved prompt text fields (populated by backfill, not from YAML)
	FallbackPrompt             string `yaml:"-" json:"fallback_prompt"`
	RewritePromptSystem        string `yaml:"-" json:"rewrite_prompt_system"`
	RewritePromptUser          string `yaml:"-" json:"rewrite_prompt_user"`
	GenerateSessionTitlePrompt string `yaml:"-" json:"generate_session_title_prompt"`
	GenerateSummaryPrompt      string `yaml:"-" json:"generate_summary_prompt"`
	ExtractEntitiesPrompt      string `yaml:"-" json:"extract_entities_prompt"`
	ExtractRelationshipsPrompt string `yaml:"-" json:"extract_relationships_prompt"`
	GenerateQuestionsPrompt    string `yaml:"-" json:"generate_questions_prompt"`

	// IntentSystemPrompts maps intent values (e.g. "greeting", "chitchat") to
	// system prompt text. Populated by backfill from IntentPrompts templates.
	IntentSystemPrompts map[string]string `yaml:"-" json:"-"`
}

// SummaryConfig 摘要配置
type SummaryConfig struct {
	MaxInputChars       int     `yaml:"max_input_chars"       json:"max_input_chars"` // Max input characters for summary generation (default: 16384)
	MaxTokens           int     `yaml:"max_tokens"            json:"max_tokens"`
	RepeatPenalty       float64 `yaml:"repeat_penalty"        json:"repeat_penalty"`
	TopK                int     `yaml:"top_k"                 json:"top_k"`
	TopP                float64 `yaml:"top_p"                 json:"top_p"`
	FrequencyPenalty    float64 `yaml:"frequency_penalty"     json:"frequency_penalty"`
	PresencePenalty     float64 `yaml:"presence_penalty"      json:"presence_penalty"`
	Temperature         float64 `yaml:"temperature"           json:"temperature"`
	Seed                int     `yaml:"seed"                  json:"seed"`
	MaxCompletionTokens int     `yaml:"max_completion_tokens" json:"max_completion_tokens"`
	NoMatchPrefix       string  `yaml:"no_match_prefix"       json:"no_match_prefix"`
	Thinking            *bool   `yaml:"thinking"              json:"thinking"`

	// Prompt template ID fields — resolved to text by backfillConversationDefaults
	PromptID          string `yaml:"prompt_id"           json:"prompt_id"`
	ContextTemplateID string `yaml:"context_template_id" json:"context_template_id"`

	// Resolved prompt text fields (populated by backfill, not from YAML)
	Prompt          string `yaml:"-" json:"prompt"`
	ContextTemplate string `yaml:"-" json:"context_template"`
}

// ServerConfig 服务器配置
type ServerConfig struct {
	Port            int           `yaml:"port"             json:"port"`
	Host            string        `yaml:"host"             json:"host"`
	LogPath         string        `yaml:"log_path"         json:"log_path"`
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout" json:"shutdown_timeout" default:"30s"`
}

// KnowledgeBaseConfig 知识库配置
type KnowledgeBaseConfig struct {
	ChunkSize       int                    `yaml:"chunk_size"       json:"chunk_size"`
	ChunkOverlap    int                    `yaml:"chunk_overlap"    json:"chunk_overlap"`
	SplitMarkers    []string               `yaml:"split_markers"    json:"split_markers"`
	KeepSeparator   bool                   `yaml:"keep_separator"   json:"keep_separator"`
	ImageProcessing *ImageProcessingConfig `yaml:"image_processing" json:"image_processing"`
}

// ImageProcessingConfig 图像处理配置
type ImageProcessingConfig struct {
	EnableMultimodal bool `yaml:"enable_multimodal" json:"enable_multimodal"`
}

// TenantConfig 租户配置
type TenantConfig struct {
	DefaultSessionName        string `yaml:"default_session_name"        json:"default_session_name"`
	DefaultSessionTitle       string `yaml:"default_session_title"       json:"default_session_title"`
	DefaultSessionDescription string `yaml:"default_session_description" json:"default_session_description"`
	// EnableCrossTenantAccess enables cross-tenant access for users with permission
	EnableCrossTenantAccess bool `yaml:"enable_cross_tenant_access" json:"enable_cross_tenant_access"`
}

type OIDCUserInfoMapping struct {
	Username string `yaml:"username" json:"username"`
	Email    string `yaml:"email"    json:"email"`
}

type OIDCAuthConfig struct {
	Enable                bool                 `yaml:"enable"                 json:"enable"`
	IssuerURL             string               `yaml:"issuer_url"             json:"issuer_url"`
	DiscoveryURL          string               `yaml:"discovery_url"          json:"discovery_url"`
	ProviderDisplayName   string               `yaml:"provider_display_name"  json:"provider_display_name"`
	ClientID              string               `yaml:"client_id"              json:"client_id"`
	ClientSecret          string               `yaml:"client_secret"          json:"-"`
	AuthorizationEndpoint string               `yaml:"authorization_endpoint" json:"authorization_endpoint"`
	TokenEndpoint         string               `yaml:"token_endpoint"         json:"token_endpoint"`
	UserInfoEndpoint      string               `yaml:"user_info_endpoint"     json:"user_info_endpoint"`
	Scopes                []string             `yaml:"scopes"                 json:"scopes"`
	UserInfoMapping       *OIDCUserInfoMapping `yaml:"user_info_mapping"      json:"user_info_mapping"`
}

// PromptTemplateI18n holds localized name and description for a prompt template.
type PromptTemplateI18n struct {
	Name        string `yaml:"name"        json:"name"`
	Description string `yaml:"description" json:"description"`
}

// PromptTemplate 提示词模板
//
// 字段设计：每个模板最多由两部分组成 —— 系统侧 (content) 和用户侧 (user)。
//   - content: 主要内容 / 系统 Prompt（所有模板都使用此字段）
//   - user:    用户侧 Prompt（仅在需要 system+user 配对的模板中使用，如 rewrite、keywords_extraction）
//   - i18n:    多语言 name/description，键为 locale（如 "zh-CN"、"en-US"、"ko-KR"），后端根据请求语言替换 Name/Description 再返回
type PromptTemplate struct {
	ID               string                        `yaml:"id"                 json:"id"`
	Name             string                        `yaml:"name"               json:"name"`
	Description      string                        `yaml:"description"        json:"description"`
	Content          string                        `yaml:"content"            json:"content"`
	User             string                        `yaml:"user"               json:"user,omitempty"`
	HasKnowledgeBase bool                          `yaml:"has_knowledge_base" json:"has_knowledge_base,omitempty"`
	HasWebSearch     bool                          `yaml:"has_web_search"     json:"has_web_search,omitempty"`
	Default          bool                          `yaml:"default"            json:"default,omitempty"`
	Mode             string                        `yaml:"mode"               json:"mode,omitempty"`
	I18n             map[string]PromptTemplateI18n `yaml:"i18n"               json:"-"`
}

// PromptTemplatesConfig 提示词模板配置
//
// 每种 Prompt 类型对应一个 YAML 文件，所有模板都在同一个字段（文件）中管理。
// 每个模板使用 content (system prompt) + user (user prompt) 两个字段。
type PromptTemplatesConfig struct {
	SystemPrompt    []PromptTemplate `yaml:"system_prompt"    json:"system_prompt"`
	ContextTemplate []PromptTemplate `yaml:"context_template" json:"context_template"`
	// Rewrite 合并了前端可选模板和运行时默认模板，每个模板同时包含 content + user
	Rewrite []PromptTemplate `yaml:"rewrite" json:"rewrite"`
	// Fallback 合并了固定回复模板和模型兜底 prompt（通过 mode:"model" 区分）
	Fallback []PromptTemplate `yaml:"fallback" json:"fallback"`

	GenerateSessionTitle []PromptTemplate `yaml:"generate_session_title" json:"generate_session_title,omitempty"`
	GenerateSummary      []PromptTemplate `yaml:"generate_summary"       json:"generate_summary,omitempty"`
	KeywordsExtraction   []PromptTemplate `yaml:"keywords_extraction"    json:"keywords_extraction,omitempty"`
	AgentSystemPrompt    []PromptTemplate `yaml:"agent_system_prompt"    json:"agent_system_prompt,omitempty"`
	GraphExtraction      []PromptTemplate `yaml:"graph_extraction"       json:"graph_extraction,omitempty"`
	GenerateQuestions    []PromptTemplate `yaml:"generate_questions"     json:"generate_questions,omitempty"`
	// IntentPrompts holds per-intent system prompt overrides (template ID = intent value).
	IntentPrompts []PromptTemplate `yaml:"intent_prompts" json:"intent_prompts,omitempty"`
}

// DefaultTemplate returns the first template marked as default in the list,
// or the first template if none is marked, or nil if the list is empty.
func DefaultTemplate(templates []PromptTemplate) *PromptTemplate {
	for i := range templates {
		if templates[i].Default {
			return &templates[i]
		}
	}
	if len(templates) > 0 {
		return &templates[0]
	}
	return nil
}

// DefaultTemplateByMode returns the default template filtered by mode.
func DefaultTemplateByMode(templates []PromptTemplate, mode string) *PromptTemplate {
	for i := range templates {
		if templates[i].Mode == mode && templates[i].Default {
			return &templates[i]
		}
	}
	for i := range templates {
		if templates[i].Mode == mode {
			return &templates[i]
		}
	}
	return DefaultTemplate(templates)
}

// LocalizeTemplates returns a deep copy of the template list with Name and
// Description replaced according to the given locale.  Fallback chain:
//   locale → primary language (e.g. "zh" from "zh-CN") → original Name/Description.
// The returned slice is safe to serialise directly; it never mutates the original.
func LocalizeTemplates(templates []PromptTemplate, locale string) []PromptTemplate {
	if len(templates) == 0 {
		return templates
	}
	out := make([]PromptTemplate, len(templates))
	copy(out, templates)
	for i := range out {
		if len(out[i].I18n) == 0 {
			continue
		}
		// Try exact match first (e.g. "zh-CN"), then primary subtag (e.g. "zh")
		l10n, ok := out[i].I18n[locale]
		if !ok {
			if idx := strings.IndexByte(locale, '-'); idx > 0 {
				l10n, ok = out[i].I18n[locale[:idx]]
			}
		}
		if !ok {
			continue
		}
		if l10n.Name != "" {
			out[i].Name = l10n.Name
		}
		if l10n.Description != "" {
			out[i].Description = l10n.Description
		}
	}
	return out
}

// ModelConfig 模型配置
type ModelConfig struct {
	Type       string                 `yaml:"type"       json:"type"`
	Source     string                 `yaml:"source"     json:"source"`
	ModelName  string                 `yaml:"model_name" json:"model_name"`
	Parameters map[string]interface{} `yaml:"parameters" json:"parameters"`
}

// StreamManagerConfig 流管理器配置
type StreamManagerConfig struct {
	Type           string        `yaml:"type"            json:"type"`            // 类型: "memory" 或 "redis"
	Redis          RedisConfig   `yaml:"redis"           json:"redis"`           // Redis配置
	CleanupTimeout time.Duration `yaml:"cleanup_timeout" json:"cleanup_timeout"` // 清理超时，单位秒
}

// RedisConfig Redis配置
type RedisConfig struct {
	Address  string        `yaml:"address"  json:"address"`  // Redis地址
	Username string        `yaml:"username" json:"username"` // Redis用户名
	Password string        `yaml:"password" json:"password"` // Redis密码
	DB       int           `yaml:"db"       json:"db"`       // Redis数据库
	Prefix   string        `yaml:"prefix"   json:"prefix"`   // 键前缀
	TTL      time.Duration `yaml:"ttl"      json:"ttl"`      // 过期时间(小时)
}

// ExtractManagerConfig 抽取管理器配置
type ExtractManagerConfig struct {
	ExtractGraph  *types.PromptTemplateStructured `yaml:"extract_graph"  json:"extract_graph"`
	ExtractEntity *types.PromptTemplateStructured `yaml:"extract_entity" json:"extract_entity"`
	FabriText     *FebriText                      `yaml:"fabri_text"     json:"fabri_text"`
}

type FebriText struct {
	WithTag   string `yaml:"with_tag"    json:"with_tag"`
	WithNoTag string `yaml:"with_no_tag" json:"with_no_tag"`
}

// LoadConfig 从配置文件加载配置
func LoadConfig() (*Config, error) {
	// 设置配置文件名和路径
	viper.SetConfigName("config")         // 配置文件名称(不带扩展名)
	viper.SetConfigType("yaml")           // 配置文件类型
	viper.AddConfigPath(".")              // 当前目录
	viper.AddConfigPath("./config")       // config子目录
	viper.AddConfigPath("$HOME/.appname") // 用户目录
	viper.AddConfigPath("/etc/appname/")  // etc目录

	// 启用环境变量替换
	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	// 读取配置文件
	if err := viper.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("error reading config file: %w", err)
	}

	// 替换配置中的环境变量引用
	configFileContent, err := os.ReadFile(viper.ConfigFileUsed())
	if err != nil {
		return nil, fmt.Errorf("error reading config file content: %w", err)
	}

	// 替换${ENV_VAR}格式的环境变量引用
	re := regexp.MustCompile(`\${([^}]+)}`)
	result := re.ReplaceAllStringFunc(string(configFileContent), func(match string) string {
		// 提取环境变量名称（去掉${}部分）
		envVar := match[2 : len(match)-1]
		// 获取环境变量值，如果不存在则保持原样
		if value := os.Getenv(envVar); value != "" {
			return value
		}
		return match
	})

	// 使用处理后的配置内容
	viper.ReadConfig(strings.NewReader(result))

	// 解析配置到结构体
	var cfg Config
	if err := viper.Unmarshal(&cfg, func(dc *mapstructure.DecoderConfig) {
		dc.TagName = "yaml"
	}); err != nil {
		return nil, fmt.Errorf("unable to decode config into struct: %w", err)
	}
	fmt.Printf("Using configuration file: %s\n", viper.ConfigFileUsed())

	configDir := filepath.Dir(viper.ConfigFileUsed())
	cfg.ConfigDir = configDir

	// 加载提示词模板（从目录或配置文件）
	promptTemplates, err := loadPromptTemplates(configDir)
	if err != nil {
		fmt.Printf("Warning: failed to load prompt templates from directory: %v\n", err)
		// 如果目录加载失败，使用配置文件中的模板（如果有）
	} else if promptTemplates != nil {
		cfg.PromptTemplates = promptTemplates
	}

	// Back-fill conversation config from prompt templates defaults
	// (so config.yaml can omit large prompt blocks and rely on template files)
	if cfg.PromptTemplates != nil && cfg.Conversation != nil {
		backfillConversationDefaults(&cfg)
	}

	// Load entity alias dictionary from entity_aliases.yaml (optional)
	if aliases, err := loadEntityAliases(configDir); err != nil {
		fmt.Printf("Warning: failed to load entity aliases: %v\n", err)
	} else if aliases != nil {
		cfg.EntityAliases = aliases
	}

	// Load document-class registry from doc_classes.yaml (optional)
	if classes, err := loadDocClasses(configDir); err != nil {
		fmt.Printf("Warning: failed to load doc classes: %v\n", err)
	} else if classes != nil {
		cfg.DocClasses = classes
	}

	// Load alias-denylist from entity_alias_denylist.yaml (optional).
	// Empty file or absent file = no denylist; auto-discovery will not
	// filter any candidate slugs.
	if dl, err := loadAliasDenylist(configDir); err != nil {
		fmt.Printf("Warning: failed to load alias denylist: %v\n", err)
	} else if dl != nil {
		cfg.AliasDenylist = dl
	}

	// Load built-in agent definitions (i18n-aware) from builtin_agents.yaml
	if err := types.LoadBuiltinAgentsConfig(configDir); err != nil {
		fmt.Printf("Warning: failed to load builtin agents config: %v\n", err)
	}

	// Load smart-reasoning agent type presets (rag-qa / wiki-qa / hybrid / custom).
	if err := types.LoadAgentTypePresetsConfig(configDir); err != nil {
		fmt.Printf("Warning: failed to load agent type presets: %v\n", err)
	}

	// Resolve prompt template ID references in builtin agent configs
	// (e.g. system_prompt_id -> actual content from agent_system_prompt.yaml)
	if cfg.PromptTemplates != nil {
		resolveBuiltinAgentPromptIDs(cfg.PromptTemplates)
		// Validate that every preset references an existing prompt template.
		types.ResolveAgentTypePresetPromptRefs(func(id string) string {
			if t := FindTemplateByID(cfg.PromptTemplates, id); t != nil {
				return t.Content
			}
			return ""
		})
	}

	// Validate configuration values
	applyOIDCEnvOverrides(&cfg)
	applyAgentEnvOverrides(&cfg)

	if err := ValidateConfig(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// ValidateConfig performs basic validation of the loaded configuration.
// It checks for obviously invalid or missing values that would cause runtime failures.
func ValidateConfig(cfg *Config) error {
	var errs []string

	if cfg.OIDCAuth != nil && cfg.OIDCAuth.Enable {
		if strings.TrimSpace(cfg.OIDCAuth.ClientID) == "" {
			errs = append(errs, "oidc_auth.client_id is required when OIDC is enabled")
		}
		if strings.TrimSpace(cfg.OIDCAuth.ClientSecret) == "" {
			errs = append(errs, "oidc_auth.client_secret is required when OIDC is enabled")
		}
		if strings.TrimSpace(cfg.OIDCAuth.DiscoveryURL) == "" &&
			(strings.TrimSpace(cfg.OIDCAuth.AuthorizationEndpoint) == "" || strings.TrimSpace(cfg.OIDCAuth.TokenEndpoint) == "") {
			errs = append(errs, "oidc_auth.discovery_url or both oidc_auth.authorization_endpoint and oidc_auth.token_endpoint are required when OIDC is enabled")
		}
	}

	if cfg.Conversation != nil {
		if cfg.Conversation.EmbeddingTopK < 0 {
			errs = append(errs, "conversation.embedding_top_k must be >= 0")
		}
		if cfg.Conversation.RerankTopK < 0 {
			errs = append(errs, "conversation.rerank_top_k must be >= 0")
		}
		if cfg.Conversation.VectorThreshold < 0 || cfg.Conversation.VectorThreshold > 1 {
			errs = append(errs, "conversation.vector_threshold must be between 0 and 1")
		}
		if cfg.Conversation.RerankThreshold < -10 || cfg.Conversation.RerankThreshold > 10 {
			errs = append(errs, "conversation.rerank_threshold must be between -10 and 10")
		}
	}

	if cfg.KnowledgeBase != nil {
		if cfg.KnowledgeBase.ChunkSize <= 0 {
			errs = append(errs, "knowledge_base.chunk_size must be > 0")
		}
		if cfg.KnowledgeBase.ChunkOverlap < 0 {
			errs = append(errs, "knowledge_base.chunk_overlap must be >= 0")
		}
		if cfg.KnowledgeBase.ChunkOverlap >= cfg.KnowledgeBase.ChunkSize {
			errs = append(errs, "knowledge_base.chunk_overlap must be less than chunk_size")
		}
	}

	if cfg.Server != nil {
		if cfg.Server.Port <= 0 || cfg.Server.Port > 65535 {
			errs = append(errs, "server.port must be between 1 and 65535")
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("config validation errors: %s", strings.Join(errs, "; "))
	}
	return nil
}

func applyOIDCEnvOverrides(cfg *Config) {
	if cfg.OIDCAuth == nil {
		cfg.OIDCAuth = &OIDCAuthConfig{}
	}
	if cfg.OIDCAuth.UserInfoMapping == nil {
		cfg.OIDCAuth.UserInfoMapping = &OIDCUserInfoMapping{}
	}

	if value := strings.TrimSpace(os.Getenv("OIDC_AUTH_ENABLE")); value != "" {
		cfg.OIDCAuth.Enable = strings.EqualFold(value, "true")
	}
	if value := strings.TrimSpace(os.Getenv("OIDC_AUTH_ISSUER_URL")); value != "" {
		cfg.OIDCAuth.IssuerURL = value
	}
	if value := strings.TrimSpace(os.Getenv("OIDC_AUTH_DISCOVERY_URL")); value != "" {
		cfg.OIDCAuth.DiscoveryURL = value
	}
	if value := strings.TrimSpace(os.Getenv("OIDC_AUTH_PROVIDER_DISPLAY_NAME")); value != "" {
		cfg.OIDCAuth.ProviderDisplayName = value
	}
	if value := strings.TrimSpace(os.Getenv("OIDC_AUTH_CLIENT_ID")); value != "" {
		cfg.OIDCAuth.ClientID = value
	}
	if value := strings.TrimSpace(os.Getenv("OIDC_AUTH_CLIENT_SECRET")); value != "" {
		cfg.OIDCAuth.ClientSecret = value
	}
	if value := strings.TrimSpace(os.Getenv("OIDC_AUTH_AUTHORIZATION_ENDPOINT")); value != "" {
		cfg.OIDCAuth.AuthorizationEndpoint = value
	}
	if value := strings.TrimSpace(os.Getenv("OIDC_AUTH_TOKEN_ENDPOINT")); value != "" {
		cfg.OIDCAuth.TokenEndpoint = value
	}
	if value := strings.TrimSpace(os.Getenv("OIDC_AUTH_USER_INFO_ENDPOINT")); value != "" {
		cfg.OIDCAuth.UserInfoEndpoint = value
	}
	if value := strings.TrimSpace(os.Getenv("OIDC_AUTH_SCOPES")); value != "" {
		cfg.OIDCAuth.Scopes = strings.Fields(strings.ReplaceAll(value, ",", " "))
	}
	if value := strings.TrimSpace(os.Getenv("OIDC_USER_INFO_MAPPING_USER_NAME")); value != "" {
		cfg.OIDCAuth.UserInfoMapping.Username = value
	}
	if value := strings.TrimSpace(os.Getenv("OIDC_USER_INFO_MAPPING_EMAIL")); value != "" {
		cfg.OIDCAuth.UserInfoMapping.Email = value
	}

	if cfg.OIDCAuth.ProviderDisplayName == "" {
		cfg.OIDCAuth.ProviderDisplayName = "OIDC"
	}
	if len(cfg.OIDCAuth.Scopes) == 0 {
		cfg.OIDCAuth.Scopes = []string{"openid", "profile", "email"}
	}
	if cfg.OIDCAuth.UserInfoMapping.Username == "" {
		cfg.OIDCAuth.UserInfoMapping.Username = "name"
	}
	if cfg.OIDCAuth.UserInfoMapping.Email == "" {
		cfg.OIDCAuth.UserInfoMapping.Email = "email"
	}
	if cfg.OIDCAuth.DiscoveryURL == "" && cfg.OIDCAuth.IssuerURL != "" {
		cfg.OIDCAuth.DiscoveryURL = strings.TrimRight(cfg.OIDCAuth.IssuerURL, "/") + "/.well-known/openid-configuration"
	}
}

func applyAgentEnvOverrides(cfg *Config) {
	if cfg.Agent == nil {
		cfg.Agent = &AgentConfig{}
	}
	if value := strings.TrimSpace(os.Getenv("WEKNORA_AGENT_LLM_TIMEOUT")); value != "" {
		if timeout, err := time.ParseDuration(value); err == nil {
			cfg.Agent.LLMCallTimeout = int(timeout.Seconds())
		} else if sec, err := time.ParseDuration(value + "s"); err == nil {
			// Handle case where user just provides a number like "300"
			cfg.Agent.LLMCallTimeout = int(sec.Seconds())
		}
	}
}

// backfillConversationDefaults resolves prompt template ID references
// into actual prompt text content. Only xxx_id fields are used;
// no fallback to default templates.
func backfillConversationDefaults(cfg *Config) {
	pt := cfg.PromptTemplates
	conv := cfg.Conversation

	if conv.FallbackPromptID != "" {
		if t := FindTemplateByID(pt, conv.FallbackPromptID); t != nil {
			conv.FallbackPrompt = t.Content
		} else {
			fmt.Printf("Warning: fallback_prompt_id %q not found\n", conv.FallbackPromptID)
		}
	}
	if conv.RewritePromptID != "" {
		if t := FindTemplateByID(pt, conv.RewritePromptID); t != nil {
			conv.RewritePromptSystem = t.Content
			conv.RewritePromptUser = t.User
		} else {
			fmt.Printf("Warning: rewrite_prompt_id %q not found\n", conv.RewritePromptID)
		}
	}
	if conv.GenerateSessionTitlePromptID != "" {
		if t := FindTemplateByID(pt, conv.GenerateSessionTitlePromptID); t != nil {
			conv.GenerateSessionTitlePrompt = t.Content
		} else {
			fmt.Printf("Warning: generate_session_title_prompt_id %q not found\n", conv.GenerateSessionTitlePromptID)
		}
	}
	if conv.GenerateSummaryPromptID != "" {
		if t := FindTemplateByID(pt, conv.GenerateSummaryPromptID); t != nil {
			conv.GenerateSummaryPrompt = t.Content
		} else {
			fmt.Printf("Warning: generate_summary_prompt_id %q not found\n", conv.GenerateSummaryPromptID)
		}
	}
	if conv.ExtractEntitiesPromptID != "" {
		if t := FindTemplateByID(pt, conv.ExtractEntitiesPromptID); t != nil {
			conv.ExtractEntitiesPrompt = t.Content
		} else {
			fmt.Printf("Warning: extract_entities_prompt_id %q not found\n", conv.ExtractEntitiesPromptID)
		}
	}
	if conv.ExtractRelationshipsPromptID != "" {
		if t := FindTemplateByID(pt, conv.ExtractRelationshipsPromptID); t != nil {
			conv.ExtractRelationshipsPrompt = t.Content
		} else {
			fmt.Printf("Warning: extract_relationships_prompt_id %q not found\n", conv.ExtractRelationshipsPromptID)
		}
	}
	if conv.GenerateQuestionsPromptID != "" {
		if t := FindTemplateByID(pt, conv.GenerateQuestionsPromptID); t != nil {
			conv.GenerateQuestionsPrompt = t.Content
		} else {
			fmt.Printf("Warning: generate_questions_prompt_id %q not found\n", conv.GenerateQuestionsPromptID)
		}
	}
	if conv.Summary != nil {
		if conv.Summary.PromptID != "" {
			if t := FindTemplateByID(pt, conv.Summary.PromptID); t != nil {
				conv.Summary.Prompt = t.Content
			} else {
				fmt.Printf("Warning: summary.prompt_id %q not found\n", conv.Summary.PromptID)
			}
		}
		if conv.Summary.ContextTemplateID != "" {
			if t := FindTemplateByID(pt, conv.Summary.ContextTemplateID); t != nil {
				conv.Summary.ContextTemplate = t.Content
			} else {
				fmt.Printf("Warning: summary.context_template_id %q not found\n", conv.Summary.ContextTemplateID)
			}
		}
	}

	// Build intent→system-prompt map from IntentPrompts templates.
	// Template ID must equal the QueryIntent string value (e.g. "greeting").
	if len(pt.IntentPrompts) > 0 {
		conv.IntentSystemPrompts = make(map[string]string, len(pt.IntentPrompts))
		for _, t := range pt.IntentPrompts {
			if t.ID != "" && t.Content != "" {
				conv.IntentSystemPrompts[t.ID] = t.Content
			}
		}
	}
}

// FindTemplateByID searches across all template lists for a template with the given ID.
// It returns the template if found, or nil otherwise.
func FindTemplateByID(pt *PromptTemplatesConfig, id string) *PromptTemplate {
	if pt == nil || id == "" {
		return nil
	}
	// Search all template collections
	for _, list := range [][]PromptTemplate{
		pt.SystemPrompt,
		pt.ContextTemplate,
		pt.Rewrite,
		pt.Fallback,
		pt.GenerateSessionTitle,
		pt.GenerateSummary,
		pt.KeywordsExtraction,
		pt.AgentSystemPrompt,
		pt.GraphExtraction,
		pt.GenerateQuestions,
		pt.IntentPrompts,
	} {
		for i := range list {
			if list[i].ID == id {
				return &list[i]
			}
		}
	}
	return nil
}

// resolveBuiltinAgentPromptIDs resolves system_prompt_id and context_template_id
// references in builtin agent configs by looking up the actual content from
// prompt template YAML files.
func resolveBuiltinAgentPromptIDs(pt *PromptTemplatesConfig) {
	types.ResolveBuiltinAgentPromptRefs(func(id string) string {
		if t := FindTemplateByID(pt, id); t != nil {
			return t.Content
		}
		return ""
	})
}

// promptTemplateFile 用于解析模板文件
type promptTemplateFile struct {
	Templates []PromptTemplate `yaml:"templates"`
}

// loadPromptTemplates 从目录加载提示词模板
func loadPromptTemplates(configDir string) (*PromptTemplatesConfig, error) {
	templatesDir := filepath.Join(configDir, "prompt_templates")

	// 检查目录是否存在
	if _, err := os.Stat(templatesDir); os.IsNotExist(err) {
		return nil, nil // 目录不存在，返回nil让调用者使用配置文件中的模板
	}

	config := &PromptTemplatesConfig{}

	// 定义模板文件映射
	templateFiles := map[string]*[]PromptTemplate{
		"system_prompt.yaml":          &config.SystemPrompt,
		"context_template.yaml":       &config.ContextTemplate,
		"rewrite.yaml":                &config.Rewrite,
		"fallback.yaml":               &config.Fallback,
		"generate_session_title.yaml": &config.GenerateSessionTitle,
		"generate_summary.yaml":       &config.GenerateSummary,
		"keywords_extraction.yaml":    &config.KeywordsExtraction,
		"agent_system_prompt.yaml":    &config.AgentSystemPrompt,
		"graph_extraction.yaml":       &config.GraphExtraction,
		"generate_questions.yaml":     &config.GenerateQuestions,
		"intent_prompts.yaml":         &config.IntentPrompts,
	}

	// 加载每个模板文件
	for filename, target := range templateFiles {
		filePath := filepath.Join(templatesDir, filename)
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			continue // 文件不存在，跳过
		}

		data, err := os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("failed to read %s: %w", filename, err)
		}

		var file promptTemplateFile
		if err := yaml.Unmarshal(data, &file); err != nil {
			return nil, fmt.Errorf("failed to parse %s: %w", filename, err)
		}

		*target = file.Templates
	}

	return config, nil
}

// WebSearchConfig represents the web search configuration
type WebSearchConfig struct {
	Timeout int `yaml:"timeout" json:"timeout"` // 超时时间（秒）
}

// ── Entity alias dictionary ───────────────────────────────────────────────────

// EntityAliasConfig holds all alias groups loaded from entity_aliases.yaml,
// plus a separate runtime view that may include wiki-merged additions.
//
// Two-tier state model (introduced for档2):
//
//   - Groups []EntityAliasGroup
//     The yaml-on-disk state. Exposed via JSON to the Web UI so the
//     settings page only shows entries the user maintains by hand.
//     Mutating this slice via the Web-UI save path is what gets
//     persisted back to entity_aliases.yaml.
//
//   - runtimeGroups []EntityAliasGroup (private)
//     Groups + wiki-merged additions. This is what the retrieval pipeline
//     reads via DetectGroups / DetectFormGroups / DetectAttributionConflicts
//     and what query-expansion uses via the index map. Wiki augmentation
//     lives here only — never bleeds back to Groups, never gets written
//     to entity_aliases.yaml.
//
// The split fixes a "save amplification" bug that existed before档2 was
// hardened: the Web UI was showing the merged list (both yaml + 180+
// wiki-discovered products), and clicking save would bake every wiki
// entry into yaml, defeating档2's whole point (wiki = source of truth
// for product catalogs). After this split, save only persists yaml-
// originating entries; wiki augmentation is reapplied on every Build
// via RuntimeRefresh.
type EntityAliasConfig struct {
	// Groups is the raw list loaded from YAML. Treated as immutable by
	// the retrieval pipeline; only the Web-UI handler mutates it (and
	// then calls Build to re-apply wiki augmentation on top).
	Groups []EntityAliasGroup `yaml:"groups"`

	// runtimeGroups is Groups + wiki augmentation. Built by Build()
	// from a deep copy of Groups, then mutated by RuntimeRefresh.
	runtimeGroups []EntityAliasGroup

	// index maps every lowercase form (from runtimeGroups) to its sibling
	// forms for O(1) Expand() lookup.
	index map[string][]string

	// RuntimeRefresh is an optional callback invoked at the end of every
	// Build to repopulate the wiki-merged augmentation on runtimeGroups.
	// Set by main.go bootstrap so it has access to wikiSvc + kbRepo.
	// Nil in tests / when wiki merge isn't wired — Build then leaves
	// runtimeGroups as a plain copy of Groups.
	RuntimeRefresh func()
}

// EntityAliasGroup is one set of equivalent terms in the alias dictionary.
//
// Forms are cross-lingual / cross-spelling aliases for the SAME entity (e.g.
// "鲁邦通" ↔ "Robustel"). They participate in query expansion so BM25/vector
// retrieval finds documents regardless of which form the user typed.
//
// Products are model numbers / SKUs that belong to this entity (e.g. "EG5120"
// belongs to Robustel). Products are intentionally NOT expanded into queries
// (we don't want "Robustel" to inject "EG5120, R1511LG, RCMS…" into BM25 —
// that would corrupt retrieval). Products are used ONLY for entity detection
// and attribution-conflict warnings, answering the question:
// "when the user mentions model number M, which brand owns M?"
//
// Kind classifies the group for mismatch-detection scope:
//   - "" or "brand" (default) — a company/vendor that owns products. Eligible
//     to be a brand anchor for entity-mismatch detection.
//   - "technology" — a protocol or technology concept (LoRaWAN, BACnet, IoT,
//     LoRa…). Useful for query expansion but MUST NOT serve as a brand
//     anchor: a query mentioning "LoRa" is not claiming any specific brand,
//     and chunks about Robustel products that happen to discuss LoRa should
//     not be flagged as cross-brand mismatches. This was the false-positive
//     mode that originally tagged Robustel datasheets with entity_owner=
//     "鲁邦通" entity_mismatch="true" when the query contained "LoRa".
//   - "concept" — synonym of "technology"; reserved for future granularity.
//
// This split is the core fix for hallucinated product attribution: when a
// query says "Brand X's Product Y" but Y is registered under Brand Z, the
// pipeline can detect the conflict before retrieval and warn the LLM.
type EntityAliasGroup struct {
	Forms    []string `yaml:"forms"`
	Products []string `yaml:"products,omitempty"`
	Kind     string   `yaml:"kind,omitempty"`

	// Source identifies how this group entered the runtime view. Values:
	//   - "" or "yaml"   — declared in entity_aliases.yaml (UI-editable,
	//                      persisted on save).
	//   - "wiki-auto"    — auto-discovered from wiki entity pages by the
	//                      inDegree heuristic. Runtime-only; never written
	//                      to yaml. Surfaced read-only in the Web UI with
	//                      an "ignore" button that appends WikiSlug to the
	//                      denylist.
	// Not yaml-serialised — runtime-only metadata.
	Source string `yaml:"-" json:"source,omitempty"`

	// WikiSlug is the originating wiki entity slug for Source="wiki-auto"
	// groups, e.g. "entity/sierra-wireless". Used by the Web UI's ignore
	// action to identify which slug to add to entity_alias_denylist.yaml.
	// Empty for yaml-declared groups.
	WikiSlug string `yaml:"-" json:"wiki_slug,omitempty"`
}

// IsBrand reports whether the group represents a company/vendor (the only
// kind eligible to serve as a brand anchor for entity-mismatch detection).
// Empty Kind defaults to brand for backward compatibility with the pre-Kind
// yaml format.
func (g EntityAliasGroup) IsBrand() bool {
	switch g.Kind {
	case "", "brand":
		return true
	default:
		return false
	}
}

// Build refreshes the runtime view in three steps:
//  1. Deep-copy Groups → runtimeGroups (resets any prior wiki augmentation).
//  2. Invoke RuntimeRefresh if set (lets main.go's wiki-merge closure
//     re-augment runtimeGroups against the current wiki state).
//  3. Rebuild the form→siblings index from runtimeGroups for Expand().
//
// Build is the single entry point that the Web-UI save path uses after
// mutating Groups, so wiki augmentation is automatically reapplied on
// every save without bleeding into yaml.
func (c *EntityAliasConfig) Build() {
	c.runtimeGroups = make([]EntityAliasGroup, len(c.Groups))
	for i, g := range c.Groups {
		c.runtimeGroups[i] = EntityAliasGroup{
			Forms:    append([]string(nil), g.Forms...),
			Products: append([]string(nil), g.Products...),
			Kind:     g.Kind,
		}
	}
	if c.RuntimeRefresh != nil {
		c.RuntimeRefresh()
	}
	c.rebuildIndex()
}

// rebuildIndex (re)populates the form→siblings lookup map from
// runtimeGroups. Only Forms participate; Products are deliberately
// excluded to keep retrieval clean (see EntityAliasGroup doc).
func (c *EntityAliasConfig) rebuildIndex() {
	c.index = make(map[string][]string)
	for _, g := range c.runtimeGroups {
		if len(g.Forms) < 2 {
			continue
		}
		for _, f := range g.Forms {
			key := strings.ToLower(f)
			siblings := make([]string, 0, len(g.Forms)-1)
			for _, s := range g.Forms {
				if s != f {
					siblings = append(siblings, s)
				}
			}
			c.index[key] = siblings
		}
	}
}

// RuntimeGroups exposes the merged (yaml + wiki) view for tests and any
// downstream caller that needs to inspect the post-augmentation state.
// Returns a fresh slice — modifying the returned value does not affect
// the config.
func (c *EntityAliasConfig) RuntimeGroups() []EntityAliasGroup {
	if c == nil {
		return nil
	}
	out := make([]EntityAliasGroup, len(c.runtimeGroups))
	copy(out, c.runtimeGroups)
	return out
}

// DetectGroups scans text for known alias forms OR product names and returns
// a map of group-index → canonical name for every group whose forms or
// products appear in text. This is the GENEROUS variant used for scanning
// retrieved-chunk content, where any mention of a brand name or a known
// product number reveals the chunk's entity family.
//
// Technology / concept groups are intentionally included here: a chunk that
// mentions "LoRa" still belongs to a technology group, which can be useful
// for downstream non-mismatch consumers. The mismatch tagger compares chunk
// groups against the anchor (which is brand-only), so technology overlap
// between query and chunk does not produce false positives.
//
// For QUERY-side anchoring use DetectFormGroups (strict, brand-only) — see
// that method's doc for why the two are different.
func (c *EntityAliasConfig) DetectGroups(text string) map[int]string {
	return c.detectGroups(text, true /* includeProducts */, false /* brandOnly */)
}

// DetectFormGroups scans text for known alias FORMS only (no products) and
// returns matched BRAND groups (groups whose Kind is brand). Use this for
// query-side anchoring where mentioning "EG71" should NOT count as an
// explicit brand claim — the user might be asking which brand owns the
// product. Only explicit brand form mentions (e.g. "Robustel", "鲁邦通")
// count as anchors.
//
// Technology / concept groups (Kind="technology") are deliberately excluded:
// a query mentioning "LoRa" is not claiming any brand, so it must not anchor
// the mismatch check. Without this filter, any Robustel datasheet that says
// "LoRa" would falsely tag as off-topic for a brandless "LoRa" query — a
// real false positive observed in production.
//
// Example: query "Robustel EG71 specs" → DetectFormGroups returns only the
// Robustel group (anchor = Robustel). DetectGroups would also return Milesight
// (via EG71), conflating user intent with attribution. The chunk-mismatch
// algorithm then compares anchor (Robustel) against chunk content (Milesight
// via product detection) and correctly flags the mismatch.
func (c *EntityAliasConfig) DetectFormGroups(text string) map[int]string {
	return c.detectGroups(text, false /* includeProducts */, true /* brandOnly */)
}

func (c *EntityAliasConfig) detectGroups(text string, includeProducts, brandOnly bool) map[int]string {
	if c == nil || len(c.runtimeGroups) == 0 {
		return nil
	}
	lower := strings.ToLower(text)
	found := make(map[int]string)
	for gi, g := range c.runtimeGroups {
		if len(g.Forms) == 0 && len(g.Products) == 0 {
			continue
		}
		if brandOnly && !g.IsBrand() {
			continue
		}
		canonical := ""
		if len(g.Forms) > 0 {
			canonical = g.Forms[0]
		} else {
			canonical = g.Products[0]
		}
		matched := false
		for _, f := range g.Forms {
			if f != "" && strings.Contains(lower, strings.ToLower(f)) {
				matched = true
				break
			}
		}
		if !matched && includeProducts {
			for _, p := range g.Products {
				if p != "" && strings.Contains(lower, strings.ToLower(p)) {
					matched = true
					break
				}
			}
		}
		if matched {
			found[gi] = canonical
		}
	}
	return found
}

// DetectAttributionConflicts scans text for "brand X mentioned alongside
// product Y" patterns where Y is registered to a DIFFERENT brand Z. Returns
// a list of conflicts the caller can surface as warnings to the LLM.
//
// Example: text="Robustel EG71 pitch" with EG71 registered under Milesight
// returns one AttributionConflict{ClaimedOwner: "Robustel", Product: "EG71",
// ActualOwner: "Milesight"}.
//
// A conflict requires:
//  1. text contains a Form of group A (the claimed owner), AND
//  2. text contains a Product of group B (the actual owner), AND
//  3. A != B (different groups).
//
// Products mentioned without any brand context produce no conflict (no claim
// to dispute). Brands mentioned without any product produce no conflict.
type AttributionConflict struct {
	ClaimedOwner string // canonical brand the user attributed to (first Form of group A)
	Product      string // product name as it appears in text (matched form)
	ActualOwner  string // canonical brand the product is registered under (first Form of group B)
}

// DetectAttributionConflicts returns all attribution conflicts found in text.
// Only BRAND groups participate — a technology mention like "LoRa" is not a
// brand claim and therefore cannot conflict with any product attribution.
//
// Operates on runtimeGroups so wiki-merged products participate in conflict
// detection without needing to be persisted to yaml.
func (c *EntityAliasConfig) DetectAttributionConflicts(text string) []AttributionConflict {
	if c == nil || len(c.runtimeGroups) == 0 {
		return nil
	}
	lower := strings.ToLower(text)

	// Find which BRAND groups have a Form mentioned (claimed brand context).
	claimedBrands := make(map[int]string) // groupIdx → canonical name
	for gi, g := range c.runtimeGroups {
		if !g.IsBrand() {
			continue
		}
		for _, f := range g.Forms {
			if f != "" && strings.Contains(lower, strings.ToLower(f)) {
				claimedBrands[gi] = g.Forms[0]
				break
			}
		}
	}
	if len(claimedBrands) == 0 {
		return nil // No brand claim → no conflict to detect
	}

	var conflicts []AttributionConflict
	for gi, g := range c.runtimeGroups {
		if !g.IsBrand() {
			continue
		}
		if len(g.Forms) == 0 {
			continue
		}
		ownerCanonical := g.Forms[0]
		for _, p := range g.Products {
			if p == "" {
				continue
			}
			if !strings.Contains(lower, strings.ToLower(p)) {
				continue
			}
			// Product p (belonging to group gi) is mentioned. For every
			// CLAIMED brand that is NOT this group, that's a conflict.
			for claimGi, claimName := range claimedBrands {
				if claimGi == gi {
					continue // user named the correct brand
				}
				conflicts = append(conflicts, AttributionConflict{
					ClaimedOwner: claimName,
					Product:      p,
					ActualOwner:  ownerCanonical,
				})
			}
		}
	}
	return conflicts
}

// Expand returns all alias forms found in text that are not already present,
// deduplicating against seen (lowercase keys). It scans for each known term
// as a case-insensitive substring, so multi-word forms like "Schneider Electric"
// are matched correctly.
func (c *EntityAliasConfig) Expand(text string, seen map[string]struct{}) []string {
	if c == nil || len(c.index) == 0 {
		return nil
	}
	lower := strings.ToLower(text)
	var additions []string
	addedKeys := make(map[string]struct{})

	for term, siblings := range c.index {
		if !strings.Contains(lower, term) {
			continue
		}
		for _, sibling := range siblings {
			sk := strings.ToLower(sibling)
			if _, already := seen[sk]; already {
				continue
			}
			if _, already := addedKeys[sk]; already {
				continue
			}
			// Only add if the sibling is not already present in text
			if strings.Contains(lower, sk) {
				continue
			}
			additions = append(additions, sibling)
			addedKeys[sk] = struct{}{}
		}
	}
	return additions
}

// loadEntityAliases reads config/entity_aliases.yaml from configDir.
// Returns nil (no error) when the file is absent — the feature is opt-in.
func loadEntityAliases(configDir string) (*EntityAliasConfig, error) {
	path := filepath.Join(configDir, "entity_aliases.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // file optional
		}
		return nil, fmt.Errorf("entity_aliases.yaml: %w", err)
	}
	var cfg EntityAliasConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("entity_aliases.yaml parse error: %w", err)
	}
	cfg.Build()
	fmt.Printf("Loaded entity alias dictionary: %d groups\n", len(cfg.Groups))
	return &cfg, nil
}
