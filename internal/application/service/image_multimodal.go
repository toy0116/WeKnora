package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	filesvc "github.com/Tencent/WeKnora/internal/application/service/file"
	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/utils/ollama"
	"github.com/Tencent/WeKnora/internal/models/vlm"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

const (
	vlmOCRPrompt = "<system_prompt>\n" +
		"You are an OCR assistant. Your task is to extract all body text content from this document image and output in pure Markdown format.\n" +
		"</system_prompt>\n\n" +
		"<instructions>\n" +
		"1. Ignore headers and footers.\n" +
		"2. Use Markdown table syntax for tables.\n" +
		"3. Use LaTeX format for formulas (wrapped with $ or $$).\n" +
		"4. Organize content in the original reading order.\n" +
		"5. Output ONLY the extracted text content. Do NOT include any HTML tags, reasoning, or unrelated comments.\n" +
		"6. If there is absolutely no recognizable text content in the image, reply ONLY with: No text content.\n" +
		"</instructions>"
	vlmOCRScannedPDFPrompt = "<system_prompt>\n" +
		"You are an OCR and document layout extraction assistant. The input image is a page from a scanned PDF document.\n" +
		"Your task is to carefully extract all text and layout structure from the image, and output the result in pure Markdown format.\n" +
		"</system_prompt>\n\n" +
		"<instructions>\n" +
		"1. Ignore headers, footers, and page numbers.\n" +
		"2. Preserve the original document's paragraph and hierarchical structure as much as possible.\n" +
		"3. If there are tables, use Markdown table syntax to represent them.\n" +
		"4. If there are mathematical formulas, use LaTeX format wrapped in $ or $$.\n" +
		"5. Output ONLY the extracted text content. Do NOT include any HTML tags, reasoning, or unrelated comments.\n" +
		"6. If there is absolutely no recognizable text content in the image, reply ONLY with: No text content.\n" +
		"</instructions>"
	vlmCaptionPrompt = "你是工业物联网产品技术文档的专业图像理解助手（服务于Robustel/鲁邦通工业通信产品）。\n\n" +
		"请对图像进行完整、精确的中文描述，确保用户无需看图即可准确回答以下任意类型的问题：" +
		"接口在哪个位置、拓扑如何连接、接线怎么接、软件在哪里配置、尺寸是多少、颜色形状是什么。\n\n" +
		"【强制要求，违反则描述无效】\n" +
		"0. 直接开始描述，禁止以【好的】【当然】【好的，我将】【作为助手】等任何礼貌性或角色性开头语起始。\n" +
		"1. 视角声明（必须是第一句）：先明确说明这是设备的哪个面——正面/背面/顶面/左侧面/右侧面/3D透视图/示意图/拓扑图等。" +
		"如果图像中有多个面，逐一说明。无法确认时写【视角不明确】。\n" +
		"2. 型号/标签准确性：只写你能在图中清晰辨认的文字（型号、接口标签、参数值）。" +
		"无法清晰读取时写【标签不清晰】，绝对不能推测或补全。\n" +
		"   特别注意：Robustel产品系列前缀容易混淆——R（路由器系列）、EG（工业IoT网关系列）、LG（LoRa网关系列）是完全不同的产品线，" +
		"不存在【EGS】系列，勿将EG误读为EGS。读取型号时逐字符确认；若不确定，写【型号不清晰】而非猜测。\n" +
		"3. LED/指示灯：同一标签不得重复出现在两处位置。若两个指示灯看起来相同，描述其位置差异（如左列第2个），不得凭印象给同一标签。\n\n" +
		"按图像类型选择描述重点：\n" +
		"- 接口/面板示意图或外观照片：视角声明后，按面板方位（顶面、左侧、右侧、正面、背面）分组，" +
		"逐一列出每个接口/部件的名称和从左到右/从上到下的排列顺序\n" +
		"- 网络拓扑/场景图：描述设备层级关系、连接介质（LTE/Wi-Fi/以太网/RS-485等）、数据流方向、涉及的行业场景\n" +
		"- 接线图：按端子物理排列顺序（从左到右）列出信号名、功能、电源极性、连接对象\n" +
		"- 软件界面截图：描述菜单路径（如System > Network > WAN）、所有可见参数名称和当前值\n" +
		"- 规格/参数图或表格：提取全部参数名称、数值和单位，完整还原行列结构\n" +
		"- 尺寸/安装图：提取全部标注尺寸（含单位mm/cm）、安装孔位置、安装方式（导轨/壁挂等）\n" +
		"- 框图/架构图：列出所有模块名称、连接关系、协议类型\n\n" +
		"要求：技术术语和接口名称保留英文原文（ETH1、RS-485、ANT、SIM、GPIO、LTE等），其余用中文描述，不省略任何技术细节。"
)

