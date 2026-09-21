package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/blueship581/hydropower-gate-dispatch-control/backend/internal/constants"
	"github.com/blueship581/hydropower-gate-dispatch-control/backend/internal/dto"
	"github.com/blueship581/hydropower-gate-dispatch-control/backend/internal/model"
	"github.com/blueship581/hydropower-gate-dispatch-control/backend/internal/repository"
)

const (
	permitMinValidity   = time.Minute
	permitMaxValidity   = 24 * time.Hour
	permitStartGrace    = time.Minute // clock-skew tolerance when comparing now with validFrom
	permitStageApplied  = "applied"
	permitStageApproved = "approved"
	permitStageRejected = "rejected"
	permitStageActive   = "activated"
	permitStageInvalid  = "invalidated"
)

type DispatchPermitService interface {
	List(context.Context, dto.PageQuery) (repository.Page[model.DispatchPermit], error)
	Get(context.Context, uint) (model.DispatchPermit, error)
	Apply(context.Context, dto.ApplyDispatchPermit, string, string) (model.DispatchPermit, error)
	Approve(context.Context, uint, dto.PermitDecisionRequest, string, string, string) (model.DispatchPermit, error)
	Reject(context.Context, uint, dto.PermitDecisionRequest, string, string, string) (model.DispatchPermit, error)
	Activate(context.Context, uint, dto.PermitActivationRequest, string, string, string) (model.DispatchPermit, error)
	StatusCounts(context.Context) (map[string]int64, error)
}

type dispatchPermitService struct {
	repository repository.DispatchPermitRepository
	directives repository.OperationDirectiveRepository
	gates      repository.GateUnitRepository
	reservoirs repository.ReservoirRepository
	security   SecurityService
}

func NewDispatchPermitService(repo repository.DispatchPermitRepository, directives repository.OperationDirectiveRepository,
	gates repository.GateUnitRepository, reservoirs repository.ReservoirRepository, security SecurityService) DispatchPermitService {
	return &dispatchPermitService{repository: repo, directives: directives, gates: gates, reservoirs: reservoirs, security: security}
}

// permitContext is the authoritative snapshot of the linked aggregates when a
// safety decision is made. evaluateContext re-runs every gatekeeping check —
// approved directive, action match, gate/reservoir ownership, lockout and the
// reservoir water-level window — and never mutates anything.
type permitContext struct {
	directive model.OperationDirective
	gate      model.GateUnit
	reservoir model.Reservoir
}

func permitError(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidInput, fmt.Sprintf(format, args...))
}

func (s *dispatchPermitService) evaluateContext(ctx context.Context, directiveCode, action string) (permitContext, error) {
	var result permitContext
	directive, err := s.directives.GetByCode(ctx, directiveCode)
	if err != nil {
		return result, fmt.Errorf("linked directive %q: %w", directiveCode, err)
	}
	if directive.Status != string(constants.DirectiveStateApproved) {
		return result, permitError("调度许可只能基于已获批指令申请，指令 %s 当前为 %s", directive.Code, directive.Status)
	}
	if !strings.EqualFold(strings.TrimSpace(directive.GateState), action) {
		return result, permitError("申请动作 %s 与已获批指令目标闸门状态 %s 不一致", action, directive.GateState)
	}
	gate, err := s.gates.GetByCode(ctx, directive.RelatedCode)
	if err != nil {
		return result, fmt.Errorf("linked gate %q: %w", directive.RelatedCode, err)
	}
	if !strings.EqualFold(gate.Facility, directive.Facility) {
		return result, permitError("闸门归属不符：闸门 %s 属于 %s，指令属于 %s", gate.Code, gate.Facility, directive.Facility)
	}
	reservoir, err := s.reservoirs.GetByCode(ctx, gate.RelatedCode)
	if err != nil {
		return result, fmt.Errorf("linked reservoir %q: %w", gate.RelatedCode, err)
	}
	if !strings.EqualFold(reservoir.Facility, gate.Facility) {
		return result, permitError("闸门归属不符：库区 %s 属于 %s，闸门属于 %s", reservoir.Code, reservoir.Facility, gate.Facility)
	}
	switch {
	case gate.Status == string(constants.GateStateLocked):
		return result, permitError("闸门 %s 处于闭锁状态，不能申请调度许可", gate.Code)
	case gate.Status == string(constants.GateStateMoving):
		return result, permitError("闸门 %s 正在动作中，不能重复申请调度许可", gate.Code)
	case gate.Status == action:
		return result, permitError("闸门 %s 当前已处于申请动作目标状态 %s", gate.Code, action)
	}
	if !constants.PermitReservoirStates[reservoir.Status] {
		return result, permitError("库区 %s 水位状态为 %s，不在许可窗口（normal/warning）内", reservoir.Code, reservoir.Status)
	}
	result.directive, result.gate, result.reservoir = directive, gate, reservoir
	return result, nil
}

