
import { request } from './client';
import type { DispatchPermit, PageMeta } from '../types/domain';

interface PermitPage { data: DispatchPermit[]; meta: PageMeta }

export interface ApplyPermitInput {
  code: string;
  name: string;
  description?: string;
  owner: string;
  relatedCode: string;
  action: 'open' | 'closed';
  validFrom: string;
  validUntil: string;
  evidence: string;
}

export async function listDispatchPermits(page = 1, pageSize = 20, search = '', status = '') {
  const query = new URLSearchParams({ page: String(page), pageSize: String(pageSize), search });
  if (status) query.set('status', status);
  return request<DispatchPermit[]>(`/permits?${query.toString()}`) as Promise<PermitPage>;
}

export async function getDispatchPermit(id: number) {
  return request<DispatchPermit>(`/permits/${id}`);
}

export async function applyDispatchPermit(input: ApplyPermitInput) {
  return request<DispatchPermit>('/permits', { method: 'POST', body: JSON.stringify(input) });
}

export async function decideDispatchPermit(id: number, kind: 'approve' | 'reject', expectedVersion: number, reason: string) {
  return request<DispatchPermit>(`/permits/${id}/${kind}`, {
    method: 'POST', body: JSON.stringify({ expectedVersion, reason }),
  });
}

export async function activateDispatchPermit(id: number, expectedVersion: number, reason: string) {
  return request<DispatchPermit>(`/permits/${id}/activate`, {
    method: 'POST', body: JSON.stringify({ expectedVersion, reason }),
  });
}
