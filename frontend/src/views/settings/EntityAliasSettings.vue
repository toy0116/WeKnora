<template>
  <div class="entity-alias-settings">
    <div class="section-header">
      <h2>Entity Aliases</h2>
      <p class="section-description">
        <strong>声明追踪的品牌组</strong> — 系统启动时会自动从 wiki 实体页合并完整产品目录与别名，
        此页面无需列出所有 SKU。一般只在以下场景才需要操作：
      </p>
      <ul class="section-actions">
        <li><strong>新追踪品牌</strong>：新增组，填写品牌名（如 "Sierra Wireless"）+ 中文别名</li>
        <li><strong>停止追踪</strong>：删除整个组</li>
        <li><strong>归属修复</strong>：当 wiki 自动归属错误时，在此手工增删 override</li>
      </ul>
      <p class="section-description-sub">
        <strong>别名</strong>（"鲁邦通" ↔ "Robustel"）参与检索的查询扩展。
        <strong>产品型号</strong>用于检测 "claim 品牌 X 但提到属于品牌 Y 的产品" 这类归属冲突，
        防止生成式任务把竞品规格当自家产品输出。<u>产品型号不会进入 BM25 查询扩展。</u>
      </p>
      <p class="section-description-note">
        此处保存的内容写入 <code>entity_aliases.yaml</code>。wiki 自动合并的产品（运行时 180+ Robustel SKU、150+ Milesight SKU）<strong>不会出现在此 UI</strong>，但参与所有 retrieval 与归属冲突检测。
      </p>
    </div>

    <!-- Loading -->
    <div v-if="loading" class="loading-state">
      <t-loading size="small" />
      <span>加载中…</span>
    </div>

    <template v-else>
      <!-- Groups -->
      <div class="groups-list">
        <div
          v-for="(group, gi) in groups"
          :key="gi"
          class="alias-group"
        >
          <div class="group-header">
            <span class="group-index">组 {{ gi + 1 }}</span>
            <t-button
              theme="danger"
              variant="text"
              size="small"
              @click="removeGroup(gi)"
            >
              <template #icon><t-icon name="delete" /></template>
              删除组
            </t-button>
          </div>

          <!-- Forms — cross-lingual aliases (BM25/vector expansion) -->
          <div class="field-section">
            <div class="field-label">
              <span class="field-name">别名</span>
              <span class="field-hint">参与检索扩展（"鲁邦通" ↔ "Robustel"）</span>
            </div>
            <div class="forms-tags">
              <t-tag
                v-for="(form, fi) in group.forms"
                :key="`form-${fi}`"
                closable
                theme="primary"
                variant="light"
                class="form-tag"
                @close="removeForm(gi, fi)"
              >
                {{ form }}
              </t-tag>

              <!-- Inline add input -->
              <div v-if="addingFormGroupIndex === gi" class="inline-add">
                <t-input
                  v-model="newFormValue"
                  size="small"
                  placeholder="输入别名，回车确认"
                  autofocus
                  @keyup.enter="confirmAddForm(gi)"
                  @blur="confirmAddForm(gi)"
                  @keyup.escape="cancelAddForm"
                />
              </div>
              <t-button
                v-else
                theme="default"
                variant="dashed"
                size="small"
                class="add-form-btn"
                @click="startAddForm(gi)"
              >
                <template #icon><t-icon name="add" /></template>
                添加别名
              </t-button>
            </div>
          </div>

          <!-- Products — model numbers / SKUs owned by this entity. NOT
               expanded into queries; used by entity-mismatch and
               attribution-conflict detectors only. -->
          <div class="field-section">
            <div class="field-label">
              <span class="field-name">产品型号</span>
              <span class="field-hint">归属此实体的产品（"EG5120" 属于 Robustel）。不进入检索扩展。</span>
            </div>
            <div class="forms-tags">
              <t-tag
                v-for="(product, pi) in (group.products || [])"
                :key="`product-${pi}`"
                closable
                theme="warning"
                variant="light"
                class="form-tag product-tag"
                @close="removeProduct(gi, pi)"
              >
                {{ product }}
              </t-tag>

              <div v-if="addingProductGroupIndex === gi" class="inline-add">
                <t-input
                  v-model="newProductValue"
                  size="small"
                  placeholder="输入产品型号，回车确认"
                  autofocus
                  @keyup.enter="confirmAddProduct(gi)"
                  @blur="confirmAddProduct(gi)"
                  @keyup.escape="cancelAddProduct"
                />
              </div>
              <t-button
                v-else
                theme="default"
                variant="dashed"
                size="small"
                class="add-form-btn"
                @click="startAddProduct(gi)"
              >
                <template #icon><t-icon name="add" /></template>
                添加产品型号
              </t-button>
            </div>
          </div>
        </div>
      </div>

      <!-- Add group -->
      <t-button
        theme="default"
        variant="outline"
        class="add-group-btn"
        @click="addGroup"
      >
        <template #icon><t-icon name="add" /></template>
        新增别名组
      </t-button>

      <!-- Save -->
      <div class="save-row">
        <t-button
          theme="primary"
          :loading="saving"
          @click="save"
        >
          保存
        </t-button>
        <span v-if="savedOk" class="save-ok">✓ 已保存</span>
        <span v-if="saveError" class="save-err">{{ saveError }}</span>
      </div>

      <!-- ─── Wiki Auto-Discovered Section (read-only) ─── -->
      <div class="auto-discovered-section">
        <div class="auto-header">
          <h3>Wiki 自动发现的品牌组</h3>
          <span class="auto-count">{{ autoDiscovered.length }} 个候选品牌</span>
        </div>
        <p class="auto-description">
          系统根据 wiki 实体页的产品-品牌 outLinks 自动识别（阈值 ≥ 3 个产品指向）。
          这些组<strong>只在内存中参与 retrieval / 归属冲突检测</strong>，不写入 yaml。
          觉得不该被识别为竞品的（例如云平台、SoC 厂、上游软件），点 ❌ 加入忽略列表，下次永不出现。
        </p>

        <div v-if="autoDiscovered.length === 0" class="auto-empty">
          目前没有自动发现的新品牌候选。
        </div>

        <div v-else class="auto-groups-list">
          <div
            v-for="(group, gi) in autoDiscovered"
            :key="group.wiki_slug"
            class="auto-group"
          >
            <div class="auto-group-header">
              <span class="auto-brand-name">{{ group.forms[0] }}</span>
              <span class="auto-slug">{{ group.wiki_slug }}</span>
              <t-button
                theme="danger"
                variant="text"
                size="small"
                :loading="ignoringSlug === group.wiki_slug"
                @click="ignoreAuto(group.wiki_slug)"
              >
                <template #icon><t-icon name="close" /></template>
                忽略
              </t-button>
            </div>
            <div class="auto-forms">
              <span class="auto-label">别名：</span>
              <t-tag
                v-for="(form, fi) in group.forms"
                :key="`auto-form-${gi}-${fi}`"
                theme="primary"
                variant="outline"
                class="auto-tag"
              >
                {{ form }}
              </t-tag>
            </div>
            <div v-if="(group.products || []).length > 0" class="auto-products">
              <span class="auto-label">产品（{{ group.products!.length }}）：</span>
              <t-tag
                v-for="(product, pi) in (group.products || []).slice(0, 12)"
                :key="`auto-product-${gi}-${pi}`"
                theme="warning"
                variant="outline"
                class="auto-tag product-tag"
              >
                {{ product }}
              </t-tag>
              <span v-if="(group.products || []).length > 12" class="auto-more">
                +{{ (group.products || []).length - 12 }} 个
              </span>
            </div>
          </div>
        </div>
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import {
  getEntityAliases,
  updateEntityAliases,
  ignoreAutoDiscovered,
  type EntityAliasGroup,
  type AutoDiscoveredGroup,
} from '@/api/entity-aliases'

