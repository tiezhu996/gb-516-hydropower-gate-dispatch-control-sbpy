package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/blueship581/hydropower-gate-dispatch-control/backend/internal/constants"
	"github.com/blueship581/hydropower-gate-dispatch-control/backend/internal/dto"
	"github.com/blueship581/hydropower-gate-dispatch-control/backend/internal/model"
	"github.com/blueship581/hydropower-gate-dispatch-control/backend/internal/repository"
)

const (
	// PermitLevelTolerance is the maximum deviation in the reservoir level unit
	// between the dispatch reference and the observed/current water level.
	PermitLevelTolerance = 0.5
	permitMinWindow      = 5 * time.Minute
	permitMaxWindow      = 48 * time.Hour
	permitFromSkew       = 30 * time.Minute
)

// errPermitConditionsChanged is a private rollback marker: the activation
// transaction aborts so no operational state is rewritten, after which the
// permit is invalidated in a separate transaction.
var errPermitConditionsChanged = errors.New("dispatch conditions changed before action start")

type DispatchPermitService interface {
	List(context.Context, dto.PageQuery) (repository.Page[model.DispatchPermit], error)
	Get(context.Context, uint) (model.DispatchPermit, error)
	Apply(context.Context, dto.CreateDispatchPermit, string, string) (model.DispatchPermit, error)
	Approve(context.Context, uint, dto.PermitReviewRequest, string, string, string) (model.DispatchPermit, error)
	Reject(context.Context, uint, dto.PermitReviewRequest, string, string, string) (model.DispatchPermit, error)
	Activate(context.Context, uint, dto.PermitActivationRequest, string, string, string) (model.DispatchPermit, error)
	Invalidate(context.Context, uint, dto.PermitInvalidateRequest, string, string, string) (model.DispatchPermit, error)
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
	gates repository.GateUnitRepository, reservoirs repository.ReservoirRepository, security SecurityService,
) DispatchPermitService {
	return &dispatchPermitService{repository: repo, directives: directives, gates: gates, reservoirs: reservoirs, security: security}
}

func (s *dispatchPermitService) List(ctx context.Context, query dto.PageQuery) (repository.Page[model.DispatchPermit], error) {
	return s.repository.List(ctx, query)
}

func (s *dispatchPermitService) Get(ctx context.Context, id uint) (model.DispatchPermit, error) {
	return s.repository.Get(ctx, id)
}

func (s *dispatchPermitService) StatusCounts(ctx context.Context) (map[string]int64, error) {
	return s.repository.CountByStatus(ctx)
}

