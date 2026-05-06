package router

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	"go.uber.org/dig"
)

type AsynqTaskParams struct {
	dig.In

	Server               *asynq.Server
	KnowledgeService     interfaces.KnowledgeService
	KnowledgeBaseService interfaces.KnowledgeBaseService
	TagService           interfaces.KnowledgeTagService
	DataSourceService    interfaces.DataSourceService
	ChunkExtractor       interfaces.TaskHandler `name:"chunkExtractor"`
	DataTableSummary     interfaces.TaskHandler `name:"dataTableSummary"`
	ImageMultimodal      interfaces.TaskHandler `name:"imageMultimodal"`
	KnowledgePostProcess interfaces.TaskHandler `name:"knowledgePostProcess"`
	WikiIngest           interfaces.TaskHandler `name:"wikiIngest"`
}

func getAsynqRedisClientOpt() *asynq.RedisClientOpt {
	db := 0
	if dbStr := os.Getenv("REDIS_DB"); dbStr != "" {
		if parsed, err := strconv.Atoi(dbStr); err == nil {
			db = parsed
		}
	}
	opt := &asynq.RedisClientOpt{
		Addr:         os.Getenv("REDIS_ADDR"),
		Username:     os.Getenv("REDIS_USERNAME"),
		Password:     os.Getenv("REDIS_PASSWORD"),
		ReadTimeout:  100 * time.Millisecond,
		WriteTimeout: 200 * time.Millisecond,
		DB:           db,
	}
	return opt
}

func NewAsyncqClient() (*asynq.Client, error) {
	opt := getAsynqRedisClientOpt()
	client := asynq.NewClient(opt)
	err := client.Ping()
	if err != nil {
		return nil, err
	}
	return client, nil
}

// wikiIngestRetryDelay is a fixed, short backoff for wiki ingest lock
// conflicts. Must be slightly longer than the active-lock TTL's worst-case
// "just got set" window so the retry is highly likely to succeed without
// burning through retries; but short enough that users don't feel the stall.
const wikiIngestRetryDelay = 15 * time.Second

// asynqRetryDelayFunc customizes per-task retry backoff.
//
// Default asynq backoff is exponential (≈10s, 40s, 90s, 2.5m, ...), which
// is appropriate for transient errors like remote HTTP failures. But for
// wiki ingest lock conflicts (ErrWikiIngestConcurrent), exponential
// backoff is harmful: a freshly orphaned lock expires in ≤60s, so a 15s
// fixed retry virtually guarantees the next attempt succeeds. Without
// this override, a crash-restart cycle can leave a KB unable to make
// progress for 7–10 minutes while the orphan lock expires AND the retry
// schedule catches up.
func asynqRetryDelayFunc(n int, e error, t *asynq.Task) time.Duration {
	if errors.Is(e, service.ErrWikiIngestConcurrent) {
		return wikiIngestRetryDelay
	}
	return asynq.DefaultRetryDelayFunc(n, e, t)
}

func NewAsynqServer() *asynq.Server {
	opt := getAsynqRedisClientOpt()
	srv := asynq.NewServer(
		opt,
		asynq.Config{
			// Concurrency: LLM/embedding 调用全是 I/O bound（等 API 响应），
			// 设为 CPU 核数的 2 倍可大幅提升吞吐，不增加 CPU 负担。
			Concurrency: 16,
			// 队列权重：chunk:extract / wiki:ingest / summary / question 全在 low 队列。
			// 原始权重 low:1 导致 pipeline 只能获得约 0.8 个 worker（严重瓶颈）。
			// 反转权重后 low 队列获得 ~10 个 worker，吞吐提升 ~10x。
			Queues: map[string]int{
				"critical": 2, // 实时对话/紧急任务（低并发）
				"default":  2, // document:process（同时最多 2 个大文档在解析）
				"low":      6, // chunk:extract + wiki:ingest — pipeline 主力
			},
			RetryDelayFunc: asynqRetryDelayFunc,
		},
	)
	return srv
}

// buildRedisClientForReclaim creates a short-lived redis.Client using the same
// connection parameters as the asynq server. Returns nil when Redis is not
// configured (Lite mode) so callers can skip reclaim gracefully.
func buildRedisClientForReclaim() *redis.Client {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		return nil // Lite mode — no Redis, no reclaim needed
	}
	db := 0
	if dbStr := os.Getenv("REDIS_DB"); dbStr != "" {
		if parsed, err := strconv.Atoi(dbStr); err == nil {
			db = parsed
		}
	}
	rc := redis.NewClient(&redis.Options{
		Addr:     addr,
		Username: os.Getenv("REDIS_USERNAME"),
		Password: os.Getenv("REDIS_PASSWORD"),
		DB:       db,
	})
	if err := rc.Ping(context.Background()).Err(); err != nil {
		logger.Warnf(context.Background(), "[Asynq] Redis ping failed during startup reclaim: %v", err)
		_ = rc.Close()
		return nil
	}
	return rc
}

