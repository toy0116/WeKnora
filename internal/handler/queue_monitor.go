package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// QueueMonitorHandler serves the queue monitoring UI and its backing APIs.
// It caches queue stats in memory (refreshed every 30s by a background goroutine)
// so HTTP handlers don't block on full Redis scans.
type QueueMonitorHandler struct {
	redis     *redis.Client
	db        *gorm.DB
	kgService interfaces.KnowledgeService // optional · for retry-doc

	mu        sync.RWMutex
	lastStats *QueueStats
}

func NewQueueMonitorHandler(redisClient *redis.Client, db *gorm.DB, kgService interfaces.KnowledgeService) *QueueMonitorHandler {
	h := &QueueMonitorHandler{redis: redisClient, db: db, kgService: kgService}
	go h.refreshLoop()
	return h
}

// ── types ────────────────────────────────────────────────────────────────────

type TaskTypeCounts struct {
	Type     string `json:"type"`
	Pending  int64  `json:"pending"`
	Active   int64  `json:"active"`
	Retry    int64  `json:"retry"`
	Archived int64  `json:"archived"`
}

type DocProgress struct {
	KnowledgeID string `json:"knowledge_id"`
	Title       string `json:"title"`
	TotalChunks int64  `json:"total_chunks"`
	// chunks still pending entity extraction in queue
	PendingExtract int64 `json:"pending_extract"`
	// wiki pages published for this document
	WikiPages  int64  `json:"wiki_pages"`
	ParseStatus string `json:"parse_status"`
	UpdatedAt  string `json:"updated_at"`
}

type FailedTask struct {
	TaskID    string `json:"task_id"`
	Queue     string `json:"queue"`
	Type      string `json:"type"`
	ChunkID   string `json:"chunk_id,omitempty"`
	ErrorMsg  string `json:"error_msg"`
	RetryCount int   `json:"retry_count"`
}

type QueueStats struct {
	RefreshedAt  time.Time        `json:"refreshed_at"`
	ByType       []TaskTypeCounts `json:"by_type"`
	TotalPending int64            `json:"total_pending"`
	TotalActive  int64            `json:"total_active"`
	TotalFailed  int64            `json:"total_failed"`
	// chunk_ids that are still pending/active in queue (for doc progress join)
	pendingChunkIDs map[string]struct{}
}

// ── background refresh ────────────────────────────────────────────────────────

func (h *QueueMonitorHandler) refreshLoop() {
	// initial refresh
	h.refresh()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		h.refresh()
	}
}

func (h *QueueMonitorHandler) refresh() {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	queues := []string{"critical", "default", "low"}

	// per-type counters
	typePending := map[string]int64{}
	typeActive := map[string]int64{}
	typeRetry := map[string]int64{}
	typeArchived := map[string]int64{}
	pendingChunkIDs := map[string]struct{}{}

	for _, q := range queues {
		// pending — scan all IDs, parse type from protobuf
		ids, err := h.redis.LRange(ctx, fmt.Sprintf("asynq:{%s}:pending", q), 0, -1).Result()
		if err == nil {
			for _, id := range ids {
				typ, payload := h.parseTaskMsg(ctx, q, id)
				if typ != "" {
					typePending[typ]++
					if typ == "chunk:extract" {
						if cid := extractChunkID(payload); cid != "" {
							pendingChunkIDs[cid] = struct{}{}
						}
					}
				}
			}
		}

		// active
		activeIDs, err := h.redis.LRange(ctx, fmt.Sprintf("asynq:{%s}:active", q), 0, -1).Result()
		if err == nil {
			for _, id := range activeIDs {
				typ, payload := h.parseTaskMsg(ctx, q, id)
				if typ != "" {
					typeActive[typ]++
					if typ == "chunk:extract" {
						if cid := extractChunkID(payload); cid != "" {
							pendingChunkIDs[cid] = struct{}{}
						}
					}
				}
			}
		}

		// retry
		retryIDs, err := h.redis.ZRange(ctx, fmt.Sprintf("asynq:{%s}:retry", q), 0, -1).Result()
		if err == nil {
			for _, id := range retryIDs {
				typ, _ := h.parseTaskMsg(ctx, q, id)
				if typ != "" {
					typeRetry[typ]++
				}
			}
		}

		// archived
		archivedIDs, err := h.redis.ZRange(ctx, fmt.Sprintf("asynq:{%s}:archived", q), 0, -1).Result()
		if err == nil {
			for _, id := range archivedIDs {
				typ, _ := h.parseTaskMsg(ctx, q, id)
				if typ != "" {
					typeArchived[typ]++
				}
			}
		}
	}

	// build sorted ByType list
	allTypes := map[string]struct{}{}
	for t := range typePending { allTypes[t] = struct{}{} }
	for t := range typeActive  { allTypes[t] = struct{}{} }
	for t := range typeRetry   { allTypes[t] = struct{}{} }
	for t := range typeArchived { allTypes[t] = struct{}{} }

	byType := make([]TaskTypeCounts, 0, len(allTypes))
	for t := range allTypes {
		byType = append(byType, TaskTypeCounts{
			Type:     t,
			Pending:  typePending[t],
			Active:   typeActive[t],
			Retry:    typeRetry[t],
			Archived: typeArchived[t],
		})
	}
	sort.Slice(byType, func(i, j int) bool { return byType[i].Type < byType[j].Type })

	var totalPending, totalActive, totalFailed int64
	for _, c := range byType {
		totalPending += c.Pending
		totalActive += c.Active
		totalFailed += c.Archived
	}

	stats := &QueueStats{
		RefreshedAt:     time.Now(),
		ByType:          byType,
		TotalPending:    totalPending,
		TotalActive:     totalActive,
		TotalFailed:     totalFailed,
		pendingChunkIDs: pendingChunkIDs,
	}

	h.mu.Lock()
	h.lastStats = stats
	h.mu.Unlock()
}