// ImageMultimodalService handles image:multimodal asynq tasks.
// It reads images from storage (via FileService for provider:// URLs),
// performs OCR and VLM caption, and creates child chunks.
type ImageMultimodalService struct {
	chunkService   interfaces.ChunkService
	modelService   interfaces.ModelService
	kbService      interfaces.KnowledgeBaseService
	knowledgeRepo  interfaces.KnowledgeRepository
	tenantRepo     interfaces.TenantRepository
	retrieveEngine interfaces.RetrieveEngineRegistry
	ollamaService  *ollama.OllamaService
	taskEnqueuer   interfaces.TaskEnqueuer
	redisClient    *redis.Client
}

func NewImageMultimodalService(
	chunkService interfaces.ChunkService,
	modelService interfaces.ModelService,
	kbService interfaces.KnowledgeBaseService,
	knowledgeRepo interfaces.KnowledgeRepository,
	tenantRepo interfaces.TenantRepository,
	retrieveEngine interfaces.RetrieveEngineRegistry,
	ollamaService *ollama.OllamaService,
	taskEnqueuer interfaces.TaskEnqueuer,
	redisClient *redis.Client,
) interfaces.TaskHandler {
	return &ImageMultimodalService{
		chunkService:   chunkService,
		modelService:   modelService,
		kbService:      kbService,
		knowledgeRepo:  knowledgeRepo,
		tenantRepo:     tenantRepo,
		retrieveEngine: retrieveEngine,
		ollamaService:  ollamaService,
		taskEnqueuer:   taskEnqueuer,
		redisClient:    redisClient,
	}
}

