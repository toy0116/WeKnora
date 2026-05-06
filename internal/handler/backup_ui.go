package handler

const backupHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>WeKnora · 备份 & 迁移</title>
<style>
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; background: #f5f5f7; color: #1d1d1f; }
  header { background: #1d1d1f; color: #f5f5f7; padding: 16px 32px; display: flex; align-items: center; gap: 16px; }
  header h1 { font-size: 18px; font-weight: 600; }
  header .subtitle { font-size: 13px; color: #86868b; margin-left: 8px; }
  .container { max-width: 960px; margin: 0 auto; padding: 28px 32px; }

  /* status bar */
  .status-bar { background: #fff; border-radius: 12px; padding: 16px 24px;
    margin-bottom: 24px; box-shadow: 0 1px 3px rgba(0,0,0,.08);
    display: flex; gap: 32px; flex-wrap: wrap; align-items: center; }
  .stat-item { display: flex; flex-direction: column; gap: 2px; }
  .stat-label { font-size: 11px; color: #86868b; text-transform: uppercase; letter-spacing: .05em; }
  .stat-value { font-size: 14px; font-weight: 600; }
  .dot { display: inline-block; width: 8px; height: 8px; border-radius: 50%; margin-right: 5px; }
  .dot-ok  { background: #34c759; }
  .dot-err { background: #ff3b30; }

  /* cards */
  .cards { display: grid; grid-template-columns: repeat(3, 1fr); gap: 18px; margin-bottom: 28px; }
  @media(max-width:700px){ .cards { grid-template-columns: 1fr; } }
  .card { background: #fff; border-radius: 14px; padding: 24px;
    box-shadow: 0 1px 3px rgba(0,0,0,.08); display: flex; flex-direction: column; gap: 12px; }
  .card-icon { font-size: 32px; line-height: 1; }
  .card-title { font-size: 16px; font-weight: 700; }
  .card-desc { font-size: 13px; color: #86868b; line-height: 1.5; flex: 1; }
  .card-meta { font-size: 12px; color: #86868b; }
  .card-meta span { font-weight: 600; color: #1d1d1f; }

  /* buttons */
  .btn { display: inline-flex; align-items: center; gap: 6px; padding: 9px 18px;
    border-radius: 9px; border: none; cursor: pointer; font-size: 14px; font-weight: 600;
    transition: opacity .15s, transform .1s; }
  .btn:active { transform: scale(.97); }
  .btn:disabled { opacity: .4; cursor: not-allowed; }
  .btn-primary { background: #007aff; color: #fff; }
  .btn-primary:hover:not(:disabled) { background: #0066d6; }
  .btn-secondary { background: #f5f5f7; color: #1d1d1f; border: 1px solid #e0e0e0; }
  .btn-secondary:hover:not(:disabled) { background: #e8e8ed; }

  /* progress overlay on card */
  .card-progress { display: none; align-items: center; gap: 8px; font-size: 13px; color: #007aff; }
  .card-progress.active { display: flex; }
  .spinner { width: 16px; height: 16px; border: 2px solid #e0e8ff; border-top-color: #007aff;
    border-radius: 50%; animation: spin .7s linear infinite; flex-shrink: 0; }
  @keyframes spin { to { transform: rotate(360deg); } }

  /* guide section */
  .section { background: #fff; border-radius: 12px; padding: 22px 24px;
    box-shadow: 0 1px 3px rgba(0,0,0,.08); margin-bottom: 20px; }
  .section h2 { font-size: 15px; font-weight: 700; margin-bottom: 14px; }
  .steps { display: flex; flex-direction: column; gap: 12px; }
  .step { display: flex; gap: 14px; align-items: flex-start; }
  .step-num { width: 24px; height: 24px; border-radius: 50%; background: #007aff; color: #fff;
    font-size: 12px; font-weight: 700; display: flex; align-items: center; justify-content: center; flex-shrink: 0; }
  .step-body { flex: 1; }
  .step-title { font-size: 14px; font-weight: 600; margin-bottom: 3px; }
  .step-desc { font-size: 13px; color: #86868b; line-height: 1.5; }
  code { font-family: "SF Mono", "Menlo", monospace; font-size: 12px;
    background: #f5f5f7; padding: 2px 6px; border-radius: 4px; color: #007aff; }
  pre { font-family: "SF Mono", "Menlo", monospace; font-size: 12px;
    background: #1d1d1f; color: #d1d1d6; padding: 14px 18px; border-radius: 10px;
    margin-top: 8px; overflow-x: auto; line-height: 1.7; }
  .toast { position: fixed; bottom: 28px; left: 50%; transform: translateX(-50%);
    background: #1d1d1f; color: #fff; padding: 10px 22px; border-radius: 20px;
    font-size: 14px; font-weight: 500; opacity: 0; transition: opacity .2s;
    pointer-events: none; z-index: 999; }
  .toast.show { opacity: 1; }
  .warn { color: #ff9f0a; }
  .ok   { color: #34c759; }
</style>
</head>
<body>

<header>
  <h1>💾 WeKnora 备份 & 迁移</h1>
  <span class="subtitle">数据库 · 文件存储 · 配置导出</span>
</header>

<div class="container">

  <!-- status bar -->
  <div class="status-bar" id="status-bar">
    <div class="stat-item">
      <span class="stat-label">数据库</span>
      <span class="stat-value" id="st-db">–</span>
    </div>
    <div class="stat-item">
      <span class="stat-label">pg_dump</span>
      <span class="stat-value" id="st-pgdump">–</span>
    </div>
    <div class="stat-item">
      <span class="stat-label">文件存储大小</span>
      <span class="stat-value" id="st-size">–</span>
    </div>
    <div class="stat-item">
      <span class="stat-label">存储目录</span>
      <span class="stat-value" id="st-dir" style="font-size:12px;font-weight:400;color:#86868b">–</span>
    </div>
    <button class="btn btn-secondary" style="margin-left:auto" onclick="loadStatus()">刷新</button>
  </div>

  <!-- three export cards -->
  <div class="cards">

    <!-- database -->
    <div class="card">
      <div class="card-icon">🗄️</div>
      <div class="card-title">数据库备份</div>
      <div class="card-desc">
        导出完整 PostgreSQL / SQLite 数据库，包含所有知识块、向量嵌入、会话记录、模型配置等。
      </div>
      <div class="card-meta" id="db-meta">–</div>
      <div class="card-progress" id="db-progress">
        <div class="spinner"></div> 正在导出，请稍候（大库可能需要数分钟）…
      </div>
      <button class="btn btn-primary" id="btn-db" onclick="downloadDB()">⬇ 下载数据库备份</button>
    </div>

    <!-- files -->
    <div class="card">
      <div class="card-icon">📁</div>
      <div class="card-title">文件存储备份</div>
      <div class="card-desc">
        打包 LOCAL_STORAGE_BASE_DIR 下的所有上传文件（PDF、图片等），保留目录结构。
      </div>
      <div class="card-meta" id="files-meta">–</div>
      <div class="card-progress" id="files-progress">
        <div class="spinner"></div> 正在打包归档，请稍候…
      </div>
      <button class="btn btn-primary" id="btn-files" onclick="downloadFiles()">⬇ 下载文件归档</button>
    </div>

    <!-- config -->
    <div class="card">
      <div class="card-icon">⚙️</div>
      <div class="card-title">配置导出</div>
      <div class="card-desc">
        导出运行时配置 JSON，包含数据库坐标、存储路径、环境变量快照（密码等敏感字段自动脱敏）。
      </div>
      <div class="card-meta">JSON · 即时生成 · 无需等待</div>
      <div class="card-progress" id="cfg-progress">
        <div class="spinner"></div> 生成中…
      </div>
      <button class="btn btn-primary" id="btn-cfg" onclick="downloadConfig()">⬇ 下载配置</button>
    </div>

  </div>

  <!-- migration guide -->
  <div class="section">
    <h2>🚀 迁移到新机器 — 操作步骤</h2>
    <div class="steps">
      <div class="step">
        <div class="step-num">1</div>
        <div class="step-body">
          <div class="step-title">在旧机器上全量备份</div>
          <div class="step-desc">
            点击上方三个下载按钮，分别下载数据库、文件、配置。<br>
            也可用命令行脚本一键备份：
          </div>
          <pre>./scripts/backup.sh export --out ~/weknora-backup --host http://127.0.0.1:18080</pre>
        </div>
      </div>
      <div class="step">
        <div class="step-num">2</div>
        <div class="step-body">
          <div class="step-title">在新机器上安装依赖 & 配置环境</div>
          <div class="step-desc">
            参考导出的 <code>weknora-config-*.json</code>，在新机器上设置相同的环境变量（更新 DB_HOST / LOCAL_STORAGE_BASE_DIR 等）。
          </div>
        </div>
      </div>
      <div class="step">
        <div class="step-num">3</div>
        <div class="step-body">
          <div class="step-title">恢复数据库</div>
          <div class="step-desc">使用命令行脚本自动完成 drop → create → restore：</div>
          <pre>DB_HOST=&lt;新机器IP&gt; DB_USER=weknora DB_PASSWORD=xxx DB_NAME=weknora \
./scripts/backup.sh import \
    --db  weknora-db-YYYYMMDD-HHmmss.sql.gz</pre>
        </div>
      </div>
      <div class="step">
        <div class="step-num">4</div>
        <div class="step-body">
          <div class="step-title">恢复文件存储</div>
          <pre>LOCAL_STORAGE_BASE_DIR=/path/to/files \
./scripts/backup.sh import \
    --files weknora-files-YYYYMMDD-HHmmss.tar.gz</pre>
        </div>
      </div>
      <div class="step">
        <div class="step-num">5</div>
        <div class="step-body">
          <div class="step-title">启动 WeKnora 并验证</div>
          <div class="step-desc">
            启动服务后，访问 <code>/health</code> 确认正常，再打开主界面确认知识库数据完整。
          </div>
        </div>
      </div>
    </div>
  </div>

</div>

<div class="toast" id="toast"></div>

<script>
const _base = window.location.pathname.replace(/\/admin\/backup.*$/, '');
const API = _base + '/admin/backup';

function toast(msg, dur = 2800) {
  const el = document.getElementById('toast');
  el.textContent = msg;
  el.classList.add('show');
  setTimeout(() => el.classList.remove('show'), dur);
}

function fmtBytes(b) {
  if (b == null) return '–';
  if (b >= 1024*1024*1024) return (b/1024/1024/1024).toFixed(1) + ' GB';
  if (b >= 1024*1024)      return (b/1024/1024).toFixed(0) + ' MB';
  if (b >= 1024)           return (b/1024).toFixed(0) + ' KB';
  return b + ' B';
}

async function loadStatus() {
  try {
    const r = await fetch(API + '/status');
    const s = await r.json();

    document.getElementById('st-db').innerHTML =
      '<span class="dot dot-ok"></span>' + (s.db_name || '–') +
      ' <span style="color:#86868b;font-weight:400">(' + (s.db_driver||'') + '@' + (s.db_host||'') + ':' + (s.db_port||'') + ')</span>';

    const pgOK = s.pg_dump_ok;
    document.getElementById('st-pgdump').innerHTML =
      pgOK
        ? '<span class="dot dot-ok"></span><span class="ok">可用</span> <span style="color:#86868b;font-weight:400;font-size:12px">' + (s.pg_dump_path||'') + '</span>'
        : '<span class="dot dot-err"></span><span class="warn">未找到</span>';

    document.getElementById('st-size').textContent = fmtBytes(s.storage_size_bytes);
    document.getElementById('st-dir').textContent  = s.storage_dir || '–';

    // update card meta
    document.getElementById('db-meta').innerHTML =
      '<span>' + s.db_driver + '</span> · ' + (s.db_name || '?') +
      (pgOK ? '' : '  <span class="warn">⚠ pg_dump 未找到，导出可能失败</span>');

    document.getElementById('files-meta').innerHTML =
      '当前大小 <span>' + fmtBytes(s.storage_size_bytes) + '</span>' +
      (s.storage_ok ? '' : '  <span class="warn">⚠ 目录不存在</span>');

    // disable DB button if pg_dump missing (SQLite still OK)
    if (s.db_driver === 'postgres' && !pgOK) {
      document.getElementById('btn-db').disabled = true;
      document.getElementById('btn-db').title = 'pg_dump 未找到，请确认 PostgreSQL 客户端已安装';
    }
  } catch(e) {
    document.getElementById('st-db').textContent = '加载失败';
    console.error(e);
  }
}

function triggerDownload(url, progressId, btnId) {
  const progress = document.getElementById(progressId);
  const btn      = document.getElementById(btnId);
  progress.classList.add('active');
  btn.disabled = true;

  // Use a hidden <a> to trigger the browser download manager;
  // the server streams the response so the browser shows native progress.
  const a = document.createElement('a');
  a.href = url;
  a.style.display = 'none';
  document.body.appendChild(a);
  a.click();
  document.body.removeChild(a);

  // Re-enable button after a delay (we can't track download completion)
  setTimeout(() => {
    progress.classList.remove('active');
    btn.disabled = false;
    toast('✅ 下载已开始，请查看浏览器下载列表');
  }, 2000);
}

function downloadDB() {
  triggerDownload(API + '/database', 'db-progress', 'btn-db');
}

function downloadFiles() {
  triggerDownload(API + '/files', 'files-progress', 'btn-files');
}

function downloadConfig() {
  triggerDownload(API + '/config', 'cfg-progress', 'btn-cfg');
}

// init
loadStatus();
</script>
</body>
</html>`