// Apply records an operator's permit application against an already approved
// directive. Water-level window, gate ownership and interlock/lock state are
// checked here and snapshots are stored for the two later re-checks.
func (s *dispatchPermitService) Apply(ctx context.Context, input dto.CreateDispatchPermit, actor, requestID string) (model.DispatchPermit, error) {
	if strings.TrimSpace(input.Code) == "" || strings.TrimSpace(input.Name) == "" {
		return model.DispatchPermit{}, fmt.Errorf("%w: permit code and name are required", ErrInvalidInput)
	}
	now := time.Now().UTC()
	from, until := input.ValidFrom.UTC(), input.ValidUntil.UTC()
	if err := validatePermitWindow(now, from, until); err != nil {
		return model.DispatchPermit{}, err
	}
	directive, gate, reservoir, err := s.loadDispatchChain(ctx, input.DirectiveCode)
	if err != nil {
		return model.DispatchPermit{}, err
	}
	if directive.Status != string(constants.DirectiveStateApproved) {
		return model.DispatchPermit{}, fmt.Errorf("%w: permit application requires an approved directive, directive %s is %s",
			ErrInvalidInput, directive.Code, directive.Status)
	}
	if err := ensureGateReady(gate, directive.Facility); err != nil {
		return model.DispatchPermit{}, err
	}
	if err := ensureReservoirDispatchable(reservoir, gate.Facility, input.ObservedLevel); err != nil {
		return model.DispatchPermit{}, err
	}
	action := strings.TrimSpace(input.Action)
	if !strings.EqualFold(directive.GateState, action) {
		return model.DispatchPermit{}, fmt.Errorf("%w: requested action %s does not match approved directive target %s",
			ErrInvalidInput, action, directive.GateState)
	}

	activeKey := directive.ID
	item := model.DispatchPermit{
		BaseModel: model.BaseModel{
			Code: strings.ToUpper(strings.TrimSpace(input.Code)), Name: strings.TrimSpace(input.Name),
			Status: model.DispatchPermitInitialStatus, Version: 1, Description: strings.TrimSpace(input.Description),
		},
		Facility:                directive.Facility,
		DirectiveID:             directive.ID,
		ActiveDirectiveKey:      &activeKey,
		DirectiveCode:           directive.Code,
		GateID:                  gate.ID,
		GateCode:                gate.Code,
		ReservoirCode:           reservoir.Code,
		Action:                  action,
		ValidFrom:               from,
		ValidUntil:              until,
		ObservedLevel:           input.ObservedLevel,
		AppliedBy:               actor,
		AppliedAt:               &now,
		AppliedReservoirStatus:  reservoir.Status,
		AppliedReservoirVersion: reservoir.Version,
		AppliedLevel:            reservoir.MetricValue,
		AppliedGateStatus:       gate.Status,
		AppliedGateVersion:      gate.Version,
		AppliedDirectiveStatus:  directive.Status,
		AppliedDirectiveVersion: directive.Version,
	}
	decision := &model.PermitDecision{Stage: "requested", Actor: actor, Role: model.RoleOperator, RequestID: requestID,
		Reason: "操作员申请调度许可", FromState: "", ToState: model.DispatchPermitInitialStatus, CreatedAt: now}
	if err := s.security.WithinTransaction(ctx, func(txCtx context.Context) error {
		active, countErr := s.repository.CountActiveByDirectiveID(txCtx, directive.ID)
		if countErr != nil {
			return countErr
		}
		if active > 0 {
			return ErrPermitConflict
		}
		if err := s.repository.CreateWithDecision(txCtx, &item, decision); err != nil {
			if errors.Is(err, repository.ErrActivePermitExists) {
				return ErrPermitConflict
			}
			return err
		}
		return s.security.Audit(txCtx, actor, requestID, "permit_apply", "DispatchPermit", item.ID, "", item.Status,
			fmt.Sprintf("申请 %s 动作，有效期至 %s", action, until.Format(time.RFC3339)))
	}); err != nil {
		if errors.Is(err, ErrPermitConflict) {
			return model.DispatchPermit{}, err
		}
		return model.DispatchPermit{}, fmt.Errorf("apply 调度许可: %w", err)
	}
	return s.repository.Get(ctx, item.ID)
}

