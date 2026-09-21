<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue';
import { Refresh, Search } from '@element-plus/icons-vue';
import type { DomainRecord, DispatchPermit } from '../types/domain';
import { ALL_PERMIT_STATE } from '../types/status';
import { formatDate, riskLabel, statusLabel } from '../utils/format';
import { useAuth } from '../hooks/useAuth';
import { usePolling } from '../hooks/usePolling';
import { request } from '../api/client';
import { useDispatchPermitStore } from '../stores/dispatch-permit';
import StatusBadge from '../components/common/StatusBadge.vue';
import GateStateBadge from '../components/common/GateStateBadge.vue';
import MetricCard from '../components/common/MetricCard.vue';
import PermitTimeline from '../components/common/PermitTimeline.vue';
import ConfirmDialog from '../components/common/ConfirmDialog.vue';

const store = useDispatchPermitStore();
const { session, can } = useAuth();
const search = ref('');
const statusFilter = ref('');
const showApply = ref(false);
const decision = ref<{ item: DispatchPermit; action: 'approve' | 'reject' | 'activate' | 'invalidate' } | null>(null);
const decisionReason = ref('');
const directiveOptions = ref<DomainRecord[]>([]);

const applyForm = reactive({
	code: '',
	name: '',
	description: '',
	directiveCode: '',
	action: 'open' as 'open' | 'closed',
	hours: 2,
	observedLevel: 0 as number,
});

async function load(): Promise<void> {
	await store.load(search.value, statusFilter.value);
}

onMounted(() => void load());
usePolling(load, 30_000);

const activeCount = computed(() => store.items.filter((item) => ['requested', 'approved'].includes(item.status)).length);
const waitingReview = computed(() => store.items.filter((item) => item.status === 'requested').length);

const applyReady = computed(() => Boolean(
	applyForm.code.trim() && applyForm.name.trim() && applyForm.directiveCode &&
	Number.isFinite(applyForm.observedLevel) && applyForm.hours >= 1,
));

async function prepareApply(): Promise<void> {
	const suffix = String(Date.now()).slice(-6);
	applyForm.code = `DP-${suffix}`;
	applyForm.name = '';
	applyForm.description = '';
	applyForm.directiveCode = '';
	applyForm.action = 'open';
	applyForm.hours = 2;
	applyForm.observedLevel = 0;
	directiveOptions.value = [];
	try {
		const result = await request<DomainRecord[]>('/directives?page=1&pageSize=100&status=approved');
		directiveOptions.value = result.data;
		selectDirective(directiveOptions.value[0]?.code || '');
		showApply.value = true;
	} catch (reason) {
		store.error = reason instanceof Error ? reason.message : String(reason);
	}
}

function selectDirective(code: string): void {
	applyForm.directiveCode = code;
	const directive = directiveOptions.value.find((item) => item.code === code);
	if (directive) {
		applyForm.action = (directive.gateState === 'open' ? 'open' : 'closed') as 'open' | 'closed';
		applyForm.name = `${directive.name}调度许可`;
	}
}

async function submitApply(): Promise<void> {
	if (!applyReady.value) {
		store.error = '请完整填写许可动作、有效期和现场观测水位';
		return;
	}
	const now = new Date();
	const validFrom = new Date(now.getTime() - 60_000);
	const created = await store.apply({
		code: applyForm.code.trim(),
		name: applyForm.name.trim(),
		description: applyForm.description.trim(),
		directiveCode: applyForm.directiveCode,
		action: applyForm.action,
		validFrom: validFrom.toISOString(),
		validUntil: new Date(now.getTime() + applyForm.hours * 3_600_000).toISOString(),
		observedLevel: applyForm.observedLevel,
	});
	if (created) showApply.value = false;
}

function canDecide(item: DispatchPermit, action: 'approve' | 'reject' | 'activate' | 'invalidate'): boolean {
	if (action === 'approve' || action === 'reject') {
		return item.status === 'requested' && can('reviewer', 'admin') && item.appliedBy !== session.value?.username;
	}
	if (action === 'activate') {
		return item.status === 'approved' && can('operator', 'admin');
	}
	return ['requested', 'approved'].includes(item.status) && can('operator', 'reviewer', 'admin');
}

