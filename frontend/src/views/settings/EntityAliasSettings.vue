<template>
  <div class="entity-alias-settings">
    <div class="section-header">
      <h2>Entity Aliases</h2>
      <p class="section-description">
        配置中英文实体别名词典，检索时自动将等价名称一同纳入查询（如"鲁邦通" ↔ "Robustel"）。
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

          <div class="forms-area">
            <!-- Existing forms as tags -->
            <div class="forms-tags">
              <t-tag
                v-for="(form, fi) in group.forms"
                :key="fi"
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

// ---- lifecycle ----
onMounted(async () => {
  try {
    const res: any = await getEntityAliases()
    const data = res?.data ?? res
    groups.value = (data?.groups ?? []).map((g: any) => ({ forms: [...(g.forms ?? [])] }))
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

// ---- group editing ----
function addGroup() {
  groups.value.push({ forms: [] })
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

  // ---- forms ----
  .forms-area {
    .forms-tags {
      display: flex;
      flex-wrap: wrap;
      gap: 8px;
      align-items: center;
    }
  }

  .form-tag {
    font-size: 13px;
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