// reclaimStaleAsynqTasks recovers tasks that were left in the "active" list
// when a previous server instance crashed or was forcibly killed.
//
// Background: asynq marks a task active when a worker dequeues it. On a clean
// shutdown the worker re-queues in-flight tasks via SIGTERM handling. On a
// crash or SIGKILL, no cleanup runs — tasks stay in the active list forever
// and are never retried unless explicitly reclaimed.
//
// This function runs synchronously at startup (before any worker goroutine
// accepts new work) so there is no race with live workers.
//
// For each active task we distinguish three cases:
//   - Real task (msg field present, len > 0): move back to pending so it gets
//     retried with a fresh attempt count.
//   - Ghost entry (msg field absent / zero-length): the task data TTL already
//     expired; just remove the dangling ID from the active list.
//
// Queues are processed in reverse-priority order so higher-priority queues
// drain into pending first.
func reclaimStaleAsynqTasks(ctx context.Context, rc *redis.Client) {
	queues := []string{"low", "default", "critical"}
	totalReclaimed, totalGhosts := 0, 0

	for _, q := range queues {
		activeKey := fmt.Sprintf("asynq:{%s}:active", q)
		pendingKey := fmt.Sprintf("asynq:{%s}:pending", q)

		ids, err := rc.LRange(ctx, activeKey, 0, -1).Result()
		if err != nil || len(ids) == 0 {
			continue
		}

		reclaimed, ghosts := 0, 0
		for _, id := range ids {
			taskKey := fmt.Sprintf("asynq:{%s}:t:%s", q, id)

			// Check whether the task still has its message payload.
			// A hash with only the "state" key (len==1) is a ghost.
			fields, herr := rc.HLen(ctx, taskKey).Result()
			if herr != nil {
				continue
			}

			if fields <= 1 {
				// Ghost: payload TTL expired; just remove from active list.
				_ = rc.LRem(ctx, activeKey, 0, id).Err()
				ghosts++
				continue
			}

			// Real task: move back to pending atomically.
			pipe := rc.Pipeline()
			pipe.LRem(ctx, activeKey, 1, id)
			pipe.HSet(ctx, taskKey, "state", "pending")
			pipe.RPush(ctx, pendingKey, id)
			if _, err := pipe.Exec(ctx); err == nil {
				reclaimed++
			}
		}

		if reclaimed+ghosts > 0 {
			logger.Infof(ctx, "[Asynq] queue=%s reclaimed=%d ghost_removed=%d", q, reclaimed, ghosts)
		}
		totalReclaimed += reclaimed
		totalGhosts += ghosts
	}

	if totalReclaimed+totalGhosts > 0 {
		logger.Infof(ctx, "[Asynq] Startup reclaim complete: tasks_recovered=%d ghost_entries_removed=%d",
			totalReclaimed, totalGhosts)
	}
}

func RunAsynqServer(params AsynqTaskParams) *asynq.ServeMux {
	// Reclaim any tasks that were left in the "active" list by a previous
	// crashed/killed server instance. This must run before workers start so
	// there is no race between reclaim and live task processing.
	if rc := buildRedisClientForReclaim(); rc != nil {
		reclaimCtx := context.Background()
		reclaimStaleAsynqTasks(reclaimCtx, rc)
		_ = rc.Close()
	}

	// Create a new mux and register all handlers
	mux := asynq.NewServeMux()

	// Install Langfuse middleware BEFORE handler registration so every task
	// type is automatically wrapped. When Langfuse is disabled the middleware
	// is a pass-through; when enabled it resumes the upstream HTTP trace (if
	// the payload carries one) or opens a standalone trace, then wraps the
	// handler execution in a SPAN so all child generations (embedding / VLM /
	// chat / rerank / ASR) nest correctly in the Langfuse UI.
	mux.Use(langfuse.AsynqMiddleware())

	// Register extract handlers - router will dispatch to appropriate handler
	mux.HandleFunc(types.TypeChunkExtract, params.ChunkExtractor.Handle)
	mux.HandleFunc(types.TypeDataTableSummary, params.DataTableSummary.Handle)

	// Register document processing handler
	mux.HandleFunc(types.TypeDocumentProcess, params.KnowledgeService.ProcessDocument)

	// Register manual knowledge processing handler (cleanup + re-indexing)
	mux.HandleFunc(types.TypeManualProcess, params.KnowledgeService.ProcessManualUpdate)

	// Register FAQ import handler (includes dry run mode)
	mux.HandleFunc(types.TypeFAQImport, params.KnowledgeService.ProcessFAQImport)

	// Register question generation handler
	mux.HandleFunc(types.TypeQuestionGeneration, params.KnowledgeService.ProcessQuestionGeneration)

	// Register summary generation handler
	mux.HandleFunc(types.TypeSummaryGeneration, params.KnowledgeService.ProcessSummaryGeneration)

	// Register KB clone handler
	mux.HandleFunc(types.TypeKBClone, params.KnowledgeService.ProcessKBClone)

	// Register knowledge move handler
	mux.HandleFunc(types.TypeKnowledgeMove, params.KnowledgeService.ProcessKnowledgeMove)

	// Register knowledge list delete handler
	mux.HandleFunc(types.TypeKnowledgeListDelete, params.KnowledgeService.ProcessKnowledgeListDelete)

	// Register index delete handler
	mux.HandleFunc(types.TypeIndexDelete, params.TagService.ProcessIndexDelete)

	// Register KB delete handler
	mux.HandleFunc(types.TypeKBDelete, params.KnowledgeBaseService.ProcessKBDelete)

	// Register image multimodal handler
	mux.HandleFunc(types.TypeImageMultimodal, params.ImageMultimodal.Handle)

	// Register knowledge post process handler
	mux.HandleFunc(types.TypeKnowledgePostProcess, params.KnowledgePostProcess.Handle)

	// Register data source sync handler
	mux.HandleFunc(types.TypeDataSourceSync, params.DataSourceService.ProcessSync)

	// Register wiki ingest handler
	mux.HandleFunc(types.TypeWikiIngest, params.WikiIngest.Handle)

	go func() {
		// Start the server
		if err := params.Server.Run(mux); err != nil {
			log.Fatalf("could not run server: %v", err)
		}
	}()
	return mux
}