// parseTaskMsg reads the asynq protobuf msg field and returns (type, payloadJSON).
func (h *QueueMonitorHandler) parseTaskMsg(ctx context.Context, queue, taskID string) (string, string) {
	raw, err := h.redis.HGet(ctx, fmt.Sprintf("asynq:{%s}:t:%s", queue, taskID), "msg").Bytes()
	if err != nil || len(raw) < 3 || raw[0] != 0x0a {
		return "", ""
	}
	tlen := int(raw[1])
	if 2+tlen > len(raw) {
		return "", ""
	}
	typ := string(raw[2 : 2+tlen])
	rest := raw[2+tlen:]
	if len(rest) < 2 || rest[0] != 0x12 {
		return typ, ""
	}
	plen := int(rest[1])
	if 2+plen > len(rest) {
		return typ, ""
	}
	return typ, string(rest[2 : 2+plen])
}

func extractChunkID(payload string) string {
	if payload == "" {
		return ""
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(payload), &m); err != nil {
		return ""
	}
	if v, ok := m["chunk_id"].(string); ok {
		return v
	}
	return ""
}

// ── HTTP handlers ─────────────────────────────────────────────────────────────

// GetStats returns cached queue stats.
// GET /api/v1/admin/queue/stats
func (h *QueueMonitorHandler) GetStats(c *gin.Context) {
	h.mu.RLock()
	stats := h.lastStats
	h.mu.RUnlock()

	if stats == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "stats not ready yet, retry in a moment"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"refreshed_at":  stats.RefreshedAt,
		"by_type":       stats.ByType,
		"total_pending": stats.TotalPending,
		"total_active":  stats.TotalActive,
		"total_failed":  stats.TotalFailed,
	})
}

// GetDocuments returns per-document processing progress.
// GET /api/v1/admin/queue/documents
func (h *QueueMonitorHandler) GetDocuments(c *gin.Context) {
	ctx := c.Request.Context()

	h.mu.RLock()
	stats := h.lastStats
	h.mu.RUnlock()

	type row struct {
		KnowledgeID string    `gorm:"column:id"`
		Title       string    `gorm:"column:title"`
		ParseStatus string    `gorm:"column:parse_status"`
		UpdatedAt   time.Time `gorm:"column:updated_at"`
		TotalChunks int64     `gorm:"column:total_chunks"`
		WikiPages   int64     `gorm:"column:wiki_pages"`
	}

	var rows []row
	err := h.db.WithContext(ctx).Raw(`
		SELECT
			k.id,
			k.title,
			k.parse_status,
			k.updated_at,
			COUNT(DISTINCT c.id) AS total_chunks,
			COUNT(DISTINCT wp.id) AS wiki_pages
		FROM knowledges k
		LEFT JOIN chunks c ON c.knowledge_id = k.id AND c.deleted_at IS NULL
		LEFT JOIN wiki_pages wp ON wp.knowledge_base_id = k.knowledge_base_id
		WHERE k.deleted_at IS NULL
		GROUP BY k.id, k.title, k.parse_status, k.updated_at
		ORDER BY k.updated_at DESC
		LIMIT 100
	`).Scan(&rows).Error

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Cross-reference with pending chunk IDs from cached stats
	var docs []DocProgress
	for _, r := range rows {
		pendingExtract := int64(0)
		if stats != nil {
			// Count how many of this doc's chunks are still in the extract queue
			// (Approximate: we don't have a cheap per-doc reverse index, so
			//  we query chunks for this knowledge and check against pendingChunkIDs)
			if len(stats.pendingChunkIDs) > 0 {
				var chunkIDs []string
				h.db.WithContext(ctx).Raw(
					"SELECT id FROM chunks WHERE knowledge_id = ? AND deleted_at IS NULL",
					r.KnowledgeID,
				).Scan(&chunkIDs)
				for _, cid := range chunkIDs {
					if _, ok := stats.pendingChunkIDs[cid]; ok {
						pendingExtract++
					}
				}
			}
		}

		docs = append(docs, DocProgress{
			KnowledgeID:    r.KnowledgeID,
			Title:          r.Title,
			TotalChunks:    r.TotalChunks,
			PendingExtract: pendingExtract,
			WikiPages:      r.WikiPages,
			ParseStatus:    r.ParseStatus,
			UpdatedAt:      r.UpdatedAt.Format("2006-01-02 15:04"),
		})
	}

	c.JSON(http.StatusOK, gin.H{"documents": docs})
}