// ---- state ----
const loading = ref(true)
const saving = ref(false)
const savedOk = ref(false)
const saveError = ref('')

const groups = ref<EntityAliasGroup[]>([])
// Wiki auto-discovered candidate brands — read-only, ignorable.
const autoDiscovered = ref<AutoDiscoveredGroup[]>([])
const ignoringSlug = ref<string>('')

// inline add-form state
const addingFormGroupIndex = ref<number | null>(null)
const newFormValue = ref('')

// inline add-product state (parallel to the form state above)
const addingProductGroupIndex = ref<number | null>(null)
const newProductValue = ref('')

// ---- lifecycle ----
async function reload() {
  try {
    const res: any = await getEntityAliases()
    const data = res?.data ?? res
    // Read BOTH forms and products from the server. Earlier versions of the
    // settings page only deserialized forms, causing products to be silently
    // dropped on the next save (the server's PUT handler then echoed back
    // the truncated group). Preserving products here closes that loop.
    groups.value = (data?.groups ?? []).map((g: any) => ({
      forms: [...(g.forms ?? [])],
      products: [...(g.products ?? [])],
    }))
    // Wiki auto-discovered groups (read-only display).
    autoDiscovered.value = (data?.auto_discovered ?? []).map((g: any) => ({
      forms: [...(g.forms ?? [])],
      products: [...(g.products ?? [])],
      kind: g.kind ?? '',
      source: g.source ?? 'wiki-auto',
      wiki_slug: g.wiki_slug ?? '',
    }))
  } catch (e: unknown) {
    saveError.value = e instanceof Error ? e.message : String(e)
  } finally {
    loading.value = false
  }
}

