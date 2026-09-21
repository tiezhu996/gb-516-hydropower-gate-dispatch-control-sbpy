import { defineStore } from 'pinia';
import {
	activateDispatchPermit,
	applyDispatchPermit,
	approveDispatchPermit,
	invalidateDispatchPermit,
	listDispatchPermit,
	rejectDispatchPermit,
	type ApplyPermitInput,
} from '../api/dispatch-permit';
import type { DispatchPermit, PageMeta } from '../types/domain';

interface PermitState {
	items: DispatchPermit[];
	meta: PageMeta;
	loading: boolean;
	error: string;
}

export const useDispatchPermitStore = defineStore('dispatchPermit', {
	state: (): PermitState => ({ items: [], meta: { page: 1, pageSize: 20, total: 0 }, loading: false, error: '' }),
	actions: {
		async load(search = '', status = '') {
			this.loading = true;
			this.error = '';
			try {
				const result = await listDispatchPermit(1, 20, search, status);
				this.items = result.data;
				this.meta = result.meta || { page: 1, pageSize: 20, total: result.data.length };
			} catch (error) {
				this.error = error instanceof Error ? error.message : String(error);
			} finally {
				this.loading = false;
			}
		},
		async apply(input: ApplyPermitInput) {
			this.loading = true;
			this.error = '';
			try {
				const result = await applyDispatchPermit(input);
				await this.load();
				return result.data;
			} catch (error) {
				this.error = error instanceof Error ? error.message : String(error);
				return null;
			} finally {
				this.loading = false;
			}
		},
		async decide(action: 'approve' | 'reject' | 'activate' | 'invalidate', item: DispatchPermit, reason: string) {
			this.loading = true;
			this.error = '';
			const calls = {
				approve: approveDispatchPermit,
				reject: rejectDispatchPermit,
				activate: activateDispatchPermit,
				invalidate: invalidateDispatchPermit,
			} as const;
			try {
				const result = await calls[action](item.id, item.version, reason);
				await this.load();
				return result.data;
			} catch (error) {
				this.error = error instanceof Error ? error.message : String(error);
				return null;
			} finally {
				this.loading = false;
			}
		},
	},
});