// Handle implements asynq handler for TypeImageMultimodal.
func (s *ImageMultimodalService) Handle(ctx context.Context, task *asynq.Task) error {
	var payload types.ImageMultimodalPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("unmarshal image multimodal payload: %w", err)
	}

	logger.Infof(ctx, "[ImageMultimodal] Processing image: chunk=%s, url=%s, ocr=%v, caption=%v",
		payload.ChunkID, payload.ImageURL, payload.EnableOCR, payload.EnableCaption)

	ctx = context.WithValue(ctx, types.TenantIDContextKey, payload.TenantID)
	if payload.Language != "" {
		ctx = context.WithValue(ctx, types.LanguageContextKey, payload.Language)
	}

	vlmModel, err := s.resolveVLM(ctx, payload.KnowledgeBaseID)
	if err != nil {
		return fmt.Errorf("resolve VLM: %w", err)
	}

	// Read image bytes: try provider:// via tenant-resolved FileService,
	// then legacy local path, then HTTP URL.
	var imgBytes []byte
	if types.ParseProviderScheme(payload.ImageURL) != "" {
		fileSvc := s.resolveFileServiceForPayload(ctx, payload)
		if fileSvc == nil {
			logger.Warnf(ctx, "[ImageMultimodal] Resolve tenant file service failed, fallback to URL/local: tenant=%d kb=%s",
				payload.TenantID, payload.KnowledgeBaseID)
		} else {
			// provider:// scheme — read via FileService
			reader, getErr := fileSvc.GetFile(ctx, payload.ImageURL)
			if getErr != nil {
				logger.Warnf(ctx, "[ImageMultimodal] FileService.GetFile(%s) failed: %v", payload.ImageURL, getErr)
			} else {
				imgBytes, err = io.ReadAll(reader)
				reader.Close()
				if err != nil {
					logger.Warnf(ctx, "[ImageMultimodal] Read provider file %s failed: %v", payload.ImageURL, err)
					imgBytes = nil
				}
			}
		}
	}
	if imgBytes == nil && payload.ImageLocalPath != "" {
		imgBytes, err = os.ReadFile(payload.ImageLocalPath)
		if err != nil {
			logger.Warnf(ctx, "[ImageMultimodal] Local file %s not available (%v), trying URL", payload.ImageLocalPath, err)
			imgBytes = nil
		}
	}
	if imgBytes == nil {
		imgBytes, err = downloadImageFromURL(payload.ImageURL)
		if err != nil {
			logger.Errorf(ctx, "[ImageMultimodal] Failed to download image from URL %s: %v", payload.ImageURL, err)
			return fmt.Errorf("read image from URL %s failed: %w", payload.ImageURL, err)
		}
		logger.Infof(ctx, "[ImageMultimodal] Image downloaded from URL, len=%d", len(imgBytes))
	}

	imageInfo := types.ImageInfo{
		URL:         payload.ImageURL,
		OriginalURL: payload.ImageURL,
	}

	if payload.EnableOCR {
		prompt := vlmOCRPrompt
		if payload.ImageSourceType == "scanned_pdf" {
			prompt = vlmOCRScannedPDFPrompt
			logger.Infof(ctx, "[ImageMultimodal] Using scanned PDF prompt for OCR: %s", payload.ImageURL)
		}
		
		ocrText, ocrErr := vlmModel.Predict(ctx, [][]byte{imgBytes}, prompt)
		if ocrErr != nil {
			logger.Warnf(ctx, "[ImageMultimodal] OCR failed for %s: %v", payload.ImageURL, ocrErr)
		} else {
			ocrText = sanitizeOCRText(ocrText)
			if ocrText != "" {
				imageInfo.OCRText = ocrText
			} else {
				logger.Warnf(ctx, "[ImageMultimodal] OCR returned empty/invalid content for %s, discarded", payload.ImageURL)
			}
		}
	}

	caption, capErr := vlmModel.Predict(ctx, [][]byte{imgBytes}, vlmCaptionPrompt)
	if capErr != nil {
		logger.Warnf(ctx, "[ImageMultimodal] Caption failed for %s: %v", payload.ImageURL, capErr)
	} else if caption != "" {
		imageInfo.Caption = caption
	}

	// Build child chunks for OCR and caption results
	imageInfoJSON, _ := json.Marshal([]types.ImageInfo{imageInfo})
	var newChunks []*types.Chunk

	if imageInfo.OCRText != "" {
		newChunks = append(newChunks, &types.Chunk{
			ID:              uuid.New().String(),
			TenantID:        payload.TenantID,
			KnowledgeID:     payload.KnowledgeID,
			KnowledgeBaseID: payload.KnowledgeBaseID,
			Content:         imageInfo.OCRText,
			ChunkType:       types.ChunkTypeImageOCR,
			ParentChunkID:   payload.ChunkID,
			IsEnabled:       true,
			Flags:           types.ChunkFlagRecommended,
			ImageInfo:       string(imageInfoJSON),
			CreatedAt:       time.Now(),
			UpdatedAt:       time.Now(),
		})
	}

	if imageInfo.Caption != "" {
		newChunks = append(newChunks, &types.Chunk{
			ID:              uuid.New().String(),
			TenantID:        payload.TenantID,
			KnowledgeID:     payload.KnowledgeID,
			KnowledgeBaseID: payload.KnowledgeBaseID,
			Content:         imageInfo.Caption,
			ChunkType:       types.ChunkTypeImageCaption,
			ParentChunkID:   payload.ChunkID,
			IsEnabled:       true,
			Flags:           types.ChunkFlagRecommended,
			ImageInfo:       string(imageInfoJSON),
			CreatedAt:       time.Now(),
			UpdatedAt:       time.Now(),
		})
	}

	if len(newChunks) == 0 {
		s.checkAndFinalizeAllImages(ctx, payload)
		return nil
	}

	// Persist chunks
	if err := s.chunkService.CreateChunks(ctx, newChunks); err != nil {
		return fmt.Errorf("create multimodal chunks: %w", err)
	}
	for _, c := range newChunks {
		logger.Infof(ctx, "[ImageMultimodal] Created %s chunk %s for image %s, len=%d",
			c.ChunkType, c.ID, payload.ImageURL, len(c.Content))
	}

	// Index chunks so they can be retrieved
	s.indexChunks(ctx, payload, newChunks)

	// Enqueue question generation for the caption/OCR content if KB has it enabled.
	// During initial processChunks, question generation is skipped for image-type
	// knowledge because the text chunk is just a markdown reference. Now that we
	// have real textual content (caption/OCR), we can generate questions.
	// Note: for documents with multiple images (e.g. PDFs), we also wait until
	// all images are processed before triggering summary/question generation.
	s.checkAndFinalizeAllImages(ctx, payload)

	return nil
}