// Approve is restricted to a safety reviewer other than the applicant. Every
// dispatch condition is re-read inside the transaction; drift since
// application refuses the approval without rewriting any state.
func (s *dispatchPermitService) Approve(ctx context.Context, id uint, input dto.PermitReviewRequest, actor, role, requestID string) (model.DispatchPermit, error) {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return model.DispatchPermit{}, err
	}
	if current.Version != input.ExpectedVersion {
		return model.DispatchPermit{}, repository.ErrVersionConflict
	}
	if current.Status != string(constants.PermitStateRequested) {
		return model.DispatchPermit{}, fmt.Errorf("%w: only requested permits can be approved, permit is %s", ErrInvalidTransition, current.Status)
	}
	if err := s.ensureNotLapsed(ctx, &current, requestID); err != nil {
		return model.DispatchPermit{}, err
	}
	if strings.EqualFold(strings.TrimSpace(current.AppliedBy), actor) {
		return model.DispatchPermit{}, ErrTwoPersonRequired
	}
	reason := strings.TrimSpace(input.Reason)
	if err := s.security.WithinTransaction(ctx, func(txCtx context.Context) error {
		locked, lockErr := s.repository.Get(txCtx, id)
		if lockErr != nil {
			return lockErr
		}
		if locked.Status != string(constants.PermitStateRequested) || locked.Version != input.ExpectedVersion {
			return repository.ErrVersionConflict
		}
		directive, gate, reservoir, loadErr := s.loadDispatchChain(txCtx, current.DirectiveCode)
		if loadErr != nil {
			return loadErr
		}
		if checkErr := s.validateAgainstApplication(locked, directive, gate, reservoir); checkErr != nil {
			return checkErr
		}
		now := time.Now().UTC()
		locked.Status = string(constants.PermitStateApproved)
		locked.Version = input.ExpectedVersion + 1
		locked.UpdatedAt = now
		locked.ApprovedBy = actor
		locked.ApprovedAt = &now
		locked.ApprovedReservoirStatus = reservoir.Status
		locked.ApprovedReservoirVersion = reservoir.Version
		locked.ApprovedLevel = reservoir.MetricValue
		locked.ApprovedGateStatus = gate.Status
		locked.ApprovedGateVersion = gate.Version
		locked.ApprovedDirectiveStatus = directive.Status
		locked.ApprovedDirectiveVersion = directive.Version
		decision := &model.PermitDecision{Stage: "approved", Actor: actor, Role: role, RequestID: requestID,
			Reason: reason, FromState: string(constants.PermitStateRequested), ToState: string(constants.PermitStateApproved), CreatedAt: now}
		if err := s.repository.SaveTransition(txCtx, id, input.ExpectedVersion, &locked, decision); err != nil {
			return err
		}
		return s.security.Audit(txCtx, actor, requestID, "permit_approve", "DispatchPermit", id,
			string(constants.PermitStateRequested), string(constants.PermitStateApproved), reason)
	}); err != nil {
		return model.DispatchPermit{}, fmt.Errorf("approve 调度许可: %w", err)
	}
	return s.repository.Get(ctx, id)
}

// Reject closes a requested permit with reviewer evidence. It frees the
// directive so the operator can apply again.
func (s *dispatchPermitService) Reject(ctx context.Context, id uint, input dto.PermitReviewRequest, actor, role, requestID string) (model.DispatchPermit, error) {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return model.DispatchPermit{}, err
	}
	if current.Version != input.ExpectedVersion {
		return model.DispatchPermit{}, repository.ErrVersionConflict
	}
	if current.Status != string(constants.PermitStateRequested) {
		return model.DispatchPermit{}, fmt.Errorf("%w: only requested permits can be rejected, permit is %s", ErrInvalidTransition, current.Status)
	}
	if err := s.ensureNotLapsed(ctx, &current, requestID); err != nil {
		return model.DispatchPermit{}, err
	}
	if strings.EqualFold(strings.TrimSpace(current.AppliedBy), actor) {
		return model.DispatchPermit{}, ErrTwoPersonRequired
	}
	now := time.Now().UTC()
	target := string(constants.PermitStateRejected)
	current.Status = target
	current.ActiveDirectiveKey = nil
	current.Version = input.ExpectedVersion + 1
	current.UpdatedAt = now
	current.RejectedBy = actor
	current.RejectedAt = &now
	decision := &model.PermitDecision{Stage: "rejected", Actor: actor, Role: role, RequestID: requestID,
		Reason: strings.TrimSpace(input.Reason), FromState: string(constants.PermitStateRequested), ToState: target, CreatedAt: now}
	if err := s.security.WithinTransaction(ctx, func(txCtx context.Context) error {
		if err := s.repository.SaveTransition(txCtx, id, input.ExpectedVersion, &current, decision); err != nil {
			return err
		}
		return s.security.Audit(txCtx, actor, requestID, "permit_reject", "DispatchPermit", id,
			string(constants.PermitStateRequested), target, strings.TrimSpace(input.Reason))
	}); err != nil {
		return model.DispatchPermit{}, fmt.Errorf("reject 调度许可: %w", err)
	}
	return s.repository.Get(ctx, id)
}

