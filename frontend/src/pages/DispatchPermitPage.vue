<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue';
import { Plus, Refresh, Search } from '@element-plus/icons-vue';
import { useAuth } from '../hooks/useAuth';
import { usePolling } from '../hooks/usePolling';
import { request } from '../api/client';
import { useDispatchPermitStore } from '../stores/dispatch-permit';
import type { DomainRecord, DispatchPermit } from '../types/domain';
import { actionLabel, formatDate, riskLabel, statusLabel } from '../utils/format';
import StatusBadge from '../components/common/StatusBadge.vue';
import GateStateBadge from '../components/common/GateStateBadge.vue';
import MetricCard from '../components/common/MetricCard.vue';
import ConfirmDialog from '../components/common/ConfirmDialog.vue';

const { session, can } = useAuth();
const store = useDispatchPermitStore();
const search = ref('');
const statusFilter = ref('');
const showApply = ref(false);
const decision = ref<{ permit: DispatchPermit; kind: 'approve' | 'reject' } | null>(null);
const activation = ref<DispatchPermit | null>(null);
const detail = ref<DispatchPermit | null>(null);
const showDetail = ref(false);
const decisionReason = ref('');
const activationReason = ref('');

const approvedDirectives = ref<DomainRecord[]>([]);
const applyForm = reactive({
  code: '',
  name: '',
  description: '',
  owner: '',
  relatedCode: '',
  action: 'open' as 'open' | 'closed',
  validFrom: '',
  validUntil: '',
  evidence: '',
});

const pendingCount = computed(() => store.items.filter((item) => item.status === 'pending').length);
const effectiveCount = computed(() => store.items.filter((item) => ['approved', 'active'].includes(item.status)).length);
const invalidCount = computed(() => store.items.filter((item) => ['invalidated', 'rejected', 'expired'].includes(item.status)).length);

async function load(): Promise<void> {
  await store.load(search.value, statusFilter.value);
}

onMounted(() => void load());
usePolling(load, 30_000);

const selectedDirective = computed(() => approvedDirectives.value.find((item) => item.code === applyForm.relatedCode));
const applyReady = computed(() => Boolean(
  applyForm.code.trim() && applyForm.name.trim() && applyForm.owner.trim() && applyForm.relatedCode &&
  applyForm.validFrom && applyForm.validUntil && applyForm.evidence.trim().length >= 3,
));

async function prepareApply(): Promise<void> {
  const suffix = String(Date.now()).slice(-6);
  applyForm.code = `DP-${suffix}`;
  applyForm.name = '';
  applyForm.description = '';
  applyForm.owner = session.value?.displayName || '现场操作员';
  applyForm.relatedCode = '';
  applyForm.action = 'open';
  applyForm.validFrom = new Date().toISOString().slice(0, 16);
  const until = new Date(Date.now() + 8 * 3600_000);
  applyForm.validUntil = until.toISOString().slice(0, 16);
  applyForm.evidence = '';
  const result = await request<DomainRecord[]>('/directives?page=1&pageSize=100&status=approved');
  approvedDirectives.value = result.data;
  showApply.value = true;
}

function selectDirective(code: string): void {
  applyForm.relatedCode = code;
  const directive = approvedDirectives.value.find((item) => item.code === code);
  if (directive) {
    applyForm.name = `${directive.name}调度许可`;
    if (directive.gateState === 'open' || directive.gateState === 'closed') applyForm.action = directive.gateState;
  }
}

async function submitApply(): Promise<void> {
  if (!applyReady.value) return;
  await store.apply({
    code: applyForm.code.trim(),
    name: applyForm.name.trim(),
    description: applyForm.description.trim(),
    owner: applyForm.owner.trim(),
    relatedCode: applyForm.relatedCode,
    action: applyForm.action,
    validFrom: new Date(applyForm.validFrom).toISOString(),
    validUntil: new Date(applyForm.validUntil).toISOString(),
    evidence: applyForm.evidence.trim(),
  });
  if (!store.error) showApply.value = false;
}

