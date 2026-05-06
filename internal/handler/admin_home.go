package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// ServeAdminHome serves the admin landing page at GET /admin.
func ServeAdminHome(c *gin.Context) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.String(http.StatusOK, adminHomeHTML)
}

// adminHomeHTML is the landing page served at GET /admin.
// It links to all admin sub-tools so operators always have a single
// known entry point regardless of which tool they need.
const adminHomeHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>WeKnora · 管理控制台</title>
<style>
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
         background: #f5f5f7; color: #1d1d1f; min-height: 100vh;
         display: flex; flex-direction: column; }
  header { background: #1d1d1f; color: #f5f5f7; padding: 18px 36px; }
  header h1 { font-size: 20px; font-weight: 700; letter-spacing: -.3px; }
  header p  { font-size: 13px; color: #86868b; margin-top: 3px; }
  main { flex: 1; max-width: 760px; margin: 48px auto; padding: 0 32px; width: 100%; }
  h2 { font-size: 13px; font-weight: 600; color: #86868b; text-transform: uppercase;
       letter-spacing: .06em; margin-bottom: 14px; }
  .cards { display: grid; grid-template-columns: 1fr 1fr; gap: 16px; margin-bottom: 40px; }
  @media(max-width:560px){ .cards { grid-template-columns: 1fr; } }
  .card { background: #fff; border-radius: 14px; padding: 22px 24px;
          box-shadow: 0 1px 4px rgba(0,0,0,.08);
          text-decoration: none; color: inherit;
          display: flex; align-items: flex-start; gap: 16px;
          transition: box-shadow .15s, transform .12s; }
  .card:hover { box-shadow: 0 4px 14px rgba(0,0,0,.12); transform: translateY(-1px); }
  .card-icon { font-size: 30px; line-height: 1; flex-shrink: 0; margin-top: 2px; }
  .card-body {}
  .card-title { font-size: 15px; font-weight: 700; margin-bottom: 5px; }
  .card-desc  { font-size: 13px; color: #86868b; line-height: 1.5; }
  footer { text-align: center; padding: 20px; font-size: 12px; color: #c7c7cc; }
</style>
</head>
<body>
<header>
  <h1>🛠 WeKnora 管理控制台</h1>
  <p>本地管理工具 · 仅限 127.0.0.1 访问 · 无需登录</p>
</header>
<main>
  <h2>工具</h2>
  <div class="cards">
    <a class="card" id="link-queue">
      <div class="card-icon">🗂</div>
      <div class="card-body">
        <div class="card-title">队列监控</div>
        <div class="card-desc">查看待处理任务、文档解析进度、失败任务重入队</div>
      </div>
    </a>
    <a class="card" id="link-backup">
      <div class="card-icon">💾</div>
      <div class="card-body">
        <div class="card-title">备份 & 迁移</div>
        <div class="card-desc">导出数据库、文件存储、配置；跨机器迁移操作指南</div>
      </div>
    </a>
  </div>
</main>
<footer>WeKnora Admin · 本地服务</footer>
<script>
const _base = window.location.pathname.replace(/\/admin.*$/, '');
document.getElementById('link-queue').href  = _base + '/admin/queue';
document.getElementById('link-backup').href = _base + '/admin/backup';
</script>
</body>
</html>`
