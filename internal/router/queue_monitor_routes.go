package router

import (
	"net/http"

	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/gin-gonic/gin"
)

// RegisterAdminHomeRoute mounts a simple landing page at GET /admin that
// links to all admin sub-tools (queue monitor, backup, etc.).
// Also registers /queue shortcut → /admin/queue.
func RegisterAdminHomeRoute(r *gin.Engine) {
	r.GET("/admin", handler.ServeAdminHome)
	// Friendly shortcut: /queue → /admin/queue (UI + all API sub-paths)
	r.GET("/queue", func(c *gin.Context) {
		c.Redirect(http.StatusMovedPermanently, "/admin/queue")
	})
	r.GET("/queue/*path", func(c *gin.Context) {
		c.Redirect(http.StatusMovedPermanently, "/admin/queue"+c.Param("path"))
	})
}

// RegisterQueueMonitorRoutes mounts the queue monitoring UI and API.
// All routes are intentionally outside the auth middleware — this is a
// local-only admin tool accessible only on 127.0.0.1.
//
// UI:  GET  /admin/queue
// API: GET  /admin/queue/api/stats
//      GET  /admin/queue/api/documents
//      GET  /admin/queue/api/failures
//      POST /admin/queue/api/reenqueue
//      GET  /admin/queue/api/failed-docs
//      POST /admin/queue/api/retry-doc/:id
//      POST /admin/queue/api/retry-all-failed-docs
func RegisterQueueMonitorRoutes(r *gin.Engine, _ *gin.RouterGroup, h *handler.QueueMonitorHandler) {
	admin := r.Group("/admin/queue")
	{
		admin.GET("", h.ServeUI)
		admin.GET("/api/stats", h.GetStats)
		admin.GET("/api/documents", h.GetDocuments)
		admin.GET("/api/failures", h.GetFailures)
		admin.POST("/api/reenqueue", h.ReenqueueFailures)
		// Failed document reparse (直接触发重解析 · 无需重新上传)
		admin.GET("/api/failed-docs", h.GetFailedDocs)
		admin.POST("/api/retry-doc/:id", h.RetryDoc)
		admin.POST("/api/retry-all-failed-docs", h.RetryAllFailedDocs)
	}
}