function startDecision(permit: DispatchPermit, kind: 'approve' | 'reject'): void {
  decision.value = { permit, kind };
  decisionReason.value = kind === 'approve'
    ? '已复核库区水位窗口、闸门归属与闭锁状态，申请条件仍然满足'
    : '复核发现申请条件不满足，驳回并要求重新申请';
}

async function confirmDecision(): Promise<void> {
  if (!decision.value || decisionReason.value.trim().length < 3) return;
  await store.decide(decision.value.permit, decision.value.kind, decisionReason.value.trim());
  if (!store.error) decision.value = null;
}

function startActivation(permit: DispatchPermit): void {
  activation.value = permit;
  activationReason.value = '动作开始前再次核对库区水位与闸门版本一致';
}

async function confirmActivation(): Promise<void> {
  if (!activation.value || activationReason.value.trim().length < 3) return;
  await store.activate(activation.value, activationReason.value.trim());
  if (!store.error) activation.value = null;
}

function canReview(permit: DispatchPermit): boolean {
  return permit.status === 'pending' && can('reviewer', 'admin') && permit.appliedBy !== session.value?.username;
}
function canActivate(permit: DispatchPermit): boolean {
  return permit.status === 'approved' && can('operator', 'admin');
}
function validityState(permit: DispatchPermit): 'in-window' | 'expired-window' | 'not-started' {
  const now = Date.now();
  if (now < new Date(permit.validFrom).getTime()) return 'not-started';
  if (now > new Date(permit.validUntil).getTime()) return 'expired-window';
  return 'in-window';
}
</script>

