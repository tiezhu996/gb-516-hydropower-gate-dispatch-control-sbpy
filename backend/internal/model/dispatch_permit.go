package model

import "time"

// DispatchPermit models 调度许可: an operator requests one gate action and a
// validity window against an already approved 操作指令.
type DispatchPermit struct {
	BaseModel
	Facility      string    `json:"facility" gorm:"size:120;index"`
	Owner         string    `json:"owner" gorm:"size:120;index"`
	Category      string    `json:"category" gorm:"size:80;index"`
	RiskLevel     string    `json:"riskLevel" gorm:"size:32;index"`
	MetricValue   float64   `json:"metricValue"`
	MetricUnit    string    `json:"metricUnit" gorm:"size:24"`
	Evidence      string    `json:"evidence" gorm:"size:2000"`
	RelatedCode   string    `json:"relatedCode" gorm:"size:64;index"`       // approved 操作指令 code
	GateCode      string    `json:"gateCode" gorm:"size:64;index;not null"` // target gate
	ReservoirCode string    `json:"reservoirCode" gorm:"size:64;index;not null"`
	Action        string    `json:"action" gorm:"size:32;not null;index"` // requested GateState: open/closed
	ValidFrom     time.Time `json:"validFrom" gorm:"not null;index"`
	ValidUntil    time.Time `json:"validUntil" gorm:"not null;index"`

	AppliedBy     string     `json:"appliedBy" gorm:"size:80;index"`
	AppliedAt     *time.Time `json:"appliedAt"`
	ApprovedBy    string     `json:"approvedBy" gorm:"size:80;index"`
	ApprovedAt    *time.Time `json:"approvedAt"`
	ActivatedBy   string     `json:"activatedBy" gorm:"size:80;index"`
	ActivatedAt   *time.Time `json:"activatedAt"`
	ClosedBy      string     `json:"closedBy" gorm:"size:80;index"`
	ClosedAt      *time.Time `json:"closedAt"`
	InvalidReason string     `json:"invalidReason" gorm:"size:500"`

	// snapshots fixed at application time; activation re-checks live versions
	SnapshotReservoirStatus  string `json:"snapshotReservoirStatus" gorm:"size:40;not null"`
	SnapshotGateStatus       string `json:"snapshotGateStatus" gorm:"size:40;not null"`
	SnapshotGateVersion      uint   `json:"snapshotGateVersion" gorm:"not null"`
	SnapshotReservoirVersion uint   `json:"snapshotReservoirVersion" gorm:"not null"`
	SnapshotDirectiveVersion uint   `json:"snapshotDirectiveVersion" gorm:"not null"`
	ApprovedGateVersion      uint   `json:"approvedGateVersion" gorm:"not null;default:0"`

	// ActiveSlot holds the gate code while the permit occupies the unique
	// execution slot (pending/approved); NULL never collides.
	ActiveSlot *string `json:"-" gorm:"column:active_slot;size:64;uniqueIndex:ux_permit_active_slot"`

	Decisions []PermitDecision `json:"decisions" gorm:"foreignKey:PermitID;constraint:OnDelete:CASCADE"`
}

func (item *DispatchPermit) GetBase() *BaseModel { return &item.BaseModel }

func (item DispatchPermit) TableName() string { return "dispatch_permits" }

var DispatchPermitInitialStatus = "pending"

// PermitDecision is append-only evidence for the two-person permit decision and
// the pre-action activation outcome. No update or delete is exposed.
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