// Activate starts the gate action. Water level, gate version and directive
// version are re-verified against the approval snapshot; any drift atomically
// invalidates the permit and leaves directive and gate untouched.
func (s *dispatchPermitService) Activate(ctx context.Context, id uint, input dto.PermitActivationRequest, actor, role, requestID string) (model.DispatchPermit, error) {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return model.DispatchPermit{}, err
	}
	if current.Version != input.ExpectedVersion {
		return model.DispatchPermit{}, repository.ErrVersionConflict
	}
	if current.Status != string(constants.PermitStateApproved) {
		return model.DispatchPermit{}, fmt.Errorf("%w: only approved permits can activate an action, permit is %s", ErrInvalidTransition, current.Status)
	}
	now := time.Now().UTC()
	if now.After(current.ValidUntil) {
		if lapseErr := s.advanceVersioned(ctx, &current, current.Version, string(constants.PermitStateExpired),
			actor, role, requestID, "permit expired before action start", "expired", "permit_expire"); lapseErr != nil {
			return model.DispatchPermit{}, lapseErr
		}
		return model.DispatchPermit{}, fmt.Errorf("%w: permit expired at %s; reapply for a new permit", ErrInvalidInput, current.ValidUntil.Format(time.RFC3339))
	}
	if now.Before(current.ValidFrom) {
		return model.DispatchPermit{}, fmt.Errorf("%w: permit is not effective before %s", ErrInvalidInput, current.ValidFrom.Format(time.RFC3339))
	}

	var driftReason string
	var activationErr error
	if txErr := s.security.WithinTransaction(ctx, func(txCtx context.Context) error {
		directive, gate, reservoir, loadErr := s.loadDispatchChain(txCtx, current.DirectiveCode)
		if loadErr != nil {
			return loadErr
		}
		if reason := activationDriftReason(current, directive, gate, reservoir); reason != "" {
			driftReason = reason
			return errPermitConditionsChanged
		}
		gateTarget := string(constants.GateStateMoving)
		if gate.Status != gateTarget && !constants.CanTransition(constants.GateUnitTransitions, gate.Status, gateTarget) {
			return fmt.Errorf("%w: gate %s cannot move from %s to %s", ErrInvalidTransition, gate.Code, gate.Status, gateTarget)
		}

		locked, lockErr := s.repository.Get(txCtx, id)
		if lockErr != nil {
			return lockErr
		}
		if locked.Status != string(constants.PermitStateApproved) || locked.Version != input.ExpectedVersion {
			return repository.ErrVersionConflict
		}
		permitBefore := locked.Status
		locked.Status = string(constants.PermitStateConsumed)
		locked.ActiveDirectiveKey = nil
		locked.Version = input.ExpectedVersion + 1
		locked.UpdatedAt = now
		locked.ConsumedAt = &now
		permitDecision := &model.PermitDecision{Stage: "consumed", Actor: actor, Role: role, RequestID: requestID,
			Reason: strings.TrimSpace(input.Reason), FromState: permitBefore, ToState: string(constants.PermitStateConsumed), CreatedAt: now}
		if err := s.repository.SaveTransition(txCtx, id, input.ExpectedVersion, &locked, permitDecision); err != nil {
			return err
		}

		directiveBefore := directive.Status
		directive.Status = string(constants.DirectiveStateExecuting)
		directive.Version++
		directive.UpdatedAt = now
		if err := s.directives.Update(txCtx, directive.ID, directive.Version-1, &directive); err != nil {
			return err
		}
		if err := s.security.Audit(txCtx, actor, requestID, "transition", "OperationDirective", directive.ID,
			directiveBefore, string(constants.DirectiveStateExecuting), strings.TrimSpace(input.Reason)); err != nil {
			return err
		}

		if gate.Status != gateTarget {
			gateBefore := gate.Status
			gate.Status = gateTarget
			gate.Version++
			gate.UpdatedAt = now
			if err := s.gates.Update(txCtx, gate.ID, gate.Version-1, &gate); err != nil {
				return err
			}
			if err := s.security.Audit(txCtx, actor, requestID, "permit_activation", "GateUnit", gate.ID,
				gateBefore, gateTarget, strings.TrimSpace(input.Reason)); err != nil {
				return err
			}
		}
		return s.security.Audit(txCtx, actor, requestID, "permit_activate", "DispatchPermit", id,
			permitBefore, string(constants.PermitStateConsumed), strings.TrimSpace(input.Reason))
	}); txErr != nil {
		activationErr = txErr
	}
	if driftReason != "" {
		// The activation transaction was rolled back, so directive and gate were
		// not rewritten. Record the permit invalidation in its own transaction.
		current, reloadErr := s.repository.Get(ctx, id)
		if reloadErr != nil {
			return model.DispatchPermit{}, reloadErr
		}
		if invalidateErr := s.advanceVersioned(ctx, &current, current.Version, string(constants.PermitStateInvalidated),
			actor, role, requestID, driftReason, "invalidated", "permit_invalidate"); invalidateErr != nil {
			return model.DispatchPermit{}, invalidateErr
		}
		return model.DispatchPermit{}, fmt.Errorf("%w: %s; permit invalidated, reapply for a new permit", ErrInvalidInput, driftReason)
	}
	if activationErr != nil {
		return model.DispatchPermit{}, fmt.Errorf("activate 调度许可: %w", activationErr)
	}
	return s.repository.Get(ctx, id)
}