// indexChunks indexes the newly created multimodal chunks into the retrieval engine
// so they can participate in semantic search.
func (s *ImageMultimodalService) indexChunks(ctx context.Context, payload types.ImageMultimodalPayload, chunks []*types.Chunk) {
	kb, err := s.kbService.GetKnowledgeBaseByIDOnly(ctx, payload.KnowledgeBaseID)
	if err != nil || kb == nil {
		logger.Warnf(ctx, "[ImageMultimodal] Failed to get KB for indexing: %v", err)
		return
	}

	// Skip vector/keyword indexing when the KB has no embedding-based pipeline enabled
	// (e.g. Wiki-only KBs). Without this check, GetEmbeddingModel would fail because
	// EmbeddingModelID is intentionally empty for such KBs. The multimodal chunks
	// themselves are already persisted in the DB above, so skipping index here is safe.
	if !kb.NeedsEmbeddingModel() {
		logger.Infof(ctx, "[ImageMultimodal] Vector/keyword indexing disabled for KB %s, skipping index for %d multimodal chunks",
			kb.ID, len(chunks))
		// Still mark chunks as indexed so downstream finalization sees a consistent state.
		for _, chunk := range chunks {
			dbChunk, gerr := s.chunkService.GetChunkByIDOnly(ctx, chunk.ID)
			if gerr != nil {
				logger.Warnf(ctx, "[ImageMultimodal] Failed to fetch chunk %s for status update: %v", chunk.ID, gerr)
				continue
			}
			dbChunk.Status = int(types.ChunkStatusIndexed)
			if uerr := s.chunkService.UpdateChunk(ctx, dbChunk); uerr != nil {
				logger.Warnf(ctx, "[ImageMultimodal] Failed to update chunk %s status to indexed: %v", chunk.ID, uerr)
			}
		}
		return
	}

	embeddingModel, err := s.modelService.GetEmbeddingModel(ctx, kb.EmbeddingModelID)
	if err != nil {
		logger.Warnf(ctx, "[ImageMultimodal] Failed to get embedding model for indexing: %v", err)
		return
	}

	tenantInfo, err := s.tenantRepo.GetTenantByID(ctx, payload.TenantID)
	if err != nil {
		logger.Warnf(ctx, "[ImageMultimodal] Failed to get tenant for indexing: %v", err)
		return
	}

	engine, err := retriever.NewCompositeRetrieveEngine(s.retrieveEngine, tenantInfo.GetEffectiveEngines())
	if err != nil {
		logger.Warnf(ctx, "[ImageMultimodal] Failed to init retrieve engine: %v", err)
		return
	}

	indexInfoList := make([]*types.IndexInfo, 0, len(chunks))
	for _, chunk := range chunks {
		indexInfoList = append(indexInfoList, &types.IndexInfo{
			Content:         chunk.Content,
			SourceID:        chunk.ID,
			SourceType:      types.ChunkSourceType,
			ChunkID:         chunk.ID,
			KnowledgeID:     chunk.KnowledgeID,
			KnowledgeBaseID: chunk.KnowledgeBaseID,
		})
	}

	if err := engine.BatchIndex(ctx, embeddingModel, indexInfoList); err != nil {
		logger.Errorf(ctx, "[ImageMultimodal] Failed to index multimodal chunks: %v", err)
		return
	}

	// Mark chunks as indexed.
	// Must re-fetch from DB because the in-memory objects lack auto-generated fields
	// (e.g. seq_id), and GORM Save would overwrite them with zero values.
	for _, chunk := range chunks {
		dbChunk, err := s.chunkService.GetChunkByIDOnly(ctx, chunk.ID)
		if err != nil {
			logger.Warnf(ctx, "[ImageMultimodal] Failed to fetch chunk %s for status update: %v", chunk.ID, err)
			continue
		}
		dbChunk.Status = int(types.ChunkStatusIndexed)
		if err := s.chunkService.UpdateChunk(ctx, dbChunk); err != nil {
			logger.Warnf(ctx, "[ImageMultimodal] Failed to update chunk %s status to indexed: %v", chunk.ID, err)
		}
	}

	logger.Infof(ctx, "[ImageMultimodal] Indexed %d multimodal chunks for image %s", len(chunks), payload.ImageURL)
}

// resolveVLM creates a vlm.VLM instance for the given knowledge base,
// supporting both new-style (ModelID) and legacy (inline BaseURL) configs.
func (s *ImageMultimodalService) resolveVLM(ctx context.Context, kbID string) (vlm.VLM, error) {
	kb, err := s.kbService.GetKnowledgeBaseByIDOnly(ctx, kbID)
	if err != nil {
		return nil, fmt.Errorf("get knowledge base %s: %w", kbID, err)
	}
	if kb == nil {
		return nil, fmt.Errorf("knowledge base %s not found", kbID)
	}

	vlmCfg := kb.VLMConfig
	if !vlmCfg.IsEnabled() {
		return nil, fmt.Errorf("VLM is not enabled for knowledge base %s", kbID)
	}

	// New-style: resolve model through ModelService
	if vlmCfg.ModelID != "" {
		return s.modelService.GetVLMModel(ctx, vlmCfg.ModelID)
	}

	// Legacy: create VLM from inline config
	return vlm.NewVLMFromLegacyConfig(vlmCfg, s.ollamaService)
}

