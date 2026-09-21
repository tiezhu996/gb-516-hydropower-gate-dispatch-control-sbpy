<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { request } from '../api/client';
import type { DispatchPermit } from '../types/domain';
import PermitTimeline from './common/PermitTimeline.vue';

const props = defineProps<{ directiveCode?: string }>();
const permits = ref<DispatchPermit[]>([]);

const filtered = computed(() =>
	props.directiveCode ? permits.value.filter((permit) => permit.directiveCode === props.directiveCode) : permits.value,
);

async function refresh(): Promise<void> {
	try {
		const result = await request<DispatchPermit[]>('/permits?page=1&pageSize=20');
		permits.value = result.data;
	} catch {
		permits.value = [];
	}
}

watch(() => props.directiveCode, refresh, { immediate: true });
</script>

<template><PermitTimeline v-if="filtered.length" :permits="filtered" compact /></template>
