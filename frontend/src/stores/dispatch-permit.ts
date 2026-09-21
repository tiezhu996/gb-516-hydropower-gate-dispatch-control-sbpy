import { defineStore } from 'pinia';
import {
  activateDispatchPermit,
  applyDispatchPermit,
  decideDispatchPermit,
  listDispatchPermits,
  type ApplyPermitInput,
} from '../api/dispatch-permit';
import type { DispatchPermit, PageMeta } from '../types/domain';

export const useDispatchPermitStore = defineStore('dispatchPermit', {
  state: () => ({
    items: [] as DispatchPermit[],
    meta: { page: 1, pageSize: 20, total: 0 } as PageMeta,
    loading: false,
    error: '',
  }),
  actions: {
    async load(search = '', status = '') {
      this.loading = true;
      this.error = '';
      try {
        const result = await listDispatchPermits(1, 20, search, status);
        this.items = result.data;
        this.meta = result.meta;
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
        await applyDispatchPermit(input);
        await this.load();
      } catch (error) {
        this.error = error instanceof Error ? error.message : String(error);
      } finally {
        this.loading = false;
      }
    },
    async decide(item: DispatchPermit, kind: 'approve' | 'reject', reason: string) {
      this.loading = true;
      this.error = '';
      try {
        await decideDispatchPermit(item.id, kind, item.version, reason);
        await this.load();
      } catch (error) {
        this.error = error instanceof Error ? error.message : String(error);
      } finally {
        this.loading = false;
      }
    },
    async activate(item: DispatchPermit, reason: string) {
      this.loading = true;
      this.error = '';
      try {
        await activateDispatchPermit(item.id, item.version, reason);
        await this.load();
      } catch (error) {
        this.error = error instanceof Error ? error.message : String(error);
      } finally {
        this.loading = false;
      }
    },
  },
});
