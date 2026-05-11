# WeKnora 维护记录

> 记录各阶段的功能开发、缺陷修复和架构调整，按时间倒序排列。

---

## 2026-05-11 — RAG 幻觉系统性修复 + 实体别名字典

### 背景

用户在使用过程中发现三类 RAG 生成错误，均与"实体-事实归属"相关：

1. **横向污染**：EBDS（客户）的事实被归给 Airicom（竞品），原因是两个来源的 chunk 共享同一 context window。
2. **竖向替换**（Query-anchor override）：用户查询"鲁邦通产品"，KB 命中 Milesight EG71 的文档，LLM 将 Milesight 的规格写成鲁邦通的产品。
3. **生成任务前提污染**：用户要求"生成鲁邦通 EG71 Pitch"，LLM 把用户 prompt 中的"鲁邦通 EG71"当作 ground truth，并用 KB 里 Milesight EG71 的规格填充，输出貌似完整实则品牌张冠李戴的文档。

### 修复内容

#### fix(rag): 横向污染 `e3d75a45`
- **`into_chat_message.go`**：`buildContextAttributes()` 为每个 `<context>` 标签注入来源属性：
  - KB 文档：`source_type="knowledge_base" source_doc="文档名"`
  - 网络搜索：`source_type="web_search" source_title="..." source_url="..."`
- **`system_prompt.yaml`**：新增"CRITICAL: Entity-Fact Binding"规则块（Rules 1–5），强制 LLM 以 chunk 为边界隔离实体事实归属。

#### fix(rag): 竖向替换 — Rule 6 `736acafa`
- **`system_prompt.yaml`**：Rule 6 — Query-anchor override prevention
  > "检索到的文档包含 [X] 的信息，而非 [Y] — 两者是不同实体，相关规格不可互换。"

#### fix(retrieval): 跨语言检索失效 `755af983`
- **`config/prompt_templates/rewrite.yaml`**：在 query rewrite 系统 prompt 中加入 ENTITY ALIAS EXPANSION 规则，LLM 改写时自动补全等价语言形式（`鲁邦通` → `鲁邦通 (Robustel)`）。

#### feat(retrieval): 实体别名字典 `7414756b`
- **`config/entity_aliases.yaml`**（新）：17 个别名组，覆盖鲁邦通生态相关公司、主流工业品牌及技术术语（LoRaWAN、IoT、楼控等）。
- **`internal/config/config.go`**：新增 `EntityAliasConfig` 结构体及 `Build()` / `Expand()` / `DetectGroups()` 方法；`LoadConfig()` 启动时自动加载 `entity_aliases.yaml` 并热建索引。
- **`internal/application/service/chat_pipeline/query_expansion.go`**：`expandQueries()` 末尾调用 `EntityAliases.Expand()` 实现确定性（非 LLM）跨语言 recall 扩展，扩展结果写入 pipeline info 日志。

#### feat(ui): 实体别名 Web 配置页 `fede88ad`
- **后端**：
  - `internal/handler/entity_aliases.go`（新）：`GET /api/v1/system/entity-aliases` + `PUT /api/v1/system/entity-aliases`，写磁盘 YAML + 热更新内存索引，无需重启。
  - `internal/router/router.go`：注册路由。
  - `internal/container/container.go`：dig 注入 `NewEntityAliasHandler`。
  - `internal/config/config.go`：补充 `ConfigDir` 字段，供 handler 定位写入路径。
- **前端**：
  - `frontend/src/api/entity-aliases.ts`（新）：`getEntityAliases()` / `updateEntityAliases()`。
  - `frontend/src/views/settings/EntityAliasSettings.vue`（新）：组列表 + 可关闭 tag + 内联输入 + 保存按钮。
  - `frontend/src/views/settings/Settings.vue`：添加"Entity Aliases"导航项及对应内容区。