// Invalidate withdraws a requested or approved permit. Terminal permits cannot
// be rewritten.
func (s *dispatchPermitService) Invalidate(ctx context.Context, id uint, input dto.PermitInvalidateRequest, actor, role, requestID string) (model.DispatchPermit, error) {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return model.DispatchPermit{}, err
	}
	if current.Version != input.ExpectedVersion {
		return model.DispatchPermit{}, repository.ErrVersionConflict
	}
	before := current.Status
	if before != string(constants.PermitStateRequested) && before != string(constants.PermitStateApproved) {
		return model.DispatchPermit{}, fmt.Errorf("%w: permit in %s state can no longer be invalidated", ErrImmutableState, before)
	}
	if err := s.advanceVersioned(ctx, &current, input.ExpectedVersion, string(constants.PermitStateInvalidated),
		actor, role, requestID, strings.TrimSpace(input.Reason), "invalidated", "permit_invalidate"); err != nil {
		return model.DispatchPermit{}, err
	}
	return s.repository.Get(ctx, id)
}

func (s *dispatchPermitService) loadDispatchChain(ctx context.Context, directiveCode string) (model.OperationDirective, model.GateUnit, model.Reservoir, error) {
	directive, err := s.directives.GetByCode(ctx, directiveCode)
	if err != nil {
		return model.OperationDirective{}, model.GateUnit{}, model.Reservoir{}, fmt.Errorf("linked directive %q: %w", directiveCode, err)
	}
	gate, err := s.gates.GetByCode(ctx, directive.RelatedCode)
	if err != nil {
		return model.OperationDirective{}, model.GateUnit{}, model.Reservoir{}, fmt.Errorf("linked gate %q: %w", directive.RelatedCode, err)
	}
	reservoir, err := s.reservoirs.GetByCode(ctx, gate.RelatedCode)
	if err != nil {
		return model.OperationDirective{}, model.GateUnit{}, model.Reservoir{}, fmt.Errorf("linked reservoir %q: %w", gate.RelatedCode, err)
	}
	return directive, gate, reservoir, nil
}