func validatePermitWindow(validFrom, validUntil, now time.Time) error {
	from, until := validFrom.UTC(), validUntil.UTC()
	duration := until.Sub(from)
	switch {
	case !until.After(from):
		return permitError("有效期结束时间必须晚于开始时间")
	case duration < permitMinValidity:
		return permitError("调度许可有效期不得短于 %s", permitMinValidity)
	case duration > permitMaxValidity:
		return permitError("调度许可有效期不得超过 %s", permitMaxValidity)
	case from.After(now.Add(permitStartGrace)):
		return permitError("生效开始时间不能晚于当前时间")
	case !until.After(now):
		return permitError("申请的有效期已结束")
	}
	return nil
}

func (s *dispatchPermitService) List(ctx context.Context, query dto.PageQuery) (repository.Page[model.DispatchPermit], error) {
	return s.repository.List(ctx, query)
}

func (s *dispatchPermitService) Get(ctx context.Context, id uint) (model.DispatchPermit, error) {
	return s.repository.Get(ctx, id)
}

func (s *dispatchPermitService) Apply(ctx context.Context, input dto.ApplyDispatchPermit, actor, requestID string) (model.DispatchPermit, error) {
	if strings.TrimSpace(input.Code) == "" || strings.TrimSpace(input.Name) == "" || strings.TrimSpace(input.Owner) == "" {
		return model.DispatchPermit{}, ErrInvalidInput
	}
	now := time.Now().UTC()
	validFrom, validUntil := input.ValidFrom.UTC(), input.ValidUntil.UTC()
	if err := validatePermitWindow(validFrom, validUntil, now); err != nil {
		return model.DispatchPermit{}, err
	}
	directiveCode := strings.ToUpper(strings.TrimSpace(input.RelatedCode))
	action := strings.TrimSpace(input.Action)
	checked, err := s.evaluateContext(ctx, directiveCode, action)
	if err != nil {
		return model.DispatchPermit{}, err
	}
	slot := checked.gate.Code
	evidence := strings.TrimSpace(input.Evidence)
	item := model.DispatchPermit{
		BaseModel: model.BaseModel{
			Code: strings.ToUpper(strings.TrimSpace(input.Code)), Name: strings.TrimSpace(input.Name),
			Status: model.DispatchPermitInitialStatus, Version: 1, Description: strings.TrimSpace(input.Description),
		},
		Facility: checked.directive.Facility, Owner: strings.TrimSpace(input.Owner),
		Category: checked.directive.Category, RiskLevel: checked.directive.RiskLevel,
		MetricValue: checked.reservoir.MetricValue, MetricUnit: checked.reservoir.MetricUnit, Evidence: evidence,
		RelatedCode: directiveCode, GateCode: checked.gate.Code, ReservoirCode: checked.reservoir.Code,
		Action: action, ValidFrom: validFrom, ValidUntil: validUntil,
		AppliedBy: actor, AppliedAt: &now, ActiveSlot: &slot,
		SnapshotReservoirStatus: checked.reservoir.Status, SnapshotGateStatus: checked.gate.Status,
		SnapshotGateVersion: checked.gate.Version, SnapshotReservoirVersion: checked.reservoir.Version,
		SnapshotDirectiveVersion: checked.directive.Version,
	}
	decision := &model.PermitDecision{Stage: permitStageApplied, Actor: actor, Role: model.RoleOperator,
		RequestID: requestID, Reason: evidence, FromState: "", ToState: item.Status, CreatedAt: now}
	audit := &model.AuditLog{Actor: actor, RequestID: requestID, Action: "permit_apply",
		EntityType: "DispatchPermit", BeforeState: "", AfterState: item.Status, Detail: evidence, CreatedAt: now}
	if err := s.security.WithinTransaction(ctx, func(txCtx context.Context) error {
		// Expired permits release the unique gate slot first so re-application works.
		if _, err := s.repository.ExpireStaleSlots(txCtx, now, requestID); err != nil {
			return fmt.Errorf("expire stale permits: %w", err)
		}
		active, err := s.repository.CountActiveSlot(txCtx, checked.gate.Code)
		if err != nil {
			return fmt.Errorf("check existing permit: %w", err)
		}
		if active > 0 {
			return fmt.Errorf("%w: 闸门 %s 已有一份生效中的调度许可，禁止重复申请", ErrDuplicatePermit, checked.gate.Code)
		}
		return s.repository.CreateWithDecision(txCtx, &item, decision, audit)
	}); err != nil {
		return model.DispatchPermit{}, fmt.Errorf("apply 调度许可: %w", err)
	}
	return s.repository.Get(ctx, item.ID)
}

