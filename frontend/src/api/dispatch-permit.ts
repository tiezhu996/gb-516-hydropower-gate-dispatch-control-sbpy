
import { request } from './client';
import type { DispatchPermit } from '../types/domain';

export interface ApplyPermitInput {
	code: string;
	name: string;
	description?: string;
	directiveCode: string;
	action: 'open' | 'closed';
	validFrom: string;
	validUntil: string;
	observedLevel: number;
}

export async function listDispatchPermit(page = 1, pageSize = 20, search = '', status = '') {
	const query = new URLSearchParams({ page: String(page), pageSize: String(pageSize), search });
	if (status) query.set('status', status);
	return request<DispatchPermit[]>(`/permits?${query.toString()}`);
}

export async function getDispatchPermit(id: number) {
	return request<DispatchPermit>(`/permits/${id}`);
}

export async function applyDispatchPermit(input: ApplyPermitInput) {
	return request<DispatchPermit>('/permits', { method: 'POST', body: JSON.stringify(input) });
}

function decision(path: string, id: number, expectedVersion: number, reason: string) {
	return request<DispatchPermit>(`/permits/${id}/${path}`, {
		method: 'POST', body: JSON.stringify({ expectedVersion, reason }),
	});
}

export const approveDispatchPermit = (id: number, expectedVersion: number, reason: string) => decision('approve', id, expectedVersion, reason);
export const rejectDispatchPermit = (id: number, expectedVersion: number, reason: string) => decision('reject', id, expectedVersion, reason);
export const activateDispatchPermit = (id: number, expectedVersion: number, reason: string) => decision('activate', id, expectedVersion, reason);
export const invalidateDispatchPermit = (id: number, expectedVersion: number, reason: string) => decision('invalidate', id, expectedVersion, reason);