<template>
  <main class="workspace">
    <header class="page-header">
      <div>
        <p class="eyebrow">安全许可工作台</p>
        <h1>调度许可</h1>
        <p>操作员依据已获批指令申请闸门动作和有效期；安全复核员独立批准；动作开始前再次核对水位与闸门版本，条件变化即失效并要求重申请。</p>
      </div>
      <el-button v-if="can('operator', 'admin')" type="primary" :icon="Plus" @click="prepareApply">申请调度许可</el-button>
    </header>

    <section class="metrics" aria-label="许可统计">
      <MetricCard label="待复核" :value="pendingCount" detail="等待安全复核员决定" />
      <MetricCard label="生效中" :value="effectiveCount" detail="已批准或已开始动作" />
      <MetricCard label="失效/驳回" :value="invalidCount" detail="需要重新申请" />
    </section>

    <section class="toolbar" aria-label="筛选工具栏">
      <el-input v-model="search" :prefix-icon="Search" placeholder="搜索许可编码或名称" clearable @keyup.enter="load" />
      <el-select v-model="statusFilter" placeholder="全部状态" clearable style="width: 150px" @change="load">
        <el-option v-for="state in ['pending', 'approved', 'active', 'rejected', 'invalidated', 'expired']" :key="state" :label="statusLabel(state)" :value="state" />
      </el-select>
      <el-button type="primary" :icon="Search" @click="load">查询</el-button>
      <el-button :icon="Refresh" @click="search = ''; statusFilter = ''; load()">重置</el-button>
    </section>
    <el-alert v-if="store.error" :title="store.error" type="error" show-icon closable @close="store.error = ''" />

    <section class="table-shell">
      <el-table v-loading="store.loading" :data="store.items" empty-text="暂无调度许可">
        <el-table-column prop="code" label="许可编码" width="130" />
        <el-table-column label="名称/指令" min-width="210">
          <template #default="{ row }">
            <strong>{{ row.name }}</strong>
            <small>{{ row.relatedCode }} · {{ row.gateCode }}</small>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="105">
          <template #default="{ row }"><StatusBadge :status="row.status" /></template>
        </el-table-column>
        <el-table-column label="动作" width="110">
          <template #default="{ row }"><GateStateBadge :state="row.action" /></template>
        </el-table-column>
        <el-table-column label="有效期" width="290">
          <template #default="{ row }">
            <span>{{ formatDate(row.validFrom) }} → {{ formatDate(row.validUntil) }}</span>
            <el-tag v-if="['pending', 'approved', 'active'].includes(row.status)" size="small" :type="validityState(row) === 'in-window' ? 'success' : 'info'" disable-transitions>
              {{ validityState(row) === 'in-window' ? '窗口内' : validityState(row) === 'not-started' ? '未到生效' : '窗口已过' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="申请人/复核" min-width="150">
          <template #default="{ row }">
            <small>申请 {{ row.appliedBy || '-' }}</small>
            <small>复核 {{ row.approvedBy || '-' }}</small>
          </template>
        </el-table-column>
        <el-table-column label="风险" width="70"><template #default="{ row }">{{ riskLabel(row.riskLevel) }}</template></el-table-column>
        <el-table-column label="操作" min-width="210" fixed="right">
          <template #default="{ row }">
            <div class="row-actions">
              <el-button link type="primary" @click="detail = row; showDetail = true">轨迹</el-button>
              <el-button v-if="canReview(row)" link type="primary" @click="startDecision(row, 'approve')">批准</el-button>
              <el-button v-if="canReview(row)" link type="danger" @click="startDecision(row, 'reject')">驳回</el-button>
              <el-button v-if="canActivate(row)" link type="primary" @click="startActivation(row)">开始动作</el-button>
              <span v-if="!canReview(row) && !canActivate(row)" class="muted">-</span>
            </div>
          </template>
        </el-table-column>
      </el-table>
    </section>

    <ConfirmDialog v-model="showApply" title="申请调度许可" confirm-label="提交申请" :confirm-disabled="!applyReady || store.loading" :loading="store.loading" @confirm="submitApply">
      <el-alert type="info" :closable="false" show-icon title="系统会核对库区水位、闸门归属与闭锁状态；不符将拒绝并说明原因。同一闸门只允许一份生效中的许可。" />
      <el-form class="record-form" label-position="top">
        <div class="form-grid">
          <el-form-item label="许可编码"><el-input v-model="applyForm.code" /></el-form-item>
          <el-form-item label="名称"><el-input v-model="applyForm.name" /></el-form-item>
          <el-form-item label="已获批指令">
            <el-select :model-value="applyForm.relatedCode" filterable placeholder="仅可选择已获批指令" @update:model-value="selectDirective">
              <el-option v-for="item in approvedDirectives" :key="item.id" :label="`${item.code} · ${item.name}（${item.relatedCode}）`" :value="item.code" />
            </el-select>
          </el-form-item>
          <el-form-item label="申请动作">
            <el-select v-model="applyForm.action">
              <el-option label="开启闸门" value="open" />
              <el-option label="关闭闸门" value="closed" />
            </el-select>
          </el-form-item>
          <el-form-item label="生效开始"><el-date-picker v-model="applyForm.validFrom" type="datetime" value-format="YYYY-MM-DDTHH:mm" format="YYYY-MM-DD HH:mm" /></el-form-item>
          <el-form-item label="生效结束（最长 24 小时）"><el-date-picker v-model="applyForm.validUntil" type="datetime" value-format="YYYY-MM-DDTHH:mm" format="YYYY-MM-DD HH:mm" /></el-form-item>
          <el-form-item label="责任人"><el-input v-model="applyForm.owner" /></el-form-item>
        </div>
        <el-form-item v-if="selectedDirective" label="指令快照">
          <el-descriptions :column="3" border size="small">
            <el-descriptions-item label="目标闸门">{{ selectedDirective.relatedCode }}</el-descriptions-item>
            <el-descriptions-item label="指令目标"><GateStateBadge :state="selectedDirective.gateState || 'closed'" /></el-descriptions-item>
            <el-descriptions-item label="动作须一致">{{ actionLabel(applyForm.action) }}</el-descriptions-item>
          </el-descriptions>
        </el-form-item>
        <el-form-item label="业务说明"><el-input v-model="applyForm.description" type="textarea" :rows="2" maxlength="1000" show-word-limit /></el-form-item>
        <el-form-item label="现场证据（必填）"><el-input v-model="applyForm.evidence" type="textarea" :rows="3" maxlength="2000" show-word-limit /></el-form-item>
      </el-form>
    </ConfirmDialog>

    <ConfirmDialog :model-value="Boolean(decision)" :title="decision?.kind === 'approve' ? '批准调度许可' : '驳回调度许可'"
      :confirm-label="decision?.kind === 'approve' ? '确认批准' : '确认驳回'"
      :confirm-type="decision?.kind === 'approve' ? 'primary' : 'danger'"
      :loading="store.loading" @update:model-value="(value: boolean) => !value && (decision = null)" @confirm="confirmDecision">
      <p v-if="decision">批准前会再次核对库区水位、闸门归属、闭锁与闸门版本；申请人 {{ decision.permit.appliedBy }} 与当前账号 {{ session?.username }} 必须不同。</p>
      <div v-if="decision" class="transition-summary"><StatusBadge :status="decision.permit.status" /><span>到</span><StatusBadge :status="decision.kind === 'approve' ? 'approved' : 'rejected'" /></div>
      <el-input v-model="decisionReason" type="textarea" :rows="3" maxlength="500" show-word-limit aria-label="复核意见" />
    </ConfirmDialog>

    <ConfirmDialog :model-value="Boolean(activation)" title="动作开始前复核" confirm-label="复核通过并开始" :loading="store.loading"
      @update:model-value="(value: boolean) => !value && (activation = null)" @confirm="confirmActivation">
      <el-alert v-if="activation" type="warning" :closable="false" show-icon :title="`若库区水位变化或闸门版本已不是批准时的 v${activation.approvedGateVersion}，许可将立即失效且不改写指令或闸门状态。`" />
      <el-input v-model="activationReason" type="textarea" :rows="3" maxlength="500" show-word-limit aria-label="动作前复核说明" />
    </ConfirmDialog>

    <el-drawer v-model="showDetail" size="420px" title="调度许可轨迹" @closed="detail = null">
      <template v-if="detail">
        <el-descriptions :column="1" border size="small">
          <el-descriptions-item label="许可状态"><StatusBadge :status="detail.status" /></el-descriptions-item>
          <el-descriptions-item label="动作 / 闸门">{{ actionLabel(detail.action) }} · {{ detail.gateCode }}</el-descriptions-item>
          <el-descriptions-item label="关联指令">{{ detail.relatedCode }}（快照 v{{ detail.snapshotDirectiveVersion }}）</el-descriptions-item>
          <el-descriptions-item label="库区">{{ detail.reservoirCode }} · 申请时 {{ detail.snapshotReservoirStatus }}（v{{ detail.snapshotReservoirVersion }}）</el-descriptions-item>
          <el-descriptions-item label="闸门快照">{{ detail.snapshotGateStatus }}（v{{ detail.snapshotGateVersion }}）· 批准时 v{{ detail.approvedGateVersion }}</el-descriptions-item>
          <el-descriptions-item label="有效期">{{ formatDate(detail.validFrom) }} → {{ formatDate(detail.validUntil) }}</el-descriptions-item>
          <el-descriptions-item v-if="detail.invalidReason" label="失效原因"><strong>{{ detail.invalidReason }}</strong></el-descriptions-item>
        </el-descriptions>
        <h4 style="margin: 16px 0 8px">复核与动作轨迹</h4>
        <el-timeline>
          <el-timeline-item v-for="entry in detail.decisions || []" :key="entry.id" :timestamp="`${formatDate(entry.createdAt)} · ${entry.requestId}`">
            <strong>{{ statusLabel(entry.toState || entry.stage) }}</strong>
            <p>{{ entry.actor }}（{{ entry.role }}）· {{ entry.reason }}</p>
          </el-timeline-item>
        </el-timeline>
      </template>
    </el-drawer>
  </main>
</template>