function startDecision(item: DispatchPermit, action: 'approve' | 'reject' | 'activate' | 'invalidate'): void {
	decision.value = { item, action };
	decisionReason.value = {
		approve: '已复核库区水位窗口、闸门归属与闭锁状态，条件一致',
		reject: '申请条件不符，拒绝本次调度许可并说明原因',
		activate: '动作开始前再次核对水位、闸门版本与指令版本一致',
		invalidate: '现场条件变化，许可不再适用并立即失效',
	}[action];
}

async function confirmDecision(): Promise<void> {
	if (!decision.value || decisionReason.value.trim().length < 3) return;
	const { item, action } = decision.value;
	const result = await store.decide(action, item, decisionReason.value.trim());
	if (result) decision.value = null;
}

const decisionTitle = computed(() => ({
	approve: '批准调度许可', reject: '拒绝调度许可', activate: '生效并开始闸门动作', invalidate: '使调度许可失效',
}[decision.value?.action || 'approve']));
</script>

<template>
  <main class="workspace">
    <header class="page-header">
      <div>
        <p class="eyebrow">安全控制</p>
        <h1>调度许可</h1>
        <p>操作员按已批准指令申请闸门动作与有效期；动作开始前再次核对水位与版本，条件变化许可立即失效。</p>
      </div>
      <el-button v-if="can('operator', 'admin')" type="primary" @click="prepareApply">申请调度许可</el-button>
    </header>

    <section class="metrics" aria-label="许可统计">
      <MetricCard label="许可总数" :value="store.meta.total" detail="全部申请记录" />
      <MetricCard label="待复核" :value="waitingReview" detail="等待安全复核员" />
      <MetricCard label="生效中" :value="activeCount" detail="申请或已批准" />
    </section>

    <PermitTimeline :permits="store.items" />

    <section class="toolbar" aria-label="筛选工具栏">
      <el-input v-model="search" :prefix-icon="Search" placeholder="搜索许可编码、指令或闸门" clearable @keyup.enter="load" />
      <el-select v-model="statusFilter" placeholder="全部状态" clearable style="width: 150px" @change="load">
        <el-option v-for="state in ALL_PERMIT_STATE" :key="state" :label="statusLabel(state)" :value="state" />
      </el-select>
      <el-button type="primary" :icon="Search" @click="load">查询</el-button>
      <el-button :icon="Refresh" @click="search = ''; statusFilter = ''; load()">重置</el-button>
    </section>
    <el-alert v-if="store.error" :title="store.error" type="error" show-icon closable @close="store.error = ''" />

    <section class="table-shell">
      <el-table v-loading="store.loading" :data="store.items" empty-text="暂无符合条件的调度许可">
        <el-table-column prop="code" label="许可编码" width="130" />
        <el-table-column label="关联对象" min-width="210">
          <template #default="{ row }">
            <strong>{{ row.name }}</strong>
            <small>指令 {{ row.directiveCode }} · 闸门 {{ row.gateCode }} · 库区 {{ row.reservoirCode }}</small>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="110">
          <template #default="{ row }"><StatusBadge :status="row.status" /></template>
        </el-table-column>
        <el-table-column label="动作" width="90">
          <template #default="{ row }"><GateStateBadge :state="row.action === 'open' ? 'open' : 'closed'" /></template>
        </el-table-column>
        <el-table-column label="有效期" min-width="240">
          <template #default="{ row }">
            <small>{{ formatDate(row.validFrom) }}<br />至 {{ formatDate(row.validUntil) }}</small>
          </template>
        </el-table-column>
        <el-table-column label="申请 / 批准" min-width="150">
          <template #default="{ row }">
            <small>{{ row.appliedBy || '-' }} → {{ row.approvedBy || '待复核' }}</small>
          </template>
        </el-table-column>
        <el-table-column label="快照水位" width="100">
          <template #default="{ row }">{{ row.approvedLevel || row.appliedLevel }} <small>m</small></template>
        </el-table-column>
        <el-table-column label="风险" width="80"><template #default="{ row }">{{ riskLabel('high') }}</template></el-table-column>
        <el-table-column label="操作" min-width="240" fixed="right">
          <template #default="{ row }">
            <div class="row-actions">
              <el-button v-if="canDecide(row, 'approve')" link type="primary" @click="startDecision(row, 'approve')">批准</el-button>
              <el-button v-if="canDecide(row, 'reject')" link type="danger" @click="startDecision(row, 'reject')">拒绝</el-button>
              <el-button v-if="canDecide(row, 'activate')" link type="primary" @click="startDecision(row, 'activate')">开始动作</el-button>
              <el-button v-if="canDecide(row, 'invalidate')" link type="warning" @click="startDecision(row, 'invalidate')">失效</el-button>
              <span v-if="!(canDecide(row, 'approve') || canDecide(row, 'reject') || canDecide(row, 'activate') || canDecide(row, 'invalidate'))" class="muted">
                当前角色无可执行动作
              </span>
            </div>
          </template>
        </el-table-column>
      </el-table>
    </section>

    <ConfirmDialog v-model="showApply" title="申请调度许可" confirm-label="提交申请" :confirm-disabled="!applyReady" :loading="store.loading" @confirm="submitApply">
      <el-alert type="info" show-icon :closable="false" title="提交时系统核对库区水位窗口、闸门归属与闭锁状态；不符将拒绝且不改写任何记录。" />
      <el-form class="record-form" label-position="top">
        <div class="form-grid">
          <el-form-item label="许可编码"><el-input v-model="applyForm.code" /></el-form-item>
          <el-form-item label="许可名称"><el-input v-model="applyForm.name" /></el-form-item>
          <el-form-item label="已批准指令">
            <el-select :model-value="applyForm.directiveCode" filterable placeholder="仅可选择已批准指令" @update:model-value="selectDirective">
              <el-option v-for="directive in directiveOptions" :key="directive.id" :label="`${directive.code} · ${directive.name}`" :value="directive.code" />
            </el-select>
          </el-form-item>
          <el-form-item label="申请动作">
            <el-radio-group v-model="applyForm.action">
              <el-radio value="open">开闸</el-radio>
              <el-radio value="closed">关闸</el-radio>
            </el-radio-group>
          </el-form-item>
          <el-form-item label="有效时长（小时）">
            <el-input-number v-model="applyForm.hours" :min="1" :max="48" :precision="0" controls-position="right" />
          </el-form-item>
          <el-form-item label="现场观测水位（m）">
            <el-input-number v-model="applyForm.observedLevel" :precision="2" controls-position="right" />
          </el-form-item>
        </div>
        <el-form-item label="申请说明"><el-input v-model="applyForm.description" type="textarea" :rows="2" maxlength="1000" show-word-limit /></el-form-item>
      </el-form>
    </ConfirmDialog>

    <ConfirmDialog :model-value="Boolean(decision)" :title="decisionTitle" confirm-label="确认并记录审计" :loading="store.loading" @update:model-value="decision = null" @confirm="confirmDecision">
      <p v-if="decision?.action === 'activate'">生效前将再次核对水位、闸门版本与指令版本；条件变化时许可自动失效且闸门不动作，需要重新申请。</p>
      <div v-if="decision" class="transition-summary">
        <StatusBadge :status="decision.item.status" />
        <span>到</span>
        <StatusBadge :status="decision.action === 'approve' ? 'approved' : decision.action === 'reject' ? 'rejected' : decision.action === 'activate' ? 'consumed' : 'invalidated'" />
      </div>
      <el-input v-model="decisionReason" type="textarea" :rows="3" maxlength="500" show-word-limit aria-label="决策原因" />
    </ConfirmDialog>
  </main>
</template>
