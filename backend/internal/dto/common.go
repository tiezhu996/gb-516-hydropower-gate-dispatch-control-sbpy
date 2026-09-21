package dto

import "time"

type PageQuery struct {
	Page     int    `form:"page"`
	PageSize int    `form:"pageSize"`
	Search   string `form:"search"`
	Status   string `form:"status"`
}

type LoginRequest struct {
	Username string `json:"username" binding:"required,min=3,max=80"`
	Password string `json:"password" binding:"required,min=6,max=100"`
}

type LoginResponse struct {
	Token       string `json:"token"`
	ExpiresIn   int64  `json:"expiresIn"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	Role        string `json:"role"`
}

type TransitionRequest struct {
	Status          string `json:"status" binding:"required,max=40"`
	ExpectedVersion uint   `json:"expectedVersion" binding:"required"`
	Reason          string `json:"reason" binding:"required,min=3,max=500"`
}

type AuditSummaryQuery struct {
	WindowHours int `form:"windowHours"`
}

func (q AuditSummaryQuery) Window() time.Duration {
	hours := q.WindowHours
	if hours < 1 {
		hours = 24
	}
	if hours > 24*90 {
		hours = 24 * 90
	}
	return time.Duration(hours) * time.Hour
}

type EntityHistoryQuery struct {
	Limit int `form:"limit"`
}

func (q EntityHistoryQuery) NormalizedLimit() int {
	if q.Limit < 1 {
		return 20
	}
	if q.Limit > 100 {
		return 100
	}
	return q.Limit
}

type SessionResponse struct {
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	Role        string `json:"role"`
	RequestID   string `json:"requestId"`
}

// ApplyDispatchPermit is the write contract for 调度许可申请. The operator only
// names the approved directive, action and validity window; facility/gate/
// reservoir context is resolved authoritatively server side. Status is omitted
// so callers cannot bypass the state machine.
type ApplyDispatchPermit struct {
	Code        string    `json:"code" binding:"required,min=2,max=64"`
	Name        string    `json:"name" binding:"required,min=2,max=160"`
	Description string    `json:"description" binding:"max=1000"`
	Owner       string    `json:"owner" binding:"required,max=120"`
	RelatedCode string    `json:"relatedCode" binding:"required,min=2,max=64"` // approved 操作指令 code
	Action      string    `json:"action" binding:"required,oneof=open closed"`
	ValidFrom   time.Time `json:"validFrom" binding:"required"`
	ValidUntil  time.Time `json:"validUntil" binding:"required"`
	Evidence    string    `json:"evidence" binding:"required,min=3,max=2000"`
}

// PermitDecisionRequest drives reviewer approve/reject of a pending permit.
type PermitDecisionRequest struct {
	ExpectedVersion uint   `json:"expectedVersion" binding:"required"`
	Reason          string `json:"reason" binding:"required,min=3,max=500"`
}

// PermitActivationRequest is issued by the operator right before the gate
// action starts; the server re-verifies water level and gate version.
type PermitActivationRequest struct {
	ExpectedVersion uint   `json:"expectedVersion" binding:"required"`
	Reason          string `json:"reason" binding:"required,min=3,max=500"`
}
