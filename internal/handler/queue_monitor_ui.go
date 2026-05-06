package handler

const queueMonitorHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>WeKnora · 队列监控</title>
<style>
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; background: #f5f5f7; color: #1d1d1f; }
  header { background: #1d1d1f; color: #f5f5f7; padding: 16px 32px; display: flex; align-items: center; gap: 16px; }
  header h1 { font-size: 18px; font-weight: 600; }
  header .nav-links { display: flex; gap: 4px; margin-left: 24px; }
  header .nav-link { padding: 5px 12px; border-radius: 7px; font-size: 13px; color: #86868b; text-decoration: none; transition: background .15s; }
  header .nav-link:hover { background: rgba(255,255,255,.12); color: #f5f5f7; }
  header .nav-link.active { background: rgba(255,255,255,.18); color: #f5f5f7; font-weight: 600; }
  header .refresh-info { font-size: 12px; color: #86868b; margin-left: auto; }
  .container { max-width: 1200px; margin: 0 auto; padding: 24px 32px; }

  /* summary cards */
  .cards { display: grid; grid-template-columns: repeat(3, 1fr); gap: 16px; margin-bottom: 28px; }
  .card { background: #fff; border-radius: 12px; padding: 20px 24px; box-shadow: 0 1px 3px rgba(0,0,0,.08); }
  .card .label { font-size: 12px; color: #86868b; text-transform: uppercase; letter-spacing: .05em; }
  .card .value { font-size: 32px; font-weight: 700; margin-top: 4px; }
  .card.pending .value { color: #007aff; }
  .card.active  .value { color: #34c759; }
  .card.failed  .value { color: #ff3b30; }

  /* progress bars */
  .section { background: #fff; border-radius: 12px; padding: 20px 24px; margin-bottom: 20px; box-shadow: 0 1px 3px rgba(0,0,0,.08); }
  .section h2 { font-size: 15px; font-weight: 600; margin-bottom: 16px; }
  .task-row { margin-bottom: 14px; }
  .task-row .task-label { display: flex; justify-content: space-between; font-size: 13px; margin-bottom: 4px; }
  .task-row .task-label .name { font-weight: 500; }
  .task-row .task-label .count { color: #86868b; }
  .bar-track { background: #f5f5f7; border-radius: 4px; height: 8px; overflow: hidden; }
  .bar-fill  { height: 100%; border-radius: 4px; background: #007aff; transition: width .4s ease; }
  .bar-fill.done { background: #34c759; }

  /* tabs */
  .tabs { display: flex; gap: 2px; margin-bottom: 20px; }
  .tab { padding: 8px 18px; border-radius: 8px; cursor: pointer; font-size: 14px; font-weight: 500; color: #86868b; transition: all .15s; }
  .tab.active { background: #007aff; color: #fff; }

  /* table */
  table { width: 100%; border-collapse: collapse; font-size: 13px; }
  th { text-align: left; padding: 8px 12px; color: #86868b; font-weight: 500; border-bottom: 1px solid #f0f0f0; }
  td { padding: 10px 12px; border-bottom: 1px solid #f8f8f8; vertical-align: middle; }
  tr:last-child td { border-bottom: none; }
  tr:hover td { background: #fafafa; }
  .badge { display: inline-block; padding: 2px 8px; border-radius: 12px; font-size: 11px; font-weight: 600; }
  .badge.done     { background: #d1fae5; color: #065f46; }
  .badge.partial  { background: #fef3c7; color: #92400e; }
  .badge.pending  { background: #dbeafe; color: #1e40af; }
  .badge.failed   { background: #fee2e2; color: #991b1b; }
  .mono { font-family: "SF Mono", monospace; font-size: 11px; color: #86868b; }

  /* action button */
  .btn { padding: 6px 14px; border-radius: 8px; border: none; cursor: pointer; font-size: 13px; font-weight: 500; }
  .btn-primary { background: #007aff; color: #fff; }
  .btn-primary:hover { background: #0066d6; }
  .btn-danger  { background: #ff3b30; color: #fff; }
  .btn-danger:hover  { background: #d63126; }
  .btn:disabled { opacity: .4; cursor: not-allowed; }
  .action-bar { display: flex; gap: 10px; margin-bottom: 16px; align-items: center; }
  .action-bar .note { font-size: 12px; color: #86868b; }

  .empty { text-align: center; padding: 40px; color: #86868b; font-size: 14px; }
  .spinner { display: inline-block; width: 16px; height: 16px; border: 2px solid #f0f0f0; border-top-color: #007aff; border-radius: 50%; animation: spin .6s linear infinite; vertical-align: middle; margin-right: 6px; }
  @keyframes spin { to { transform: rotate(360deg); } }
</style>
</head>
<body>
<header>
  <h1>🛠 WeKnora 管理</h1>
  <nav class="nav-links">
    <a class="nav-link active" href="queue">🗂 队列监控</a>
    <a class="nav-link" href="backup">💾 备份 & 迁移</a>
  </nav>
  <span class="refresh-info" id="refresh-info">加载中…</span>
</header>

<div class="container">

  <!-- summary cards -->
  <div class="cards">
    <div class="card pending"><div class="label">待处理</div><div class="value" id="total-pending">–</div></div>
    <div class="card active"><div class="label">处理中</div><div class="value" id="total-active">–</div></div>
    <div class="card failed"><div class="label">已失败</div><div class="value" id="total-failed">–</div></div>
  </div>

  <!-- progress by task type -->
  <div class="section">
    <h2>各任务类型进度</h2>
    <div id="type-bars"><div class="spinner"></div> 加载中…</div>
  </div>

  <!-- tabs: documents / failed-docs / failures -->
  <div class="tabs">
    <div class="tab active" onclick="switchTab('docs')">文档进度</div>
    <div class="tab" onclick="switchTab('failed-docs')" id="tab-btn-failed-docs">解析失败文档</div>
    <div class="tab" onclick="switchTab('failures')">失败任务</div>
  </div>

  <div id="tab-docs" class="section">
    <h2>文档处理进度</h2>
    <div id="docs-table"><div class="spinner"></div> 加载中…</div>
  </div>

  <div id="tab-failed-docs" class="section" style="display:none">
    <h2>解析失败文档</h2>
    <div class="action-bar">
      <button class="btn btn-primary" onclick="retryAllFailedDocs()">全部重新解析</button>
      <span class="note">无需重新上传 · 直接重新触发解析 pipeline</span>
    </div>
    <div id="failed-docs-table"><div class="spinner"></div> 加载中…</div>
  </div>

  <div id="tab-failures" class="section" style="display:none">
    <h2>失败任务</h2>
    <div class="action-bar">
      <button class="btn btn-primary" onclick="reenqueueAll()">全部重入队</button>
      <button class="btn btn-primary" id="btn-reenqueue-selected" onclick="reenqueueSelected()" disabled>重入队选中</button>
      <span class="note" id="selected-count"></span>
    </div>
    <div id="failures-table"><div class="spinner"></div> 加载中…</div>
  </div>

</div>

<script>
// Resolve API base relative to the current page URL so it works whether
// accessed directly (port 3120) or via LocalHub proxy (/app/weknora/admin/queue).
const _base = window.location.pathname.replace(/\/admin\/queue.*$/, '');
const API = _base + '/admin/queue/api';
let selectedTaskIDs = new Set();

// ── tab switch ────────────────────────────────────────────────────────────────
function switchTab(tab) {
  document.querySelectorAll('.tab').forEach(t => t.classList.remove('active'));
  event.target.classList.add('active');
  document.getElementById('tab-docs').style.display        = tab === 'docs'        ? '' : 'none';
  document.getElementById('tab-failed-docs').style.display = tab === 'failed-docs' ? '' : 'none';
  document.getElementById('tab-failures').style.display    = tab === 'failures'    ? '' : 'none';
  if (tab === 'failures')    loadFailures();
  if (tab === 'failed-docs') loadFailedDocs();
}

// ── stats ─────────────────────────────────────────────────────────────────────
async function loadStats() {
  try {
    const r = await fetch(API + '/stats');
    const d = await r.json();
    document.getElementById('total-pending').textContent = d.total_pending ?? '–';
    document.getElementById('total-active').textContent  = d.total_active  ?? '–';
    document.getElementById('total-failed').textContent  = d.total_failed  ?? '–';

    const ts = d.refreshed_at ? new Date(d.refreshed_at).toLocaleTimeString('zh-CN') : '–';
    document.getElementById('refresh-info').textContent = '数据更新：' + ts + ' · 每30s自动刷新';

    const total = (d.total_pending ?? 0) + (d.total_active ?? 0);
    const bars = (d.by_type || []).map(t => {
      const done = total > 0 ? Math.max(0, 1 - (t.pending + t.active) / Math.max(1, total)) : 1;
      const pct = Math.round(done * 100);
      const running = t.pending + t.active;
      return ` + "`" + `
        <div class="task-row">
          <div class="task-label">
            <span class="name">${t.type}</span>
            <span class="count">待处理 ${t.pending} · 进行中 ${t.active} · 重试 ${t.retry} · 失败 ${t.archived}</span>
          </div>
          <div class="bar-track">
            <div class="bar-fill ${running === 0 ? 'done' : ''}" style="width:${running === 0 ? 100 : Math.max(4, 100 - Math.round((t.pending+t.active)/Math.max(1,total)*100))}%"></div>
          </div>
        </div>` + "`" + `;
    }).join('');
    document.getElementById('type-bars').innerHTML = bars || '<div class="empty">暂无任务</div>';
  } catch(e) {
    document.getElementById('type-bars').innerHTML = '<div class="empty">加载失败：' + e + '</div>';
  }
}

// ── documents ─────────────────────────────────────────────────────────────────
async function loadDocs() {
  try {
    const r = await fetch(API + '/documents');
    const d = await r.json();
    const docs = d.documents || [];
    if (!docs.length) {
      document.getElementById('docs-table').innerHTML = '<div class="empty">暂无文档</div>';
      return;
    }
    const rows = docs.map(doc => {
      const extractDone = doc.total_chunks - doc.pending_extract;
      const extractPct = doc.total_chunks > 0 ? Math.round(extractDone / doc.total_chunks * 100) : 100;
      const badge = doc.parse_status === 'completed' && doc.pending_extract === 0
        ? '<span class="badge done">完成</span>'
        : doc.pending_extract > 0
          ? '<span class="badge pending">图谱处理中</span>'
          : '<span class="badge partial">待完成</span>';
      return ` + "`" + `<tr>
        <td>${doc.title || doc.knowledge_id}</td>
        <td style="text-align:right">${doc.total_chunks}</td>
        <td>
          <div style="display:flex;align-items:center;gap:8px">
            <div class="bar-track" style="width:80px;flex-shrink:0">
              <div class="bar-fill ${extractPct===100?'done':''}" style="width:${extractPct}%"></div>
            </div>
            <span style="font-size:12px;color:#86868b">${extractDone}/${doc.total_chunks}</span>
          </div>
        </td>
        <td style="text-align:right">${doc.wiki_pages}</td>
        <td>${badge}</td>
        <td class="mono">${doc.updated_at}</td>
      </tr>` + "`" + `;
    }).join('');
    document.getElementById('docs-table').innerHTML = ` + "`" + `
      <table>
        <thead><tr>
          <th>文档</th><th style="text-align:right">Chunks</th>
          <th>实体提取</th><th style="text-align:right">Wiki页</th>
          <th>状态</th><th>更新时间</th>
        </tr></thead>
        <tbody>${rows}</tbody>
      </table>` + "`" + `;
  } catch(e) {
    document.getElementById('docs-table').innerHTML = '<div class="empty">加载失败：' + e + '</div>';
  }
}

// ── failures ──────────────────────────────────────────────────────────────────
async function loadFailures() {
  try {
    const r = await fetch(API + '/failures');
    const d = await r.json();
    const items = d.failures || [];
    if (!items.length) {
      document.getElementById('failures-table').innerHTML = '<div class="empty">✅ 没有失败任务</div>';
      return;
    }
    const rows = items.map(f => ` + "`" + `<tr>
      <td><input type="checkbox" onchange="toggleSelect('${f.task_id}', this.checked)"></td>
      <td class="mono">${(f.chunk_id||f.task_id).slice(0,8)}</td>
      <td>${f.type}</td>
      <td>${f.queue}</td>
      <td style="color:#ff3b30;font-size:12px">${f.error_msg || '(无错误信息)'}</td>
      <td style="text-align:right">${f.retry_count}</td>
      <td><button class="btn btn-primary" onclick="reenqueueOne('${f.task_id}')">重入队</button></td>
    </tr>` + "`" + `).join('');
    document.getElementById('failures-table').innerHTML = ` + "`" + `
      <table>
        <thead><tr>
          <th></th><th>ID</th><th>类型</th><th>队列</th><th>错误</th><th style="text-align:right">重试次数</th><th></th>
        </tr></thead>
        <tbody>${rows}</tbody>
      </table>` + "`" + `;
  } catch(e) {
    document.getElementById('failures-table').innerHTML = '<div class="empty">加载失败：' + e + '</div>';
  }
}

function toggleSelect(id, checked) {
  if (checked) selectedTaskIDs.add(id); else selectedTaskIDs.delete(id);
  document.getElementById('btn-reenqueue-selected').disabled = selectedTaskIDs.size === 0;
  document.getElementById('selected-count').textContent = selectedTaskIDs.size > 0 ? selectedTaskIDs.size + ' 个已选' : '';
}

async function reenqueueAll() {
  if (!confirm('将所有失败任务重新入队？')) return;
  await doReenqueue([]);
}
async function reenqueueSelected() {
  await doReenqueue([...selectedTaskIDs]);
}
async function reenqueueOne(id) {
  await doReenqueue([id]);
}

async function doReenqueue(ids) {
  try {
    const r = await fetch(API + '/reenqueue', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ task_ids: ids }),
    });
    const d = await r.json();
    alert('已重入队 ' + d.requeued + ' 个任务');
    selectedTaskIDs.clear();
    loadFailures();
    loadStats();
  } catch(e) {
    alert('操作失败：' + e);
  }
}

// ── failed docs (parse_status = 'failed') ────────────────────────────────────
async function loadFailedDocs() {
  try {
    const r = await fetch(API + '/failed-docs');
    const d = await r.json();
    const docs = d.failed_docs || [];

    // Update tab badge
    const btn = document.getElementById('tab-btn-failed-docs');
    if (btn) btn.textContent = docs.length > 0 ? ` + "`" + `解析失败文档 (${docs.length})` + "`" + ` : '解析失败文档';

    if (!docs.length) {
      document.getElementById('failed-docs-table').innerHTML = '<div class="empty">✅ 没有解析失败的文档</div>';
      return;
    }
    const rows = docs.map(doc => ` + "`" + `<tr>
      <td>${doc.title || doc.knowledge_id}</td>
      <td><span class="badge failed">失败</span></td>
      <td style="color:#ff3b30;font-size:12px;max-width:320px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap" title="${doc.error_msg}">${doc.error_msg || '(无错误信息)'}</td>
      <td class="mono">${doc.updated_at}</td>
      <td>
        <button class="btn btn-primary" onclick="retryDoc('${doc.knowledge_id}', this)">重新解析</button>
      </td>
    </tr>` + "`" + `).join('');
    document.getElementById('failed-docs-table').innerHTML = ` + "`" + `
      <table>
        <thead><tr>
          <th>文档</th><th>状态</th><th>错误信息</th><th>更新时间</th><th></th>
        </tr></thead>
        <tbody>${rows}</tbody>
      </table>` + "`" + `;
  } catch(e) {
    document.getElementById('failed-docs-table').innerHTML = '<div class="empty">加载失败：' + e + '</div>';
  }
}

async function retryDoc(id, btn) {
  btn.disabled = true;
  btn.textContent = '提交中…';
  try {
    const r = await fetch(API + '/retry-doc/' + id, { method: 'POST' });
    const d = await r.json();
    if (d.ok) {
      btn.textContent = '✅ 已重新解析';
      btn.style.background = '#34c759';
    } else {
      btn.textContent = '❌ 失败';
      btn.disabled = false;
      alert('重新解析失败：' + (d.error || JSON.stringify(d)));
    }
  } catch(e) {
    btn.textContent = '❌ 错误';
    btn.disabled = false;
    alert('网络错误：' + e);
  }
}

async function retryAllFailedDocs() {
  if (!confirm('将所有解析失败的文档重新解析？（无需重新上传）')) return;
  try {
    const r = await fetch(API + '/retry-all-failed-docs', { method: 'POST' });
    const d = await r.json();
    const msg = ` + "`" + `已重新提交 ${d.queued} 个文档${d.errors?.length ? '\n失败: ' + d.errors.join('\n') : ''}` + "`" + `;
    alert(msg);
    loadFailedDocs();
    loadStats();
  } catch(e) {
    alert('操作失败：' + e);
  }
}

// ── init + auto-refresh ───────────────────────────────────────────────────────
function refreshAll() {
  loadStats();
  loadDocs();
  // 静默刷新失败文档 tab badge（不管当前 tab 是否激活）
  fetch(API + '/failed-docs').then(r => r.json()).then(d => {
    const btn = document.getElementById('tab-btn-failed-docs');
    const n = (d.failed_docs || []).length;
    if (btn) btn.textContent = n > 0 ? ` + "`" + `解析失败文档 (${n})` + "`" + ` : '解析失败文档';
  }).catch(() => {});
}
refreshAll();
setInterval(refreshAll, 30000);
</script>
</body>
</html>`