onMounted(() => { reload() })

// ---- form editing ----
function startAddForm(gi: number) {
  addingFormGroupIndex.value = gi
  newFormValue.value = ''
}

function confirmAddForm(gi: number) {
  const val = newFormValue.value.trim()
  if (val && !groups.value[gi].forms.includes(val)) {
    groups.value[gi].forms.push(val)
  }
  addingFormGroupIndex.value = null
  newFormValue.value = ''
}

function cancelAddForm() {
  addingFormGroupIndex.value = null
  newFormValue.value = ''
}

function removeForm(gi: number, fi: number) {
  groups.value[gi].forms.splice(fi, 1)
}

// ---- product editing (mirror of form editing) ----
function startAddProduct(gi: number) {
  addingProductGroupIndex.value = gi
  newProductValue.value = ''
}

function confirmAddProduct(gi: number) {
  const val = newProductValue.value.trim()
  const g = groups.value[gi]
  if (!g.products) g.products = []
  if (val && !g.products.includes(val)) {
    g.products.push(val)
  }
  addingProductGroupIndex.value = null
  newProductValue.value = ''
}

function cancelAddProduct() {
  addingProductGroupIndex.value = null
  newProductValue.value = ''
}

function removeProduct(gi: number, pi: number) {
  const g = groups.value[gi]
  if (!g.products) return
  g.products.splice(pi, 1)
}

// ---- group editing ----
function addGroup() {
  groups.value.push({ forms: [], products: [] })
  // immediately open the add-form input for the new group
  addingFormGroupIndex.value = groups.value.length - 1
  newFormValue.value = ''
}

function removeGroup(gi: number) {
  groups.value.splice(gi, 1)
}

// ---- auto-discovered actions ----
async function ignoreAuto(slug: string) {
  if (!slug || ignoringSlug.value) return
  ignoringSlug.value = slug
  try {
    await ignoreAutoDiscovered(slug)
    // Refresh the auto-discovered list — the just-ignored slug will be
    // gone since the backend re-runs Build with the new denylist.
    await reload()
  } catch (e: unknown) {
    saveError.value = e instanceof Error ? e.message : String(e)
  } finally {
    ignoringSlug.value = ''
  }
}

// ---- save ----
async function save() {
  savedOk.value = false
  saveError.value = ''
  saving.value = true
  try {
    await updateEntityAliases({ groups: groups.value })
    savedOk.value = true
    setTimeout(() => { savedOk.value = false }, 3000)
  } catch (e: unknown) {
    saveError.value = e instanceof Error ? e.message : String(e)
  } finally {
    saving.value = false
  }
}
</script>

