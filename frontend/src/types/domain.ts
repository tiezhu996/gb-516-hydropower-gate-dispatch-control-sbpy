
export interface DomainRecord {
  id: number;
  code: string;
  name: string;
  status: string;
  version: number;
  description: string;
  facility: string;
  owner: string;
  category: string;
  riskLevel: 'low' | 'medium' | 'high' | 'critical';
  metricValue: number;
  metricUnit: string;
  effectiveAt: string;
  evidence: string;
	relatedCode: string;
	gateState?: 'open' | 'closed' | 'moving' | 'locked';
	submittedBy?: string;
	submittedAt?: string;
	approvedBy?: string;
	approvedAt?: string;
	confirmedBy?: string;
	confirmedAt?: string;
	approvals?: DirectiveApproval[];
  createdAt: string;
  updatedAt: string;
}

export interface DirectiveApproval {
	id: number;
	directiveId: number;
	stage: 'submitted' | 'approved';
	actor: string;
	role: string;
	requestId: string;
	reason: string;
	fromState: string;
	toState: string;
	createdAt: string;
}

export interface PermitDecision {
	id: number;
	permitId: number;
	stage: 'requested' | 'approved' | 'rejected' | 'consumed' | 'invalidated' | 'expired';
	actor: string;
	role: string;
	requestId: string;
	reason: string;
	fromState: string;
	toState: string;
	createdAt: string;
}

export interface DispatchPermit {
	id: number;
	code: string;
	name: string;
	status: string;
	version: number;
	description: string;
	facility: string;
	directiveId: number;
	directiveCode: string;
	gateId: number;
	gateCode: string;
	reservoirCode: string;
	action: 'open' | 'closed';
	validFrom: string;
	validUntil: string;
	observedLevel: number;
	appliedBy: string;
	appliedAt?: string;
	appliedReservoirStatus: string;
	appliedReservoirVersion: number;
	appliedLevel: number;
	appliedGateStatus: string;
	appliedGateVersion: number;
	appliedDirectiveStatus: string;
	appliedDirectiveVersion: number;
	approvedBy: string;
	approvedAt?: string;
	approvedReservoirStatus: string;
	approvedReservoirVersion: number;
	approvedLevel: number;
	approvedGateStatus: string;
	approvedGateVersion: number;
	approvedDirectiveStatus: string;
	approvedDirectiveVersion: number;
	consumedAt?: string;
	rejectedBy: string;
	rejectedAt?: string;
	invalidatedBy: string;
	invalidatedAt?: string;
	decisions?: PermitDecision[];
	createdAt: string;
	updatedAt: string;
}

export interface PageMeta { page: number; pageSize: number; total: number }
export interface ApiEnvelope<T> { data: T; error?: string; message?: string; meta?: PageMeta }
export interface UserSession { token: string; username: string; displayName: string; role: string; expiresIn: number; expiresAt: number }
export interface SessionResponse { username: string; displayName: string; role: string; requestId: string }
export interface AuditLog {
  id: number; requestId: string; actor: string; action: string; entityType: string;
  entityId: number; beforeState: string; afterState: string; detail: string; createdAt: string;
}
export interface AuditSummary {
	since: string; total: number; transitions: number; uniqueActors: number;
	actions: Array<{ action: string; count: number }>;
	entityTypes: Array<{ entityType: string; count: number }>;
}
export interface EntityConfig { key: string; path: string; label: string; statuses: readonly string[] }