// decide implements reviewer approve/reject. Approval re-verifies the live
// water level, ownership, lock state and the gate version captured at apply.
func (s *dispatchPermitService) decide(ctx context.Context, id uint, input dto.PermitDecisionRequest,
	approve bool, actor, role, requestID string) (model.DispatchPermit, error) {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return model.DispatchPermit{}, err
	}
	if current.Status != string(constants.PermitStatePending) {
		return model.DispatchPermit{}, fmt.Errorf("%w: 只有待批准申请可以复核，当前为 %s", ErrInvalidTransition, current.Status)
	}
	// 安全复核员只能批准（驳回）他人的有效申请。
	if strings.EqualFold(strings.TrimSpace(current.AppliedBy), actor) {
		return model.DispatchPermit{}, ErrTwoPersonRequired
	}
	target, stage := string(constants.PermitStateApproved), permitStageApproved
	if !approve {
		target, stage = string(constants.PermitStateRejected), permitStageRejected
	}
	now := time.Now().UTC()
	var approvedGateVersion uint
	if approve {
		checked, evalErr := s.evaluateContext(ctx, current.RelatedCode, current.Action)
		if evalErr != nil {
			return model.DispatchPermit{}, evalErr
		}
		switch {
		case now.Before(current.ValidFrom.Add(-permitStartGrace)):
			return model.DispatchPermit{}, permitError("申请尚未进入有效期，不能批准")
		case !now.Before(current.ValidUntil):
			return model.DispatchPermit{}, permitError("申请已超过有效期，不能批准")
		case checked.gate.Version != current.SnapshotGateVersion:
			return model.DispatchPermit{}, permitError("闸门 %s 版本已从 %d 变为 %d，申请条件已变化，请重新申请",
				checked.gate.Code, current.SnapshotGateVersion, checked.gate.Version)
		}
		approvedGateVersion = checked.gate.Version
	}
	before := current.Status
	current.Status = target
	if approve {
		// An approved permit keeps occupying the unique gate slot until activated,
		// invalidated or expired.
		slot := current.GateCode
		current.ActiveSlot = &slot
		current.ApprovedBy, current.ApprovedAt = actor, &now
		current.ApprovedGateVersion = approvedGateVersion
	} else {
		current.ActiveSlot = nil
		current.ClosedBy, current.ClosedAt = actor, &now
	}
	current.Version = input.ExpectedVersion + 1
	current.UpdatedAt = now
	reason := strings.TrimSpace(input.Reason)
	decision := &model.PermitDecision{Stage: stage, Actor: actor, Role: role, RequestID: requestID,
		Reason: reason, FromState: before, ToState: target, CreatedAt: now}
	audit := &model.AuditLog{Actor: actor, RequestID: requestID,
		Action:     map[bool]string{true: "permit_approve", false: "permit_reject"}[approve],
		EntityType: "DispatchPermit", EntityID: id, BeforeState: before, AfterState: target,
		Detail: reason, CreatedAt: now}
	if err := s.repository.SaveWithDecision(ctx, id, input.ExpectedVersion, &current, decision, audit); err != nil {
		return model.DispatchPermit{}, fmt.Errorf("%s 调度许可: %w", stage, err)
	}
	return s.repository.Get(ctx, id)
}

func (s *dispatchPermitService) Approve(ctx context.Context, id uint, input dto.PermitDecisionRequest, actor, role, requestID string) (model.DispatchPermit, error) {
	return s.decide(ctx, id, input, true, actor, role, requestID)
}

func (s *dispatchPermitService) Reject(ctx context.Context, id uint, input dto.PermitDecisionRequest, actor, role, requestID string) (model.DispatchPermit, error) {
	return s.decide(ctx, id, input, false, actor, role, requestID)
}

