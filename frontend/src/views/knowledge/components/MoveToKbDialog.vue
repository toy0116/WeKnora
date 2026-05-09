<script setup lang="ts">
import { ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { MessagePlugin } from 'tdesign-vue-next';
import { listMoveTargets, moveKnowledge, getKnowledgeMoveProgress } from '@/api/knowledge-base';

const props = defineProps<{
  visible: boolean;
  sourceKbId: string;
  knowledgeIds: string[];
}>();

const emit = defineEmits<{
  (e: 'update:visible', v: boolean): void;
  (e: 'moved'): void;
}>();

const { t } = useI18n();

type Step = 'targets' | 'confirm';
const step = ref<Step>('targets');
const targetKbs = ref<any[]>([]);
const targetsLoading = ref(false);
const selectedKb = ref<any>(null);
const moveMode = ref<'reuse_vectors' | 'reparse'>('reuse_vectors');
const submitting = ref(false);

// Load target KBs whenever dialog opens
watch(() => props.visible, async (val) => {
  if (!val) return;
  step.value = 'targets';
  selectedKb.value = null;
  moveMode.value = 'reuse_vectors';
  submitting.value = false;
  targetsLoading.value = true;
  try {
    const res: any = await listMoveTargets(props.sourceKbId);
    targetKbs.value = res.data || [];
  } catch {
    targetKbs.value = [];
  } finally {
    targetsLoading.value = false;
  }
});

const selectTarget = (kb: any) => {
  selectedKb.value = kb;
  step.value = 'confirm';
};

const goBack = () => {
  step.value = 'targets';
};

const close = () => emit('update:visible', false);

let pollTimer: ReturnType<typeof setInterval> | null = null;

const startPoll = (taskId: string) => {
  if (pollTimer) clearInterval(pollTimer);
  pollTimer = setInterval(async () => {
    try {
      const res: any = await getKnowledgeMoveProgress(taskId);
      const status = res.data?.status;
      if (status === 'completed' || status === 'failed') {
        clearInterval(pollTimer!);
        pollTimer = null;
        submitting.value = false;
        if (status === 'completed') {
          MessagePlugin.success(t('knowledgeBase.moveSuccess', { count: props.knowledgeIds.length }));
          emit('moved');
        } else {
          MessagePlugin.error(t('knowledgeBase.moveFailed'));
        }
      }
    } catch {
      clearInterval(pollTimer!);
      pollTimer = null;
      submitting.value = false;
    }
  }, 1500);
};

const confirm = async () => {
  if (!selectedKb.value || submitting.value) return;
  submitting.value = true;
  try {
    const res: any = await moveKnowledge({
      knowledge_ids: props.knowledgeIds,
      source_kb_id: props.sourceKbId,
      target_kb_id: selectedKb.value.id,
      mode: moveMode.value,
    });
    MessagePlugin.info(t('knowledgeBase.moveStarted'));
    close();
    const taskId = res.data?.task_id;
    if (taskId) {
      startPoll(taskId);
    } else {
      submitting.value = false;
      emit('moved');
    }
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('knowledgeBase.moveFailed'));
    submitting.value = false;
  }
};
</script>

<template>
  <t-dialog
    :visible="visible"
    :header="step === 'targets' ? t('knowledgeBase.moveToKnowledgeBase') : t('knowledgeBase.moveConfirmTitle')"
    :closeBtn="true"
    :cancelBtn="step === 'confirm' ? { content: t('common.cancel') } : null"
    :confirmBtn="step === 'confirm' ? { content: t('knowledgeBase.moveConfirm'), loading: submitting } : null"
    width="420px"
    @close="close"
    @cancel="step === 'confirm' ? goBack() : close()"
    @confirm="confirm"
  >
    <!-- Step 1: pick target KB -->
    <div v-if="step === 'targets'" class="move-dialog-targets">
      <div v-if="targetsLoading" class="move-dialog-loading">
        <t-loading size="small" />
      </div>
      <div v-else-if="targetKbs.length === 0" class="move-dialog-empty">
        {{ t('knowledgeBase.moveNoTargets') }}
      </div>
      <div v-else class="move-target-list">
        <div
          v-for="kb in targetKbs"
          :key="kb.id"
          class="move-target-item"
          @click="selectTarget(kb)"
        >
          <t-icon name="root-list" size="16px" class="move-target-icon" />
          <span class="move-target-name">{{ kb.name }}</span>
          <span v-if="kb.knowledge_count !== undefined" class="move-target-count">{{ kb.knowledge_count }}</span>
          <t-icon name="chevron-right" size="14px" class="move-target-arrow" />
        </div>
      </div>
    </div>

    <!-- Step 2: pick move mode -->
    <div v-else class="move-dialog-confirm">
      <div class="move-confirm-target">
        <t-icon name="arrow-right" size="14px" />
        <span>{{ selectedKb?.name }}</span>
      </div>
      <div
        class="move-mode-item"
        :class="{ active: moveMode === 'reuse_vectors' }"
        @click="moveMode = 'reuse_vectors'"
      >
        <t-radio :checked="moveMode === 'reuse_vectors'" />
        <div class="move-mode-text">
          <span class="move-mode-label">{{ t('knowledgeBase.moveModeReuseVectors') }}</span>
          <span class="move-mode-desc">{{ t('knowledgeBase.moveModeReuseVectorsDesc') }}</span>
        </div>
      </div>
      <div
        class="move-mode-item"
        :class="{ active: moveMode === 'reparse' }"
        @click="moveMode = 'reparse'"
      >
        <t-radio :checked="moveMode === 'reparse'" />
        <div class="move-mode-text">
          <span class="move-mode-label">{{ t('knowledgeBase.moveModeReparse') }}</span>
          <span class="move-mode-desc">{{ t('knowledgeBase.moveModeReparseDesc') }}</span>
        </div>
      </div>
    </div>
  </t-dialog>
</template>

<style scoped lang="less">
.move-dialog-loading,
.move-dialog-empty {
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px 0;
  color: var(--td-text-color-secondary);
  font-size: 13px;
}

.move-target-list {
  display: flex;
  flex-direction: column;
  gap: 2px;
  max-height: 320px;
  overflow-y: auto;
}

.move-target-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px 12px;
  border-radius: 6px;
  cursor: pointer;
  transition: background 0.15s;

  &:hover {
    background: var(--td-bg-color-container-hover);
  }
}

.move-target-icon {
  color: var(--td-text-color-secondary);
  flex-shrink: 0;
}

.move-target-name {
  flex: 1;
  font-size: 14px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.move-target-count {
  font-size: 12px;
  color: var(--td-text-color-secondary);
  flex-shrink: 0;
}

.move-target-arrow {
  color: var(--td-text-color-placeholder);
  flex-shrink: 0;
}

.move-dialog-confirm {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.move-confirm-target {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 8px 12px;
  background: var(--td-bg-color-container-hover);
  border-radius: 6px;
  font-size: 14px;
  font-weight: 500;
  margin-bottom: 4px;
}

.move-mode-item {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 10px 12px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 6px;
  cursor: pointer;
  transition: border-color 0.15s, background 0.15s;

  &:hover {
    background: var(--td-bg-color-container-hover);
  }

  &.active {
    border-color: var(--td-brand-color);
    background: var(--td-brand-color-light);
  }
}

.move-mode-text {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.move-mode-label {
  font-size: 13px;
  font-weight: 500;
}

.move-mode-desc {
  font-size: 12px;
  color: var(--td-text-color-secondary);
  line-height: 1.4;
}
</style>
