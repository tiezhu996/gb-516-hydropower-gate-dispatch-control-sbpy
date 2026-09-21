<script setup lang="ts">
import type { DispatchPermit } from '../../types/domain';
import { formatDate } from '../../utils/format';
import StatusBadge from './StatusBadge.vue';

withDefaults(defineProps<{ permits: DispatchPermit[]; compact?: boolean }>(), { compact: false });

const STAGE_LABELS: Readonly<Record<string, string>> = {
	requested: '申请',
	approved: '批准',
	rejected: '拒绝',
	consumed: '生效',
	invalidated: '失效',
	expired: '过期',
};
</script>

<template>
  <section class="timeline-panel" aria-label="调度许可轨迹">
    <header><strong>调度许可轨迹</strong><span>最近 {{ Math.min(permits.length, 3) }} 份</span></header>
    <div v-if="permits.length" class="directive-timeline">
      <article v-for="permit in permits.slice(0, 3)" :key="permit.id">
        <div class="timeline-record">
          <strong>{{ permit.code }}</strong>
          <span>{{ permit.directiveCode }} · {{ permit.action === 'open' ? '开闸' : '关闸' }}</span>
          <StatusBadge :status="permit.status" />
        </div>
        <ol v-if="permit.decisions?.length" class="approval-steps">
          <li v-for="decision in permit.decisions" :key="decision.id">
            <span>{{ STAGE_LABELS[decision.stage] || decision.stage }}</span>
            <strong>{{ decision.actor }}</strong>
            <small>{{ formatDate(decision.createdAt) }}</small>
          </li>
        </ol>
        <ol v-else class="approval-steps"><li class="pending-step">等待操作员申请</li></ol>
        <p v-if="!compact" class="permit-window">
          有效期 {{ formatDate(permit.validFrom) }} – {{ formatDate(permit.validUntil) }}
        </p>
      </article>
    </div>
    <div v-else class="empty">暂无可展示的调度许可</div>
  </section>
</template>