// resolveFileServiceForPayload resolves tenant/KB scoped file service for reading provider:// URLs.
func (s *ImageMultimodalService) resolveFileServiceForPayload(ctx context.Context, payload types.ImageMultimodalPayload) interfaces.FileService {
	tenant, err := s.tenantRepo.GetTenantByID(ctx, payload.TenantID)
	if err != nil || tenant == nil {
		logger.Warnf(ctx, "[ImageMultimodal] GetTenantByID failed: tenant=%d err=%v", payload.TenantID, err)
		return nil
	}

	provider := types.ParseProviderScheme(payload.ImageURL)
	if provider == "" {
		kb, kbErr := s.kbService.GetKnowledgeBaseByIDOnly(ctx, payload.KnowledgeBaseID)
		if kbErr != nil {
			logger.Warnf(ctx, "[ImageMultimodal] GetKnowledgeBaseByIDOnly failed: kb=%s err=%v", payload.KnowledgeBaseID, kbErr)
		} else if kb != nil {
			provider = strings.ToLower(strings.TrimSpace(kb.GetStorageProvider()))
		}
	}

	baseDir := strings.TrimSpace(os.Getenv("LOCAL_STORAGE_BASE_DIR"))
	fileSvc, _, svcErr := filesvc.NewFileServiceFromStorageConfig(provider, tenant.StorageEngineConfig, baseDir)
	if svcErr != nil {
		logger.Warnf(ctx, "[ImageMultimodal] resolve file service failed: tenant=%d provider=%s err=%v", payload.TenantID, provider, svcErr)
		return nil
	}
	return fileSvc
}

// downloadImageFromURL downloads image bytes from an HTTP(S) URL.
func downloadImageFromURL(imageURL string) ([]byte, error) {
	return secutils.DownloadBytes(imageURL)
}

func (s *ImageMultimodalService) checkAndFinalizeAllImages(ctx context.Context, payload types.ImageMultimodalPayload) {
	if s.redisClient == nil {
		s.enqueueKnowledgePostProcessTask(ctx, payload)
		return
	}

	redisKey := fmt.Sprintf("multimodal:pending:%s", payload.KnowledgeID)
	
	pendingCount, err := s.redisClient.Decr(ctx, redisKey).Result()
	if err != nil && err != redis.Nil {
		logger.Warnf(ctx, "[ImageMultimodal] Failed to decrement pending count for %s: %v", payload.KnowledgeID, err)
		return
	}

	if pendingCount <= 0 {
		logger.Infof(ctx, "[ImageMultimodal] All images processed for knowledge %s. Finalizing...", payload.KnowledgeID)
		s.redisClient.Del(ctx, redisKey)

		s.enqueueKnowledgePostProcessTask(ctx, payload)
	}
}

func (s *ImageMultimodalService) enqueueKnowledgePostProcessTask(ctx context.Context, payload types.ImageMultimodalPayload) {
	if s.taskEnqueuer == nil {
		return
	}
	
	taskPayload := types.KnowledgePostProcessPayload{
		TenantID:        payload.TenantID,
		KnowledgeID:     payload.KnowledgeID,
		KnowledgeBaseID: payload.KnowledgeBaseID,
		Language:        payload.Language,
	}
	langfuse.InjectTracing(ctx, &taskPayload)
	payloadBytes, err := json.Marshal(taskPayload)
	if err != nil {
		logger.Warnf(ctx, "[ImageMultimodal] Failed to marshal post process payload: %v", err)
		return
	}

	task := asynq.NewTask(types.TypeKnowledgePostProcess, payloadBytes, asynq.Queue("low"), asynq.MaxRetry(3))
	if _, err := s.taskEnqueuer.Enqueue(task); err != nil {
		logger.Warnf(ctx, "[ImageMultimodal] Failed to enqueue post process task for %s: %v", payload.KnowledgeID, err)
	} else {
		logger.Infof(ctx, "[ImageMultimodal] Enqueued post process task for %s", payload.KnowledgeID)
	}
}
