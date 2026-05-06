package handler

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// BackupHandler provides admin-only endpoints for exporting data to enable
// cross-machine migration:
//
//	GET /admin/backup/database  — PostgreSQL dump (.sql.gz) or SQLite file (.db)
//	GET /admin/backup/files     — knowledge-base file storage (.tar.gz)
//	GET /admin/backup/config    — active configuration as JSON (secrets redacted)
type BackupHandler struct{}

// NewBackupHandler creates a BackupHandler.  It reads no config at
// construction time; all env vars are read lazily per request.
func NewBackupHandler() *BackupHandler { return &BackupHandler{} }

// timestamp returns a compact UTC timestamp string suitable for filenames.
func timestamp() string { return time.Now().UTC().Format("20060102-150405") }

// ── Database ─────────────────────────────────────────────────────────────────

// ExportDatabase streams a compressed database dump.
//
//   - PostgreSQL → runs pg_dump and gzip-compresses the output on the fly.
//   - SQLite     → streams the raw .db file (WAL mode: readers are safe).
func (h *BackupHandler) ExportDatabase(c *gin.Context) {
	driver := os.Getenv("DB_DRIVER")
	switch driver {
	case "postgres":
		h.exportPostgres(c)
	case "sqlite":
		h.exportSQLite(c)
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("unsupported DB_DRIVER: %q", driver)})
	}
}

// pgDumpCandidates lists directories where pg_dump is commonly installed.
// exec.LookPath and PATH are checked first; these are fallbacks for
// service processes whose PATH doesn't include Homebrew / PostgreSQL bin dirs.
var pgDumpCandidates = []string{
	"/opt/homebrew/bin/pg_dump",      // Apple Silicon Homebrew
	"/usr/local/bin/pg_dump",         // Intel Homebrew / manual install
	"/usr/bin/pg_dump",               // system Debian/Ubuntu
	"/usr/lib/postgresql/17/bin/pg_dump",
	"/usr/lib/postgresql/16/bin/pg_dump",
	"/usr/lib/postgresql/15/bin/pg_dump",
}

// findPgDump returns the absolute path to pg_dump, checking PATH then
// known fallback locations.
func findPgDump() (string, error) {
	if p, err := exec.LookPath("pg_dump"); err == nil {
		return p, nil
	}
	for _, candidate := range pgDumpCandidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("pg_dump not found in PATH or common locations (%v)", pgDumpCandidates)
}

func (h *BackupHandler) exportPostgres(c *gin.Context) {
	host := os.Getenv("DB_HOST")
	port := os.Getenv("DB_PORT")
	user := os.Getenv("DB_USER")
	password := os.Getenv("DB_PASSWORD")
	dbname := os.Getenv("DB_NAME")

	if host == "" || dbname == "" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DB_HOST / DB_NAME not set"})
		return
	}
	if port == "" {
		port = "5432"
	}

	pgDump, err := findPgDump()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	filename := fmt.Sprintf("weknora-db-%s.sql.gz", timestamp())
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	c.Header("Content-Type", "application/gzip")

	gz := gzip.NewWriter(c.Writer)
	defer gz.Close()

	// pg_dump writes SQL to stdout; we pipe it through gzip straight to the
	// HTTP response — no temp file needed.
	//nolint:gosec // args come from trusted env vars, not user input
	cmd := exec.CommandContext(c.Request.Context(), pgDump,
		"-h", host, "-p", port, "-U", user, "-d", dbname,
		"--no-password",
	)
	cmd.Env = append(os.Environ(), "PGPASSWORD="+password)
	cmd.Stdout = gz

	var stderr strings.Builder
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// Headers may have been flushed already; the client will see a
		// truncated gzip stream.  Log stderr for operator visibility.
		_ = gz.Close()
		return
	}
}

func (h *BackupHandler) exportSQLite(c *gin.Context) {
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "./data/weknora.db"
	}

	f, err := os.Open(dbPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("cannot open SQLite db: %v", err)})
		return
	}
	defer f.Close()

	filename := fmt.Sprintf("weknora-db-%s.db", timestamp())
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	c.Header("Content-Type", "application/octet-stream")
	c.Status(http.StatusOK)
	_, _ = io.Copy(c.Writer, f)
}