// GetFailures returns failed (archived) tasks with error info.
// GET /api/v1/admin/queue/failures
func (h *QueueMonitorHandler) GetFailures(c *gin.Context) {
	ctx := c.Request.Context()
	queues := []string{"default", "low"}
	var failures []FailedTask

	for _, q := range queues {
		ids, err := h.redis.ZRange(ctx, fmt.Sprintf("asynq:{%s}:archived", q), 0, 99).Result()
		if err != nil {
			continue
		}
		for _, id := range ids {
			typ, payload := h.parseTaskMsg(ctx, q, id)
			errMsg, _ := h.redis.HGet(ctx, fmt.Sprintf("asynq:{%s}:t:%s", q, id), "error_msg").Result()
			retryStr, _ := h.redis.HGet(ctx, fmt.Sprintf("asynq:{%s}:t:%s", q, id), "retried").Result()
			retry := 0
			fmt.Sscanf(retryStr, "%d", &retry)
			failures = append(failures, FailedTask{
				TaskID:     id,
				Queue:      q,
				Type:       typ,
				ChunkID:    extractChunkID(payload),
				ErrorMsg:   errMsg,
				RetryCount: retry,
			})
		}
	}

	c.JSON(http.StatusOK, gin.H{"failures": failures, "total": len(failures)})
}

