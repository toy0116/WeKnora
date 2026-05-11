// Package main is the main package for the WeKnora server
// It contains the main function and the entry point for the server
//
// @title           WeKnora API
// @version         1.0
// @description     WeKnora 知识库管理系统 API 文档
// @termsOfService  http://swagger.io/terms/
//
// @contact.name   WeKnora Github
// @contact.url    https://github.com/Tencent/WeKnora
//
// @BasePath  /api/v1
//
// @securityDefinitions.apikey Bearer
// @in header
// @name Authorization
// @description 用户登录认证：输入 Bearer {token} 格式的 JWT 令牌

// @securityDefinitions.apikey ApiKeyAuth
// @in header
// @name X-API-Key
// @description 租户身份认证：输入 sk- 开头的 API Key
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/container"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/runtime"
	"github.com/Tencent/WeKnora/internal/tracing"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// listAllEntityWikiPages enumerates every published wiki page with
// PageType="entity" across all KBs. Used at startup to feed
// EntityAliasConfig.MergeFromWiki — see档2 in cmd/server/main.go.
//
// Pagination: WikiPageService.ListPages is per-KB; we iterate KBs and
// page through each. Page size 500 keeps the round-trip count low for
// the ~7000-page deployments we expect.
func listAllEntityWikiPages(
	ctx context.Context,
	wikiSvc interfaces.WikiPageService,
	kbs []*types.KnowledgeBase,
) ([]*types.WikiPage, error) {
	const pageSize = 500
	var all []*types.WikiPage
	for _, kb := range kbs {
		if kb == nil || kb.ID == "" {
			continue
		}
		for page := 1; ; page++ {
			resp, err := wikiSvc.ListPages(ctx, &types.WikiPageListRequest{
				KnowledgeBaseID: kb.ID,
				PageType:        "entity",
				Status:          "published",
				Page:            page,
				PageSize:        pageSize,
			})
			if err != nil {
				return nil, fmt.Errorf("kb %s: %w", kb.ID, err)
			}
			if resp == nil || len(resp.Pages) == 0 {
				break
			}
			all = append(all, resp.Pages...)
			if len(resp.Pages) < pageSize {
				break
			}
		}
	}
	return all, nil
}

func main() {
	// Set Gin mode
	if os.Getenv("GIN_MODE") == "release" {
		gin.SetMode(gin.ReleaseMode)
	} else {
		gin.SetMode(gin.DebugMode)
	}

	// Build dependency injection container
	c := container.BuildContainer(runtime.GetContainer())

	// Run application
	err := c.Invoke(func(
		cfg *config.Config,
		router *gin.Engine,
		tracer *tracing.Tracer,
		resourceCleaner interfaces.ResourceCleaner,
		kbRepo interfaces.KnowledgeBaseRepository,
		wikiSvc interfaces.WikiPageService,
	) error {
		bootstrapCtx := context.Background()

		// Populate doc-class KB defaults: yaml's kb_defaults is keyed by KB
		// name, but chunk-render code path passes KB UUIDs. Resolve once at
		// startup so the classifier can look up by ID in O(1). KB
		// create/rename will need to re-call ResolveKBDefaults; we'll wire
		// that in the KB handler when the feature settles.
		var kbs []*types.KnowledgeBase
		if cfg.DocClasses != nil || cfg.EntityAliases != nil {
			var err error
			kbs, err = kbRepo.ListKnowledgeBases(bootstrapCtx)
			if err != nil {
				logger.Warnf(bootstrapCtx, "bootstrap: failed to list KBs: %v", err)
			}
		}
		if cfg.DocClasses != nil {
			summaries := make([]config.KBSummary, 0, len(kbs))
			for _, kb := range kbs {
				summaries = append(summaries, config.KBSummary{ID: kb.ID, Name: kb.Name})
			}
			cfg.DocClasses.ResolveKBDefaults(summaries)
			logger.Infof(bootstrapCtx, "doc-class: resolved kb_defaults for %d KBs", len(summaries))
		}

		// 档2 — augment entity aliases from wiki entity pages.
		// The yaml declares the brand groups (the surface of "what entities
		// we track"); wiki entity pages supply the actual aliases and
		// brand→product relationships. Maintaining the brand→product list
		// in yaml duplicates what wiki entity edits already encode; this
		// merge lets the wiki be the source of truth and yaml become a
		// declaration of which brands matter.
		if cfg.EntityAliases != nil && wikiSvc != nil && len(kbs) > 0 {
			allEntityPages, err := listAllEntityWikiPages(bootstrapCtx, wikiSvc, kbs)
			if err != nil {
				logger.Warnf(bootstrapCtx, "entity-alias: wiki listing failed: %v", err)
			} else {
				formsAdded, productsAdded := cfg.EntityAliases.MergeFromWiki(allEntityPages)
				// Rebuild the internal lookup index — MergeFromWiki may have
				// changed which forms are in each group.
				cfg.EntityAliases.Build()
				logger.Infof(bootstrapCtx,
					"entity-alias: merged %d wiki entity pages — forms+%d products+%d",
					len(allEntityPages), formsAdded, productsAdded,
				)
			}
		}

		// Create HTTP server
		server := &http.Server{
			Handler: router,
		}

		addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
		listener, err := listenWithRetry(addr, 10, 300*time.Millisecond)
		if err != nil {
			return fmt.Errorf("failed to start server: %v", err)
		}

		ctx, done := context.WithCancel(context.Background())

		signals := make(chan os.Signal, 1)
		signal.Notify(signals, shutdownSignals...)
		go func() {
			sig := <-signals
			logger.Infof(context.Background(), "Received signal: %v, starting server shutdown...", sig)

			// Close listener first to release port immediately,
			// so the next process can bind during our graceful drain.
			listener.Close()

			shutdownTimeout := cfg.Server.ShutdownTimeout
			if shutdownTimeout == 0 {
				shutdownTimeout = 30 * time.Second
			}
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
			defer shutdownCancel()

			// Second signal → force close all connections immediately
			go func() {
				sig := <-signals
				logger.Warnf(context.Background(), "Received second signal: %v, forcing shutdown...", sig)
				server.Close()
			}()

			if err := server.Shutdown(shutdownCtx); err != nil {
				logger.Errorf(context.Background(), "Server forced to shutdown: %v", err)
				server.Close()
			}

			logger.Info(context.Background(), "Cleaning up resources...")
			errs := resourceCleaner.Cleanup(shutdownCtx)
			if len(errs) > 0 {
				logger.Errorf(context.Background(), "Errors occurred during resource cleanup: %v", errs)
			}
			logger.Info(context.Background(), "Server has exited")
			done()
		}()

		logger.Infof(context.Background(), "Server is running at %s", addr)
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("server error: %v", err)
		}

		<-ctx.Done()
		return nil
	})
	if err != nil {
		logger.Fatalf(context.Background(), "Failed to run application: %v", err)
	}
}