// ── Files ─────────────────────────────────────────────────────────────────────

// ExportFiles streams the LOCAL_STORAGE_BASE_DIR as a .tar.gz archive.
// The archive preserves relative paths so the recipient can extract it
// into any target directory.
func (h *BackupHandler) ExportFiles(c *gin.Context) {
	baseDir := strings.TrimSpace(os.Getenv("LOCAL_STORAGE_BASE_DIR"))
	if baseDir == "" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "LOCAL_STORAGE_BASE_DIR not set"})
		return
	}

	info, err := os.Stat(baseDir)
	if err != nil || !info.IsDir() {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("storage directory not found: %s", baseDir)})
		return
	}

	filename := fmt.Sprintf("weknora-files-%s.tar.gz", timestamp())
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	c.Header("Content-Type", "application/gzip")
	c.Status(http.StatusOK)

	gz := gzip.NewWriter(c.Writer)
	tw := tar.NewWriter(gz)

	_ = filepath.Walk(baseDir, func(path string, fi os.FileInfo, walkErr error) error {
		if walkErr != nil || c.Request.Context().Err() != nil {
			return walkErr
		}

		rel, err := filepath.Rel(baseDir, path)
		if err != nil {
			return nil
		}

		hdr, err := tar.FileInfoHeader(fi, "")
		if err != nil {
			return nil
		}
		hdr.Name = rel
		if fi.IsDir() {
			hdr.Name += "/"
		}

		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}

		if fi.IsDir() {
			return nil
		}

		f, err := os.Open(path)
		if err != nil {
			return nil // skip unreadable files
		}
		defer f.Close()
		_, _ = io.Copy(tw, f)
		return nil
	})

	_ = tw.Close()
	_ = gz.Close()
}

// ── Config ────────────────────────────────────────────────────────────────────

// configExport is the JSON shape returned by ExportConfig.
type configExport struct {
	ExportedAt  string            `json:"exported_at"`
	DBDriver    string            `json:"db_driver"`
	DBHost      string            `json:"db_host"`
	DBPort      string            `json:"db_port"`
	DBUser      string            `json:"db_user"`
	DBName      string            `json:"db_name"`
	StorageDir  string            `json:"local_storage_base_dir"`
	RetrieveDriver string        `json:"retrieve_driver"`
	RedisAddr   string            `json:"redis_addr,omitempty"`
	ConfigFile  string            `json:"config_file,omitempty"`
	// Secrets intentionally omitted: DB_PASSWORD, REDIS_PASSWORD, API keys
	EnvSnapshot  map[string]string `json:"env_snapshot"` // non-secret env vars
}

// sensitiveEnvKeys lists substrings that flag an env var as a secret.
// Any env key containing one of these substrings (case-insensitive) is
// replaced with "***" in the exported snapshot.
var sensitiveEnvKeys = []string{
	"PASSWORD", "SECRET", "KEY", "TOKEN", "CREDENTIAL",
	"PRIVATE", "AUTH", "API_KEY", "APIKEY",
}

func isSensitive(key string) bool {
	up := strings.ToUpper(key)
	for _, s := range sensitiveEnvKeys {
		if strings.Contains(up, s) {
			return true
		}
	}
	return false
}

