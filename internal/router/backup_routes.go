package router

import (
	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/gin-gonic/gin"
)

// RegisterBackupRoutes mounts the admin backup/export endpoints.
// All routes are outside the auth middleware — local-only admin tool
// accessible only on 127.0.0.1.
//
//	GET /admin/backup/database  — dump the database (pg_dump .sql.gz or SQLite .db)
//	GET /admin/backup/files     — archive LOCAL_STORAGE_BASE_DIR (.tar.gz)
//	GET /admin/backup/config    — export non-secret runtime configuration (.json)
func RegisterBackupRoutes(r *gin.Engine, h *handler.BackupHandler) {
	admin := r.Group("/admin/backup")
	{
		admin.GET("/database", h.ExportDatabase)
		admin.GET("/files", h.ExportFiles)
		admin.GET("/config", h.ExportConfig)
	}
}
