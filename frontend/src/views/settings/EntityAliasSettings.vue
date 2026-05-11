<template>
  <div class="entity-alias-settings">
    <div class="section-header">
      <h2>Entity Aliases</h2>
      <p class="section-description">
        配置中英文实体别名词典与产品归属。
        <strong>别名</strong>（如"鲁邦通" ↔ "Robustel"）在检索时自动作等价扩展。
        <strong>产品型号</strong>（如 "EG5120" 归 Robustel、"EG71" 归 Milesight）用于检测
        "用户claim了品牌 X 但提到了属于品牌 Y 的产品" 这类归属冲突，
        防止生成式任务把竞品规格当成自家产品输出。产品型号不会进入 BM25 查询扩展。
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
    </template>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { getEntityAliases, updateEntityAliases, type EntityAliasGroup } from '@/api/entity-aliases'

// ---- state ----
const loading = ref(true)
const saving = ref(false)
const savedOk = ref(false)
const saveError = ref('')

const groups = ref<EntityAliasGroup[]>([])

// inline add-form state
const addingFormGroupIndex = ref<number | null>(null)
const newFormValue = ref('')

// inline add-product state (parallel to the form state above)
const addingProductGroupIndex = ref<number | null>(null)
const newProductValue = ref('')

// ---- lifecycle ----
onMounted(async () => {
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
  } catch (e: unknown) {
    saveError.value = e instanceof Error ? e.message : String(e)
  } finally {
    loading.value = false
  }
})

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
      margin: 0;
      line-height: 1.6;
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
}
</style>
