package model

import "time"

// DispatchPermit models 调度许可: an operator applies for a gate action and a
// validity window against an approved 操作指令. The permit carries snapshots at
// both application and approval time so the action-start re-check can detect
// water-level or gate/version drift. A directive may have at most one active
// (requested or approved) permit at any time.
type DispatchPermit struct {
	BaseModel
	Facility    string `json:"facility" gorm:"size:120;index"`
	DirectiveID uint   `json:"directiveId" gorm:"not null;index"`
	// ActiveDirectiveKey holds the directive ID for requested/approved permits
	// and is NULL once the permit reaches a terminal state. Its unique index
	// therefore guarantees at most one active permit per directive even under
	// concurrent requests (NULLs are distinct in standard unique indexes).
	ActiveDirectiveKey *uint     `json:"-" gorm:"uniqueIndex:idx_directive_active"`
	DirectiveCode      string    `json:"directiveCode" gorm:"size:64;index;not null"`
	GateID             uint      `json:"gateId" gorm:"not null;index"`
	GateCode           string    `json:"gateCode" gorm:"size:64;index;not null"`
	ReservoirCode      string    `json:"reservoirCode" gorm:"size:64;index;not null"`
	Action             string    `json:"action" gorm:"size:16;not null;index"`
	ValidFrom          time.Time `json:"validFrom"`
	ValidUntil         time.Time `json:"validUntil"`
	ObservedLevel      float64   `json:"observedLevel"`

	AppliedBy               string     `json:"appliedBy" gorm:"size:80;index"`
	AppliedAt               *time.Time `json:"appliedAt"`
	AppliedReservoirStatus  string     `json:"appliedReservoirStatus" gorm:"size:40"`
	AppliedReservoirVersion uint       `json:"appliedReservoirVersion"`
	AppliedLevel            float64    `json:"appliedLevel"`
	AppliedGateStatus       string     `json:"appliedGateStatus" gorm:"size:32"`
	AppliedGateVersion      uint       `json:"appliedGateVersion"`
	AppliedDirectiveStatus  string     `json:"appliedDirectiveStatus" gorm:"size:40"`
	AppliedDirectiveVersion uint       `json:"appliedDirectiveVersion"`

	ApprovedBy               string     `json:"approvedBy" gorm:"size:80;index"`
	ApprovedAt               *time.Time `json:"approvedAt"`
	ApprovedReservoirStatus  string     `json:"approvedReservoirStatus" gorm:"size:40"`
	ApprovedReservoirVersion uint       `json:"approvedReservoirVersion"`
	ApprovedLevel            float64    `json:"approvedLevel"`
	ApprovedGateStatus       string     `json:"approvedGateStatus" gorm:"size:32"`
	ApprovedGateVersion      uint       `json:"approvedGateVersion"`
	ApprovedDirectiveStatus  string     `json:"approvedDirectiveStatus" gorm:"size:40"`
	ApprovedDirectiveVersion uint       `json:"approvedDirectiveVersion"`

	ConsumedAt    *time.Time `json:"consumedAt"`
	RejectedBy    string     `json:"rejectedBy" gorm:"size:80;index"`
	RejectedAt    *time.Time `json:"rejectedAt"`
	InvalidatedBy string     `json:"invalidatedBy" gorm:"size:80;index"`
	InvalidatedAt *time.Time `json:"invalidatedAt"`

	Decisions []PermitDecision `json:"decisions" gorm:"foreignKey:PermitID;constraint:OnDelete:CASCADE"`
}

func (item *DispatchPermit) GetBase() *BaseModel { return &item.BaseModel }

func (item DispatchPermit) TableName() string { return "dispatch_permits" }

var DispatchPermitInitialStatus = "requested"

// PermitDecision is append-only evidence for each lifecycle decision of a
// 调度许可. No update or delete operation is exposed for this table.
type PermitDecision struct {
	ID        uint      `json:"id" gorm:"primaryKey"`
	PermitID  uint      `json:"permitId" gorm:"not null;index;uniqueIndex:idx_permit_stage"`
	Stage     string    `json:"stage" gorm:"size:32;not null;uniqueIndex:idx_permit_stage"`
	Actor     string    `json:"actor" gorm:"size:80;not null;index"`
	Role      string    `json:"role" gorm:"size:32;not null"`
	RequestID string    `json:"requestId" gorm:"size:64;not null;index"`
	Reason    string    `json:"reason" gorm:"size:500;not null"`
	FromState string    `json:"fromState" gorm:"size:40;not null"`
	ToState   string    `json:"toState" gorm:"size:40;not null"`
	CreatedAt time.Time `json:"createdAt" gorm:"index"`
}