// ReenqueueFailures re-enqueues selected archived tasks back to low queue.
// POST /api/v1/admin/queue/reenqueue
// Body: { "task_ids": ["id1", "id2"] }  — empty = reenqueue all
func (h *QueueMonitorHandler) ReenqueueFailures(c *gin.Context) {
	ctx := c.Request.Context()

	var req struct {
		TaskIDs []string `json:"task_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	queues := []string{"default", "low"}
	requeued := 0

	for _, q := range queues {
		var ids []string
		if len(req.TaskIDs) == 0 {
			// all archived
			var err error
			ids, err = h.redis.ZRange(ctx, fmt.Sprintf("asynq:{%s}:archived", q), 0, -1).Result()
			if err != nil {
				continue
			}
		} else {
			ids = req.TaskIDs
		}

		for _, id := range ids {
			// re-push to pending list; asynq workers will pick it up
			// (We move the task key from archived zset back to pending list)
			pipe := h.redis.Pipeline()
			pipe.ZRem(ctx, fmt.Sprintf("asynq:{%s}:archived", q), id)
			pipe.HSet(ctx, fmt.Sprintf("asynq:{%s}:t:%s", q, id), "state", "pending")
			pipe.LPush(ctx, fmt.Sprintf("asynq:{low}:pending", ), id)
			if _, err := pipe.Exec(ctx); err == nil {
				requeued++
			}
		}
	}

	// Trigger immediate stats refresh
	go h.refresh()

	c.JSON(http.StatusOK, gin.H{"requeued": requeued})
}

// FailedDoc is returned by GetFailedDocs.
type FailedDoc struct {
	KnowledgeID string `json:"knowledge_id"`
	Title       string `json:"title"`
	ParseStatus string `json:"parse_status"`
	ErrorMsg    string `json:"error_msg"`
	TenantID    uint64 `json:"tenant_id"`
	UpdatedAt   string `json:"updated_at"`
}

// GetFailedDocs returns documents whose parse_status is 'failed'.
// GET /admin/queue/api/failed-docs
func (h *QueueMonitorHandler) GetFailedDocs(c *gin.Context) {
	ctx := c.Request.Context()

	type row struct {
		ID          string    `gorm:"column:id"`
		Title       string    `gorm:"column:title"`
		ParseStatus string    `gorm:"column:parse_status"`
		ErrorMsg    string    `gorm:"column:error_message"`
		TenantID    uint64    `gorm:"column:tenant_id"`
		UpdatedAt   time.Time `gorm:"column:updated_at"`
	}

	var rows []row
	err := h.db.WithContext(ctx).Raw(`
		SELECT id, title, parse_status, error_message, tenant_id, updated_at
		FROM knowledges
		WHERE parse_status = 'failed'
		  AND deleted_at IS NULL
		ORDER BY updated_at DESC
		LIMIT 200
	`).Scan(&rows).Error
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	docs := make([]FailedDoc, 0, len(rows))
	for _, r := range rows {
		docs = append(docs, FailedDoc{
			KnowledgeID: r.ID,
			Title:       r.Title,
			ParseStatus: r.ParseStatus,
			ErrorMsg:    r.ErrorMsg,
			TenantID:    r.TenantID,
			UpdatedAt:   r.UpdatedAt.Format("2006-01-02 15:04"),
		})
	}

	c.JSON(http.StatusOK, gin.H{"failed_docs": docs, "total": len(docs)})
}

// buildAdminCtx looks up the Tenant record and injects both TenantIDContextKey
// and TenantInfoContextKey into ctx — the service layer asserts on both.
func (h *QueueMonitorHandler) buildAdminCtx(ctx context.Context, tenantID uint64) (context.Context, error) {
	var tenant types.Tenant
	if err := h.db.WithContext(ctx).
		Where("id = ?", tenantID).
		First(&tenant).Error; err != nil {
		return ctx, fmt.Errorf("tenant %d not found: %w", tenantID, err)
	}
	ctx = context.WithValue(ctx, types.TenantIDContextKey, tenantID)
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, &tenant)
	return ctx, nil
}

// RetryDoc triggers a reparse for a single failed document.
// POST /admin/queue/api/retry-doc/:id
func (h *QueueMonitorHandler) RetryDoc(c *gin.Context) {
	knowledgeID := c.Param("id")
	if knowledgeID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "knowledge id required"})
		return
	}
	if h.kgService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "knowledge service not available"})
		return
	}

	ctx := c.Request.Context()

	// Look up tenant_id so we can build the admin context
	var tenantID uint64
	if err := h.db.WithContext(ctx).Raw(
		"SELECT tenant_id FROM knowledges WHERE id = ? AND deleted_at IS NULL LIMIT 1",
		knowledgeID,
	).Scan(&tenantID).Error; err != nil || tenantID == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "document not found"})
		return
	}

	adminCtx, err := h.buildAdminCtx(ctx, tenantID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	knowledge, err := h.kgService.ReparseKnowledge(adminCtx, knowledgeID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":           true,
		"knowledge_id": knowledge.ID,
		"title":        knowledge.Title,
		"parse_status": knowledge.ParseStatus,
	})
}

// RetryAllFailedDocs triggers reparse for all documents with parse_status = 'failed'.
// POST /admin/queue/api/retry-all-failed-docs
func (h *QueueMonitorHandler) RetryAllFailedDocs(c *gin.Context) {
	if h.kgService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "knowledge service not available"})
		return
	}

	ctx := c.Request.Context()

	type row struct {
		ID       string `gorm:"column:id"`
		TenantID uint64 `gorm:"column:tenant_id"`
	}
	var rows []row
	if err := h.db.WithContext(ctx).Raw(`
		SELECT id, tenant_id FROM knowledges
		WHERE parse_status = 'failed' AND deleted_at IS NULL
		LIMIT 50
	`).Scan(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	queued := 0
	var errs []string
	// Cache admin contexts per tenant to avoid repeated lookups
	tenantCtxCache := map[uint64]context.Context{}
	for _, r := range rows {
		adminCtx, ok := tenantCtxCache[r.TenantID]
		if !ok {
			var err error
			adminCtx, err = h.buildAdminCtx(ctx, r.TenantID)
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", r.ID, err))
				continue
			}
			tenantCtxCache[r.TenantID] = adminCtx
		}
		if _, err := h.kgService.ReparseKnowledge(adminCtx, r.ID); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", r.ID, err))
			continue
		}
		queued++
	}

	c.JSON(http.StatusOK, gin.H{
		"queued": queued,
		"errors": errs,
		"total":  len(rows),
	})
}

// ServeUI serves the monitoring HTML page.
// GET /admin/queue
func (h *QueueMonitorHandler) ServeUI(c *gin.Context) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.String(http.StatusOK, queueMonitorHTML)
}