// Activate is called right before the gate action starts: water level and gate
// version are re-verified against approval-time values. Any drift invalidates
// the permit and requires a fresh application without touching the directive or
// the gate aggregate.
func (s *dispatchPermitService) Activate(ctx context.Context, id uint, input dto.PermitActivationRequest, actor, role, requestID string) (model.DispatchPermit, error) {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return model.DispatchPermit{}, err
	}
	if current.Status != string(constants.PermitStateApproved) {
		return model.DispatchPermit{}, fmt.Errorf("%w: 只有已获批许可可以开始动作，当前为 %s", ErrInvalidTransition, current.Status)
	}
	now := time.Now().UTC()
	if now.Before(current.ValidFrom.Add(-permitStartGrace)) {
		return model.DispatchPermit{}, permitError("许可尚未进入有效期")
	}
	var failureReason string
	if !now.Before(current.ValidUntil) {
		failureReason = fmt.Sprintf("许可已超过有效期（截止 %s）", current.ValidUntil.Format(time.RFC3339))
	} else if checked, evalErr := s.evaluateContext(ctx, current.RelatedCode, current.Action); evalErr != nil {
		failureReason = strings.TrimPrefix(evalErr.Error(), ErrInvalidInput.Error()+": ")
	} else {
		switch {
		case checked.gate.Version != current.ApprovedGateVersion:
			failureReason = fmt.Sprintf("闸门 %s 版本已从 %d 变为 %d，动作前复核不一致",
				checked.gate.Code, current.ApprovedGateVersion, checked.gate.Version)
		case checked.reservoir.Version != current.SnapshotReservoirVersion:
			failureReason = fmt.Sprintf("库区 %s 版本已从 %d 变为 %d，水位条件可能已变化",
				checked.reservoir.Code, current.SnapshotReservoirVersion, checked.reservoir.Version)
		}
	}
	if failureReason != "" {
		if saveErr := s.invalidate(ctx, &current, failureReason, actor, requestID, now); saveErr != nil {
			return model.DispatchPermit{}, saveErr
		}
		return model.DispatchPermit{}, fmt.Errorf("%w: %s", ErrPermitInvalidated, failureReason)
	}
	before := current.Status
	current.Status = string(constants.PermitStateActive)
	current.ActivatedBy, current.ActivatedAt = actor, &now
	current.Version = input.ExpectedVersion + 1
	current.UpdatedAt = now
	reason := strings.TrimSpace(input.Reason)
	decision := &model.PermitDecision{Stage: permitStageActive, Actor: actor, Role: role, RequestID: requestID,
		Reason: reason, FromState: before, ToState: current.Status, CreatedAt: now}
	audit := &model.AuditLog{Actor: actor, RequestID: requestID, Action: "permit_activate",
		EntityType: "DispatchPermit", EntityID: id, BeforeState: before, AfterState: current.Status,
		Detail: reason, CreatedAt: now}
	if err := s.repository.SaveWithDecision(ctx, id, input.ExpectedVersion, &current, decision, audit); err != nil {
		return model.DispatchPermit{}, fmt.Errorf("activate 调度许可: %w", err)
	}
	return s.repository.Get(ctx, id)
}

// invalidate persists the guarded invalidation with evidence. The audit actor
// stays the requesting operator while the decision is attributed to the system
// guard so the trail distinguishes automated enforcement.
func (s *dispatchPermitService) invalidate(ctx context.Context, current *model.DispatchPermit, reason, actor, requestID string, now time.Time) error {
	before, target := current.Status, string(constants.PermitStateInvalidated)
	current.Status, current.ActiveSlot, current.InvalidReason = target, nil, reason
	current.ClosedBy, current.ClosedAt = "system", &now
	current.Version, current.UpdatedAt = current.Version+1, now
	decision := &model.PermitDecision{Stage: permitStageInvalid, Actor: "system", Role: "system", RequestID: requestID,
		Reason: reason, FromState: before, ToState: target, CreatedAt: now}
	audit := &model.AuditLog{Actor: actor, RequestID: requestID, Action: "permit_invalidate",
		EntityType: "DispatchPermit", EntityID: current.ID, BeforeState: before, AfterState: target,
		Detail: reason, CreatedAt: now}
	if err := s.repository.SaveWithDecision(ctx, current.ID, current.Version-1, current, decision, audit); err != nil {
		return fmt.Errorf("invalidate 调度许可: %w", err)
	}
	return nil
}

func (s *dispatchPermitService) StatusCounts(ctx context.Context) (map[string]int64, error) {
	return s.repository.CountByStatus(ctx)
}
