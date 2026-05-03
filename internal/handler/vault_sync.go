package handler

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// VaultSyncHandler syncs WeKnora knowledge to an Obsidian vault (one-way).
type VaultSyncHandler struct {
	wikiService      interfaces.WikiPageService
	kbService        interfaces.KnowledgeBaseService
	knowledgeService interfaces.KnowledgeService
	chunkRepo        interfaces.ChunkRepository
	fileService      interfaces.FileService
}

func NewVaultSyncHandler(
	wikiService interfaces.WikiPageService,
	kbService interfaces.KnowledgeBaseService,
	knowledgeService interfaces.KnowledgeService,
	chunkRepo interfaces.ChunkRepository,
	fileService interfaces.FileService,
) *VaultSyncHandler {
	return &VaultSyncHandler{
		wikiService:      wikiService,
		kbService:        kbService,
		knowledgeService: knowledgeService,
		chunkRepo:        chunkRepo,
		fileService:      fileService,
	}
}

type VaultSyncResult struct {
	VaultPath     string   `json:"vault_path"`
	WikiPages     int      `json:"wiki_pages"`
	Docs          int      `json:"docs"`
	Images        int      `json:"images"`
	Errors        []string `json:"errors,omitempty"`
}

// SyncToVault godoc
// POST /api/v1/knowledge-bases/:id/vault-sync
func (h *VaultSyncHandler) SyncToVault(c *gin.Context) {
	ctx := c.Request.Context()
	kbID := c.Param("id")
	tenantID := c.GetUint64(types.TenantIDContextKey.String())

	vaultPath := expandHome(os.Getenv("VAULT_PATH"))
	if vaultPath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "VAULT_PATH not configured"})
		return
	}

	result := &VaultSyncResult{VaultPath: vaultPath}
	addErr := func(msg string) { result.Errors = append(result.Errors, msg) }

	weknoraDir := filepath.Join(vaultPath, "weknora")
	attachDir := filepath.Join(weknoraDir, "attachments")
	for _, dir := range []string{
		filepath.Join(weknoraDir, "docs"),
		filepath.Join(weknoraDir, "summary"),
		filepath.Join(weknoraDir, "entity"),
		filepath.Join(weknoraDir, "concept"),
		attachDir,
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "cannot create vault dirs: " + err.Error()})
			return
		}
	}

	// ── 1. Wiki pages ────────────────────────────────────────────────────────
	pages, err := h.wikiService.ListAllPages(ctx, kbID)
	if err != nil {
		logger.Errorf(ctx, "vault-sync: list wiki pages: %v", err)
		addErr("list wiki pages: " + err.Error())
	}
	for _, p := range pages {
		if p.PageType == "log" {
			continue
		}
		content := convertWikilinks(p.Content)
		// Slug may include the page-type prefix (e.g. "entity/at-commands"); strip it.
		slugBase := p.Slug
		if i := strings.LastIndex(p.Slug, "/"); i >= 0 {
			slugBase = p.Slug[i+1:]
		}
		var dest string
		switch p.PageType {
		case "index":
			dest = filepath.Join(weknoraDir, "Index.md")
		case "summary":
			// Use slug as filename so [[slug]] wikilinks in Index.md resolve correctly.
			dest = filepath.Join(weknoraDir, "summary", slugBase+".md")
			content = ensureH1(p.Title, content)
		case "entity":
			dest = filepath.Join(weknoraDir, "entity", slugBase+".md")
			content = ensureH1(p.Title, content)
		case "concept":
			dest = filepath.Join(weknoraDir, "concept", slugBase+".md")
			content = ensureH1(p.Title, content)
		default:
			dest = filepath.Join(weknoraDir, safeFilename(p.PageType)+"_"+slugBase+".md")
			content = ensureH1(p.Title, content)
		}
		if err := os.WriteFile(dest, []byte(content), 0o644); err != nil {
			addErr(fmt.Sprintf("write wiki %s: %v", p.Slug, err))
		} else {
			result.WikiPages++
		}
	}

	// ── 2. PDF docs (text chunks) + images ──────────────────────────────────
	knowledges, err := h.knowledgeService.ListKnowledgeByKnowledgeBaseID(ctx, kbID)
	if err != nil {
		addErr("list knowledge: " + err.Error())
	}
	for _, k := range knowledges {
		if k.ParseStatus != "completed" {
			continue
		}
		chunks, err := h.chunkRepo.ListChunksByKnowledgeID(ctx, tenantID, k.ID)
		if err != nil {
			addErr(fmt.Sprintf("chunks for %s: %v", k.Title, err))
			continue
		}

		var sb strings.Builder
		for _, ch := range chunks {
			sb.WriteString(ch.Content)
			sb.WriteString("\n\n")
		}
		raw := sb.String()

		// resolve local:// images → copy to attachments, rewrite to ![[name]]
		raw, copied := h.resolveImages(ctx, raw, attachDir)
		result.Images += copied

		docName := safeFilename(strings.TrimSuffix(k.FileName, filepath.Ext(k.FileName)))
		dest := filepath.Join(weknoraDir, "docs", docName+".md")
		raw = ensureH1(k.Title, raw)
		if err := os.WriteFile(dest, []byte(raw), 0o644); err != nil {
			addErr(fmt.Sprintf("write doc %s: %v", k.Title, err))
		} else {
			result.Docs++
		}
	}

	c.JSON(http.StatusOK, gin.H{"ok": 1, "data": result})
}