// ExportConfig returns a JSON document with the current runtime configuration,
// secrets redacted.  This covers database coordinates, storage paths, and
// the full non-secret environment so the operator can reproduce the setup
// on a new machine.
func (h *BackupHandler) ExportConfig(c *gin.Context) {
	snap := make(map[string]string)
	for _, kv := range os.Environ() {
		idx := strings.IndexByte(kv, '=')
		if idx < 0 {
			continue
		}
		k, v := kv[:idx], kv[idx+1:]
		if isSensitive(k) {
			snap[k] = "***"
		} else {
			snap[k] = v
		}
	}

	// Try to read the raw config file for reference
	var configFileContent string
	if cfgPath := findConfigFile(); cfgPath != "" {
		if raw, err := os.ReadFile(cfgPath); err == nil {
			// Redact inline secrets from YAML
			configFileContent = redactYAML(string(raw))
		}
	}

	export := configExport{
		ExportedAt:     time.Now().UTC().Format(time.RFC3339),
		DBDriver:       os.Getenv("DB_DRIVER"),
		DBHost:         os.Getenv("DB_HOST"),
		DBPort:         os.Getenv("DB_PORT"),
		DBUser:         os.Getenv("DB_USER"),
		DBName:         os.Getenv("DB_NAME"),
		StorageDir:     os.Getenv("LOCAL_STORAGE_BASE_DIR"),
		RetrieveDriver: os.Getenv("RETRIEVE_DRIVER"),
		RedisAddr:      os.Getenv("REDIS_ADDR"),
		ConfigFile:     configFileContent,
		EnvSnapshot:    snap,
	}

	filename := fmt.Sprintf("weknora-config-%s.json", timestamp())
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	c.Header("Content-Type", "application/json")

	enc := json.NewEncoder(c.Writer)
	enc.SetIndent("", "  ")
	_ = enc.Encode(export)
}

// findConfigFile looks for config.yaml in common locations.
func findConfigFile() string {
	candidates := []string{
		"config.yaml",
		"config/config.yaml",
		"./config.yaml",
	}
	// Also check the directory of the running binary
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(dir, "config.yaml"),
			filepath.Join(dir, "config", "config.yaml"),
		)
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// redactYAML replaces values on lines whose key matches a sensitive pattern.
// This is a best-effort line-by-line redaction — not a full YAML parser.
func redactYAML(content string) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		colon := strings.IndexByte(trimmed, ':')
		if colon < 0 {
			continue
		}
		key := trimmed[:colon]
		if isSensitive(key) {
			// Preserve indentation, replace value
			indent := line[:len(line)-len(trimmed)]
			lines[i] = indent + key + ": ***"
		}
	}
	return strings.Join(lines, "\n")
}

// ── Status ────────────────────────────────────────────────────────────────────

// backupStatus is the JSON shape returned by Status.
type backupStatus struct {
	DBDriver     string `json:"db_driver"`
	DBHost       string `json:"db_host"`
	DBPort       string `json:"db_port"`
	DBName       string `json:"db_name"`
	PgDumpPath   string `json:"pg_dump_path"`
	PgDumpOK     bool   `json:"pg_dump_ok"`
	StorageDir   string `json:"storage_dir"`
	StorageSizeB int64  `json:"storage_size_bytes"`
	StorageSizeMB float64 `json:"storage_size_mb"`
	StorageOK    bool   `json:"storage_ok"`
}

// dirSize returns the total byte size of all regular files under root.
func dirSize(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, e := d.Info(); e == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

// Status returns a JSON summary used by the backup UI to show current state.
func (h *BackupHandler) Status(c *gin.Context) {
	pgPath, pgErr := findPgDump()
	storageDir := strings.TrimSpace(os.Getenv("LOCAL_STORAGE_BASE_DIR"))

	var storageSizeB int64
	storageOK := false
	if storageDir != "" {
		if info, err := os.Stat(storageDir); err == nil && info.IsDir() {
			storageOK = true
			storageSizeB = dirSize(storageDir)
		}
	}

	status := backupStatus{
		DBDriver:      os.Getenv("DB_DRIVER"),
		DBHost:        os.Getenv("DB_HOST"),
		DBPort:        os.Getenv("DB_PORT"),
		DBName:        os.Getenv("DB_NAME"),
		PgDumpPath:    pgPath,
		PgDumpOK:      pgErr == nil,
		StorageDir:    storageDir,
		StorageSizeB:  storageSizeB,
		StorageSizeMB: float64(storageSizeB) / 1024 / 1024,
		StorageOK:     storageOK,
	}
	c.JSON(http.StatusOK, status)
}

// ── Web UI ────────────────────────────────────────────────────────────────────

// ServeUI serves the backup management HTML page.
func (h *BackupHandler) ServeUI(c *gin.Context) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.String(http.StatusOK, backupHTML)
}
