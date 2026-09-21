package dto

import "time"

// CreateDispatchPermit is the operator application contract for 调度许可. The
// linked directive must already be independently approved; status and snapshot
// fields are derived by the service and cannot be supplied by callers.
type CreateDispatchPermit struct {
	Code          string    `json:"code" binding:"required,min=2,max=64"`
	Name          string    `json:"name" binding:"required,min=2,max=160"`
	Description   string    `json:"description" binding:"max=1000"`
	DirectiveCode string    `json:"directiveCode" binding:"required,min=2,max=64"`
	Action        string    `json:"action" binding:"required,oneof=open closed"`
	ValidFrom     time.Time `json:"validFrom" binding:"required"`
	ValidUntil    time.Time `json:"validUntil" binding:"required"`
	ObservedLevel float64   `json:"observedLevel"`
}

// PermitReviewRequest carries the reviewer decision on a requested permit.
type PermitReviewRequest struct {
	ExpectedVersion uint   `json:"expectedVersion" binding:"required"`
	Reason          string `json:"reason" binding:"required,min=3,max=500"`
}

// PermitActivationRequest starts the gate action under an approved permit. The
// service re-verifies water level, gate version and directive version against
// the approval snapshot before anything is allowed to move.
type PermitActivationRequest struct {
	ExpectedVersion uint   `json:"expectedVersion" binding:"required"`
	Reason          string `json:"reason" binding:"required,min=3,max=500"`
}

// PermitInvalidateRequest voluntarily withdraws an active permit or records why
// it can no longer be used.
type PermitInvalidateRequest struct {
	ExpectedVersion uint   `json:"expectedVersion" binding:"required"`
	Reason          string `json:"reason" binding:"required,min=3,max=500"`
}