// convertWikilinks rewrites WeKnora internal links to plain Obsidian wikilinks.
// [[entity/rs-232|RS-232]]  →  [[RS-232]]
// [[concept/iot|IoT]]       →  [[IoT]]
// [[summary/slug|Title]]    →  [[Title]]
// [[slug]]                  →  [[slug]]  (unchanged)
var wikilinkRe = regexp.MustCompile(`\[\[(?:entity|concept|summary|index)/[^\]|]*\|([^\]]+)\]\]`)
var wikilinkNoAlias = regexp.MustCompile(`\[\[(?:entity|concept|summary|index)/([^\]|]+)\]\]`)

func convertWikilinks(content string) string {
	content = wikilinkRe.ReplaceAllString(content, "[[$1]]")
	content = wikilinkNoAlias.ReplaceAllStringFunc(content, func(m string) string {
		// extract the last path segment as the display name
		inner := strings.TrimPrefix(strings.TrimSuffix(m, "]]"), "[[")
		parts := strings.Split(inner, "/")
		return "[[" + parts[len(parts)-1] + "]]"
	})
	return content
}

// resolveImages finds local:// image refs, copies the files, returns updated content + count.
var localImgRe = regexp.MustCompile(`!\[([^\]]*)\]\(local://([^\)]+)\)`)

func (h *VaultSyncHandler) resolveImages(ctx interface{ Value(any) any }, content, attachDir string) (string, int) {
	copied := 0
	result := localImgRe.ReplaceAllStringFunc(content, func(m string) string {
		sub := localImgRe.FindStringSubmatch(m)
		if len(sub) < 3 {
			return m
		}
		alt := sub[1]
		relPath := sub[2] // e.g. "10000/exports/xxx.jpg"

		storageBase := os.Getenv("LOCAL_STORAGE_BASE_DIR")
		if storageBase == "" {
			storageBase = "/data/files"
		}
		srcPath := filepath.Join(storageBase, filepath.FromSlash(relPath))
		fname := filepath.Base(srcPath)
		dstPath := filepath.Join(attachDir, fname)

		if err := copyFile(srcPath, dstPath); err == nil {
			copied++
		}
		_ = alt
		return "![[" + fname + "]]"
	})
	return result, copied
}

func copyFile(src, dst string) error {
	if _, err := os.Stat(dst); err == nil {
		return nil // already exists
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// ensureH1 prepends "# title\n\n" only if the content doesn't already start with a heading.
func ensureH1(title, content string) string {
	if strings.HasPrefix(strings.TrimSpace(content), "#") {
		return content
	}
	return "# " + title + "\n\n" + content
}

func safeFilename(s string) string {
	// remove characters forbidden in most filesystems
	r := strings.NewReplacer("/", "-", "\\", "-", ":", "-", "*", "-",
		"?", "", "\"", "", "<", "", ">", "", "|", "-")
	return strings.TrimSpace(r.Replace(s))
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[2:])
	}
	return p
}