func ensureGateReady(gate model.GateUnit, facility string) error {
	if !strings.EqualFold(strings.TrimSpace(gate.Facility), strings.TrimSpace(facility)) {
		return fmt.Errorf("%w: gate belongs to another facility and cannot serve this directive", ErrInvalidInput)
	}
	switch gate.Status {
	case string(constants.GateStateLocked):
		return fmt.Errorf("%w: gate %s is locked; clear the interlock before applying for a permit", ErrInvalidInput, gate.Code)
	case string(constants.GateStateMoving):
		return fmt.Errorf("%w: gate %s is already moving; wait for the running action to finish", ErrInvalidInput, gate.Code)
	}
	return nil
}

func ensureReservoirDispatchable(reservoir model.Reservoir, facility string, observed float64) error {
	if !strings.EqualFold(strings.TrimSpace(reservoir.Facility), strings.TrimSpace(facility)) {
		return fmt.Errorf("%w: reservoir and gate belong to different facilities", ErrInvalidInput)
	}
	if reservoir.Status == "restricted" {
		return fmt.Errorf("%w: reservoir %s is restricted; gate actions are not permitted", ErrInvalidInput, reservoir.Code)
	}
	if math.Abs(observed-reservoir.MetricValue) > PermitLevelTolerance {
		return fmt.Errorf("%w: observed water level %.2f is outside the permitted window %.2f ± %.2f",
			ErrInvalidInput, observed, reservoir.MetricValue, PermitLevelTolerance)
	}
	return nil
}

func (s *dispatchPermitService) validateAgainstApplication(permit model.DispatchPermit, directive model.OperationDirective, gate model.GateUnit, reservoir model.Reservoir) error {
	if directive.ID != permit.DirectiveID {
		return fmt.Errorf("%w: directive linkage changed after application", ErrInvalidInput)
	}
	if directive.Status != string(constants.DirectiveStateApproved) {
		return fmt.Errorf("%w: directive is no longer approved (now %s)", ErrInvalidInput, directive.Status)
	}
	if directive.Version != permit.AppliedDirectiveVersion {
		return fmt.Errorf("%w: directive changed (version %d -> %d) after application", ErrInvalidInput, permit.AppliedDirectiveVersion, directive.Version)
	}
	if err := ensureGateReady(gate, directive.Facility); err != nil {
		return err
	}
	if gate.Version != permit.AppliedGateVersion {
		return fmt.Errorf("%w: gate changed (version %d -> %d) after application", ErrInvalidInput, permit.AppliedGateVersion, gate.Version)
	}
	if err := ensureReservoirDispatchable(reservoir, gate.Facility, reservoir.MetricValue); err != nil {
		return err
	}
	if reservoir.Version != permit.AppliedReservoirVersion {
		return fmt.Errorf("%w: reservoir water-level status changed (version %d -> %d) after application", ErrInvalidInput, permit.AppliedReservoirVersion, reservoir.Version)
	}
	if math.Abs(reservoir.MetricValue-permit.AppliedLevel) > PermitLevelTolerance {
		return fmt.Errorf("%w: water level moved beyond the permitted window after application", ErrInvalidInput)
	}
	return nil
}

func activationDriftReason(permit model.DispatchPermit, directive model.OperationDirective, gate model.GateUnit, reservoir model.Reservoir) string {
	if directive.ID != permit.DirectiveID || directive.Status != string(constants.DirectiveStateApproved) {
		return "directive is no longer approved for this permit"
	}
	if directive.Version != permit.ApprovedDirectiveVersion {
		return fmt.Sprintf("directive version changed after approval (%d -> %d)", permit.ApprovedDirectiveVersion, directive.Version)
	}
	if !strings.EqualFold(gate.Facility, directive.Facility) || gate.ID != permit.GateID {
		return "gate ownership or linkage changed after approval"
	}
	if gate.Version != permit.ApprovedGateVersion {
		return fmt.Sprintf("gate version changed after approval (%d -> %d)", permit.ApprovedGateVersion, gate.Version)
	}
	if gate.Status != permit.ApprovedGateStatus {
		return fmt.Sprintf("gate state changed after approval (%s -> %s)", permit.ApprovedGateStatus, gate.Status)
	}
	if gate.Status == string(constants.GateStateLocked) {
		return "gate is locked"
	}
	if !strings.EqualFold(reservoir.Facility, gate.Facility) || reservoir.Code != permit.ReservoirCode {
		return "reservoir linkage changed after approval"
	}
	if reservoir.Version != permit.ApprovedReservoirVersion || reservoir.Status != permit.ApprovedReservoirStatus {
		return "reservoir water-level status changed after approval"
	}
	if reservoir.Status == "restricted" {
		return "reservoir became restricted after approval"
	}
	if math.Abs(reservoir.MetricValue-permit.ApprovedLevel) > PermitLevelTolerance {
		return fmt.Sprintf("water level %.2f moved beyond the approved window %.2f ± %.2f", reservoir.MetricValue, permit.ApprovedLevel, PermitLevelTolerance)
	}
	return ""
}