<style lang="less" scoped>
.entity-alias-settings {
  .section-header {
    margin-bottom: 28px;

    h2 {
      font-size: 18px;
      font-weight: 600;
      color: var(--td-text-color-primary);
      margin: 0 0 8px;
    }

    .section-description {
      font-size: 13px;
      color: var(--td-text-color-secondary);
      margin: 0 0 8px;
      line-height: 1.6;
    }

    .section-actions {
      font-size: 13px;
      color: var(--td-text-color-secondary);
      margin: 4px 0 12px;
      padding-left: 22px;
      line-height: 1.7;

      li { margin: 2px 0; }
      strong { color: var(--td-text-color-primary); }
    }

    .section-description-sub {
      font-size: 12px;
      color: var(--td-text-color-secondary);
      margin: 4px 0 8px;
      line-height: 1.6;
    }

    .section-description-note {
      font-size: 12px;
      color: var(--td-text-color-placeholder);
      background: var(--td-bg-color-secondarycontainer);
      border-left: 3px solid var(--td-brand-color-light);
      padding: 8px 12px;
      margin: 4px 0 0;
      line-height: 1.6;
      border-radius: 0 4px 4px 0;

      code {
        font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
        font-size: 11px;
        background: var(--td-bg-color-component);
        padding: 1px 5px;
        border-radius: 3px;
      }

      strong { color: var(--td-text-color-primary); }
    }
  }

  .loading-state {
    display: flex;
    align-items: center;
    gap: 10px;
    color: var(--td-text-color-secondary);
    font-size: 14px;
  }

  // ---- groups ----
  .groups-list {
    display: flex;
    flex-direction: column;
    gap: 12px;
    margin-bottom: 16px;
  }

  .alias-group {
    border: 1px solid var(--td-component-stroke);
    border-radius: 8px;
    padding: 14px 16px;
    background: var(--td-bg-color-secondarycontainer);
    transition: border-color 0.2s;

    &:hover {
      border-color: var(--td-brand-color-light);
    }
  }

  .group-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 10px;
  }

  .group-index {
    font-size: 12px;
    font-weight: 600;
    color: var(--td-text-color-secondary);
    text-transform: uppercase;
    letter-spacing: 0.04em;
  }

  // ---- forms / products dual-section layout ----
  .field-section {
    margin-top: 10px;

    &:first-of-type {
      margin-top: 0;
    }
  }

  .field-label {
    display: flex;
    align-items: baseline;
    gap: 8px;
    margin-bottom: 6px;
  }

  .field-name {
    font-size: 12px;
    font-weight: 600;
    color: var(--td-text-color-primary);
  }

  .field-hint {
    font-size: 11px;
    color: var(--td-text-color-placeholder);
  }

  .forms-tags {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    align-items: center;
  }

  .form-tag {
    font-size: 13px;
    font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  }

  .product-tag {
    // Visually distinct from form tags (forms = brand-name aliases, products
    // = SKUs). Using the warning theme keeps it readable and signals
    // "different category from the alias above".
    letter-spacing: 0.02em;
  }

  .inline-add {
    width: 150px;
  }

  .add-form-btn {
    height: 26px;
    font-size: 12px;
    padding: 0 10px;
    border-style: dashed;
  }

  // ---- add group ----
  .add-group-btn {
    width: 100%;
    margin-bottom: 24px;
  }

  // ---- save row ----
  .save-row {
    display: flex;
    align-items: center;
    gap: 12px;
  }

  .save-ok {
    font-size: 13px;
    color: var(--td-success-color);
  }

  .save-err {
    font-size: 13px;
    color: var(--td-error-color);
  }

  // ---- wiki auto-discovered section ----
  .auto-discovered-section {
    margin-top: 36px;
    padding-top: 28px;
    border-top: 1px dashed var(--td-component-stroke);
  }

  .auto-header {
    display: flex;
    align-items: baseline;
    gap: 12px;
    margin-bottom: 8px;

    h3 {
      font-size: 16px;
      font-weight: 600;
      color: var(--td-text-color-primary);
      margin: 0;
    }
  }

  .auto-count {
    font-size: 12px;
    color: var(--td-text-color-placeholder);
  }

  .auto-description {
    font-size: 12px;
    color: var(--td-text-color-secondary);
    margin: 0 0 16px;
    line-height: 1.6;

    strong { color: var(--td-text-color-primary); }
  }

  .auto-empty {
    font-size: 13px;
    color: var(--td-text-color-placeholder);
    padding: 20px;
    text-align: center;
    border: 1px dashed var(--td-component-stroke);
    border-radius: 8px;
  }

  .auto-groups-list {
    display: flex;
    flex-direction: column;
    gap: 10px;
  }

  .auto-group {
    border: 1px solid var(--td-component-stroke);
    border-radius: 8px;
    padding: 12px 14px;
    background: var(--td-bg-color-secondarycontainer);
    opacity: 0.92;
  }

  .auto-group-header {
    display: flex;
    align-items: center;
    gap: 10px;
    margin-bottom: 8px;
  }

  .auto-brand-name {
    font-size: 14px;
    font-weight: 600;
    color: var(--td-text-color-primary);
  }

  .auto-slug {
    flex: 1;
    font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
    font-size: 11px;
    color: var(--td-text-color-placeholder);
  }

  .auto-forms,
  .auto-products {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    align-items: center;
    margin-top: 4px;
  }

  .auto-label {
    font-size: 12px;
    color: var(--td-text-color-secondary);
    margin-right: 4px;
  }

  .auto-tag {
    font-size: 12px;

    &.product-tag {
      font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
      letter-spacing: 0.02em;
    }
  }

  .auto-more {
    font-size: 11px;
    color: var(--td-text-color-placeholder);
    font-style: italic;
  }
}
</style>
