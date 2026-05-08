package service

// wiki_ingest_reconcile.go — periodic safety-net for the wiki pending queue.
//
// Problem addressed:
//   Redis wiki:pending:{kbID} lists accumulate items when all processing
//   tasks are permanently archived (concurrent-retry-budget exhaustion Bug 2,
//   Redis restart, process crash mid-task) and no survivor remains to drain
//   them.  Without intervention items stay there forever because the asynq
//   retry queue may be empty and there is no other trigger.
//
// Solution (reactive reconciliation):
//   Scan all wiki:pending:* keys.  For each non-empty list whose
//   wiki:active:{kbID} lock is absent — no batch currently running —
//   enqueue a fresh wiki:ingest task.  The task drains the list via the
//   normal batch pipeline.
//
// Collision safety:
//   If a legitimate task races with the reconciler and acquires the active
//   lock first, the reconciler's duplicate task hits the lock-conflict path
//   and self-reschedules (Fix 2).  No double-processing occurs.
//
// Usage:
//   Call s.ReconcileWikiPendingLists(ctx) from a periodic cron task or from
//   the startup probe.  Recommended interval: every 10–30 minutes.

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
)

const (
	// wikiReconcilePageSize is the cursor count hint for each Redis SCAN batch.
	wikiReconcilePageSize = 100

	// wikiReconcileTaskDelay gives any in-flight batch time to finish and
	// release its active lock before the reconciler's new task arrives.
	wikiReconcileTaskDelay = 30 * time.Second

	// WikiReconcileInterval is how often the background reconciler rescans
	// all wiki:pending:* keys.  Exported so the router can drive the ticker
	// without duplicating the value.
	WikiReconcileInterval = 20 * time.Minute
)

// ReconcileWikiPendingLists scans all wiki:pending:* Redis keys and
// re-enqueues a wiki:ingest task for each non-empty list that has no
// currently active batch (wiki:active:* absent).
//
// Safe to call concurrently; idempotent — duplicate tasks either find an
// empty list (no-op exit) or lose the active-lock race and self-reschedule.
func (s *wikiIngestService) ReconcileWikiPendingLists(ctx context.Context) {
	if s.redisClient == nil {
		return // Lite mode — no Redis pending lists, nothing to reconcile
	}

	logger.Infof(ctx, "wiki reconcile: scanning wiki:pending:* keys")
	pattern := wikiPendingKeyPrefix + "*"

	var cursor uint64
	var totalKeys, enqueuedCount, skippedEmpty, skippedActive, skippedWikiOff int

	for {
		scanCtx, scanCancel := context.WithTimeout(context.Background(), 10*time.Second)
		keys, nextCursor, err := s.redisClient.Scan(scanCtx, cursor, pattern, wikiReconcilePageSize).Result()
		scanCancel()
		if err != nil {
			logger.Warnf(ctx, "wiki reconcile: SCAN error at cursor %d: %v", cursor, err)
			break
		}

		for _, key := range keys {
			totalKeys++
			kbID := strings.TrimPrefix(key, wikiPendingKeyPrefix)
			if kbID == "" {
				continue
			}

			// Skip empty lists.
			listCtx, listCancel := context.WithTimeout(context.Background(), 5*time.Second)
			n, llenErr := s.redisClient.LLen(listCtx, key).Result()
			listCancel()
			if llenErr != nil || n == 0 {
				skippedEmpty++
				continue
			}

			// Skip KBs whose batch is currently running.
			activeKey := wikiActiveKeyPrefix + kbID
			activeCtx, activeCancel := context.WithTimeout(context.Background(), 5*time.Second)
			exists, existsErr := s.redisClient.Exists(activeCtx, activeKey).Result()
			activeCancel()
			if existsErr == nil && exists > 0 {
				skippedActive++
				continue
			}

			// Orphaned list: items present but no active processor.
			// Resolve KB to get TenantID and verify wiki is still enabled.
			kb, kbErr := s.kbService.GetKnowledgeBaseByIDOnly(ctx, kbID)
			if kbErr != nil || kb == nil {
				logger.Warnf(ctx, "wiki reconcile: KB %s not found, skipping (%d orphaned items): %v",
					kbID, n, kbErr)
				continue
			}

			if !kb.IndexingStrategy.WikiEnabled {
				// Wiki was disabled after items were queued — delete the stale list.
				delCtx, delCancel := context.WithTimeout(context.Background(), 5*time.Second)
				s.redisClient.Del(delCtx, key)
				delCancel()
				skippedWikiOff++
				logger.Infof(ctx, "wiki reconcile: KB %s wiki disabled — deleted stale list (%d items)", kbID, n)
				continue
			}

			// Language left empty: the wiki:ingest handler injects it from
			// context / document content detection at processing time, so the
			// reconciler does not need to know the KB's preferred language.
			payload := WikiIngestPayload{
				TenantID:        kb.TenantID,
				KnowledgeBaseID: kbID,
			}
			payloadBytes, marshalErr := json.Marshal(payload)
			if marshalErr != nil {
				logger.Warnf(ctx, "wiki reconcile: failed to marshal payload for KB %s: %v", kbID, marshalErr)
				continue
			}

			t := asynq.NewTask(types.TypeWikiIngest, payloadBytes,
				asynq.Queue("low"),
				asynq.MaxRetry(25),
				asynq.Timeout(60*time.Minute),
				asynq.ProcessIn(wikiReconcileTaskDelay),
			)
			if _, enqErr := s.task.Enqueue(t); enqErr != nil {
				logger.Warnf(ctx, "wiki reconcile: enqueue failed for KB %s (%d items): %v", kbID, n, enqErr)
			} else {
				enqueuedCount++
				logger.Infof(ctx, "wiki reconcile: enqueued recovery task for KB %s (%d orphaned items)", kbID, n)
			}
		}

		if nextCursor == 0 {
			break
		}
		cursor = nextCursor
	}

	logger.Infof(ctx, "wiki reconcile: done — keys=%d enqueued=%d skipped(empty=%d active=%d wiki_off=%d)",
		totalKeys, enqueuedCount, skippedEmpty, skippedActive, skippedWikiOff)
}