func (s *dispatchPermitService) ensureNotLapsed(ctx context.Context, permit *model.DispatchPermit, requestID string) error {
	if permit.Status == string(constants.PermitStateRequested) && time.Now().UTC().After(permit.ValidUntil) {
		if err := s.advanceVersioned(ctx, permit, permit.Version, string(constants.PermitStateExpired),
			"system", "system", requestID, "validity window ended before review", "expired", "permit_expire"); err != nil {
			return err
		}
		return fmt.Errorf("%w: permit expired at %s before review; a new application is required", ErrInvalidInput, permit.ValidUntil.Format(time.RFC3339))
	}
	return nil
}

// advanceVersioned performs a single-permit lifecycle write with decision
// evidence and an audit record in one transaction. It never touches directive
// or gate state, so drift invalidation cannot rewrite operational aggregates.
func (s *dispatchPermitService) advanceVersioned(ctx context.Context, permit *model.DispatchPermit, expectedVersion uint,
	target, actor, role, requestID, reason, stage, auditAction string,
) error {
	current, err := s.repository.Get(ctx, permit.ID)
	if err != nil {
		return err
	}
	before := current.Status
	if !constants.CanTransition(constants.DispatchPermitTransitions, before, target) {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, before, target)
	}
	now := time.Now().UTC()
	current.Status = target
	current.ActiveDirectiveKey = nil
	current.Version = expectedVersion + 1
	current.UpdatedAt = now
	if target == string(constants.PermitStateInvalidated) {
		current.InvalidatedBy = actor
		current.InvalidatedAt = &now
	}
	decision := &model.PermitDecision{Stage: stage, Actor: actor, Role: role, RequestID: requestID,
		Reason: reason, FromState: before, ToState: target, CreatedAt: now}
	if err := s.security.WithinTransaction(ctx, func(txCtx context.Context) error {
		if err := s.repository.SaveTransition(txCtx, current.ID, expectedVersion, &current, decision); err != nil {
			return err
		}
		return s.security.Audit(txCtx, actor, requestID, auditAction, "DispatchPermit", current.ID, before, target, reason)
	}); err != nil {
		return fmt.Errorf("%s 调度许可: %w", auditAction, err)
	}
	*permit = current
	return nil
}

func validatePermitWindow(now, from, until time.Time) error {
	if !until.After(from) {
		return fmt.Errorf("%w: validity window must end after it starts", ErrInvalidInput)
	}
	window := until.Sub(from)
	if window < permitMinWindow || window > permitMaxWindow {
		return fmt.Errorf("%w: validity window must be between %s and %s", ErrInvalidInput, permitMinWindow, permitMaxWindow)
	}
	if from.Before(now.Add(-permitFromSkew)) {
		return fmt.Errorf("%w: validity window cannot start more than %s in the past", ErrInvalidInput, permitFromSkew)
	}
	if !until.After(now) {
		return fmt.Errorf("%w: validity window must end in the future", ErrInvalidInput)
	}
	return nil
}