#### fix(rag): 生成任务防护 `2b892db7`
- **`internal/application/service/chat_pipeline/entity_mismatch_tag.go`**（新）：
  - `tagEntityMismatches()` 在 INTO_CHAT_MESSAGE 前运行。
  - 用 `DetectGroups()` 对比 query 锚定实体与每个 KB chunk 的实体。
  - 不匹配的 chunk 打 `Metadata["entity_mismatch"]="true"` + `Metadata["entity_owner"]="X"`。
- **`into_chat_message.go`**：
  - `PluginIntoChatMessage` 接收 `*config.Config`（dig 自动注入）。
  - `buildContextAttributes()` 将 mismatch 标输出为 XML 属性：`entity_owner="Milesight" entity_mismatch="true"`。
- **`system_prompt.yaml`**：Rule 7 — Generation Task Guard：
  - chunk 带 `entity_mismatch="true"` → LLM 插入 ⚠️ 竞品参考警告。
  - 所有相关 chunk 全部 mismatch → LLM 拒绝伪造，要求用户上传真实产品文档。

---

## 2026-05-10 — Session 活跃时间修复

#### fix(web-qa): `c5acd0a8`
- Web QA 每轮都更新 session `updated_at`（之前只有首轮更新）。

#### fix(im): `663d564d`
- IM 消息每条到达时更新 session `updated_at`，避免会话排序错误。

---

## 2026-05 上旬 — 解析器与摄入管线重构

#### feat/fix(parser): PDFHybridParser `3651439f` `498294a1` `fec5a0c7` `4266a58e`
- 自适应路径：纯文字 PDF 走文本提取，图片密集型 PDF 同时做页面渲染。
- 文本提取与页面渲染并发执行（`perf`）。
- 总失败时 Go 侧回退到内置引擎（`fix`）。

#### feat(parser): Office 格式混合渲染 `9a71264d`
- `DocxHybridParser` + `PptxHybridParser`：文本 + 图片并行提取。

#### feat(ingest): 大文件路由 `6cb5b758` `d8418467`
- 文件 ≥ 5 MB 或页数超阈值自动路由到 `doc_large` 队列，避免 MinerU 超时。
- 双阈值（文件大小 OR 页数）决定 MinerU 使用与否。

#### feat(kb-ui): 批量移动 `51a95a57`
- 知识库 UI 支持批量选中后移动到其他知识库；修复列表视图的移动逻辑。

---

## 2026-05 上旬 — Agent 与 IM 稳定性

#### fix(agent): 多项修复 `93c3b494` `4239fe0b` `c2b7113c` `6765d46c` `4a119642`
- 消除 LLM 空响应重试时的 context-canceled 错误。
- 检测并重试 planning-artifact 类型响应；修复 nudge 的数据合成逻辑。
- handleMessageStream teardown 前等待 agent goroutine 结束。
- 长 IM session 的主动历史压缩（proactive consolidation）。

#### feat(knowledge): 文档版本检测 `258eddc5`
- 上传文档时 LLM 检测是否为已有文档的新版本，重复上传时给出警告。

---

## 2026-05 上旬 — Wiki 维护任务修复

#### fix(wiki): 多项稳定性修复 `5834a339` `8be5a919` `1b5022f4`
- asynq task context 过期时防止队列卡死。
- requeueFailedOps 后调度 follow-up 任务确保 retry 继续推进。
- 综合修复：retry 耗尽、LLM 超时、RPush ctx 泄漏、reconciliation 逻辑。

---

## 维护约定

| 项目 | 说明 |
|------|------|
| 实体别名更新 | 设置页 → Entity Aliases，或直接编辑 `config/entity_aliases.yaml` 后重启 |
| 系统 prompt 调优 | `config/prompt_templates/system_prompt.yaml` 和 `rewrite.yaml` |
| RAG pipeline 调试 | pipeline info 日志搜索 `EntityMismatch` / `alias_expansion` stage |
| 重启服务 | LocalHub → weknora stop/start，或 `launchctl` 重载 |
