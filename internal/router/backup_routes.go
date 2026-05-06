package router

import (
	"net/http"

	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/gin-gonic/gin"
)

// RegisterBackupRoutes mounts the admin backup/export endpoints.
// All routes are outside the auth middleware — local-only admin tool
// accessible only on 127.0.0.1.
//
//	GET /admin/backup           — web UI
//	GET /admin/backup/status    — JSON: DB info, storage size, pg_dump availability
//	GET /admin/backup/database  — dump the database (pg_dump .sql.gz or SQLite .db)
//	GET /admin/backup/files     — archive LOCAL_STORAGE_BASE_DIR (.tar.gz)
//	GET /admin/backup/config    — export non-secret runtime configuration (.json)
//
// Shortcut aliases (redirect to canonical paths):
//
//	GET /backup  →  /admin/backup
func RegisterBackupRoutes(r *gin.Engine, h *handler.BackupHandler) {
	// Canonical routes
	admin := r.Group("/admin/backup")
	{
		admin.GET("", h.ServeUI)
		admin.GET("/status", h.Status)
		admin.GET("/database", h.ExportDatabase)
		admin.GET("/files", h.ExportFiles)
		admin.GET("/config", h.ExportConfig)
	}

	// Friendly shortcut: /backup → /admin/backup
	// Preserves the sub-path so /backup/database still works.
	r.GET("/backup", func(c *gin.Context) {
		c.Redirect(http.StatusMovedPermanently, "/admin/backup")
	})
	r.GET("/backup/*path", func(c *gin.Context) {
		c.Redirect(http.StatusMovedPermanently, "/admin/backup"+c.Param("path"))
	})
}
