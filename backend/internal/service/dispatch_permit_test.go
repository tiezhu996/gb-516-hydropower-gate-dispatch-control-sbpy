package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/blueship581/hydropower-gate-dispatch-control/backend/internal/config"
	"github.com/blueship581/hydropower-gate-dispatch-control/backend/internal/dto"
	"github.com/blueship581/hydropower-gate-dispatch-control/backend/internal/model"
	"github.com/blueship581/hydropower-gate-dispatch-control/backend/internal/repository"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

type permitFixture struct {
	permits       DispatchPermitService
	directives    OperationDirectiveService
	directiveRepo repository.OperationDirectiveRepository
	gates         repository.GateUnitRepository
	reservoirs    repository.ReservoirRepository
	db            *gorm.DB
}

func newPermitFixture(t *testing.T, reservoirStatus string, level float64) permitFixture {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&model.Reservoir{}, &model.GateUnit{}, &model.OperationDirective{},
		&model.DirectiveApproval{}, &model.ExecutionConfirmation{}, &model.DispatchPermit{}, &model.PermitDecision{}, &model.AuditLog{}); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}
	gateRepo := repository.NewGateUnitRepository(db)
	reservoirRepo := repository.NewReservoirRepository(db)
	directiveRepo := repository.NewOperationDirectiveRepository(db)
	permitRepo := repository.NewDispatchPermitRepository(db)
	security := NewSecurityService(repository.NewSecurityRepository(db), config.Config{})
	directives := NewOperationDirectiveService(directiveRepo, gateRepo, security)
	permits := NewDispatchPermitService(permitRepo, directiveRepo, gateRepo, reservoirRepo, security)

	reservoir := model.Reservoir{BaseModel: model.BaseModel{Code: "R-PERMIT", Name: "上游库区", Status: reservoirStatus, Version: 1},
		Facility: "主坝", Owner: "运行一组", MetricValue: level, MetricUnit: "m"}
	if err := reservoirRepo.Create(context.Background(), &reservoir); err != nil {
		t.Fatalf("create reservoir: %v", err)
	}
	gate := model.GateUnit{BaseModel: model.BaseModel{Code: "GU-PERMIT", Name: "泄洪闸", Status: "closed", Version: 1},
		Facility: "主坝", Owner: "运行一组", RelatedCode: "R-PERMIT"}
	if err := gateRepo.Create(context.Background(), &gate); err != nil {
		t.Fatalf("create gate: %v", err)
	}
	return permitFixture{permits: permits, directives: directives, directiveRepo: directiveRepo, gates: gateRepo, reservoirs: reservoirRepo, db: db}
}

func (f permitFixture) approvedDirective(t *testing.T) model.OperationDirective {
	t.Helper()
	ctx := context.Background()
	created, err := f.directives.Create(ctx, dto.CreateOperationDirective{
		Code: "OD-PERMIT", Name: "开启泄洪闸", Facility: "主坝", Owner: "运行一组",
		Category: "泄洪", RiskLevel: "high", MetricValue: 35, MetricUnit: "%",
		EffectiveAt: time.Now().UTC().Add(time.Hour), Evidence: "水位与闭锁核对完成",
		RelatedCode: "GU-PERMIT", GateState: "open",
	}, "operator", "req-create")
	if err != nil {
		t.Fatalf("create directive: %v", err)
	}
	submitted, err := f.directives.Transition(ctx, created.ID, dto.TransitionRequest{Status: "pending", ExpectedVersion: created.Version, Reason: "提交复核"}, "operator", model.RoleOperator, "req-submit")
	if err != nil {
		t.Fatalf("submit directive: %v", err)
	}
	approved, err := f.directives.Transition(ctx, submitted.ID, dto.TransitionRequest{Status: "approved", ExpectedVersion: submitted.Version, Reason: "复核通过"}, "reviewer", model.RoleReviewer, "req-approve")
	if err != nil {
		t.Fatalf("approve directive: %v", err)
	}
	return approved
}

func permitApplyInput(code string, observed float64) dto.CreateDispatchPermit {
	return dto.CreateDispatchPermit{
		Code: code, Name: "泄洪闸开闸许可", DirectiveCode: "OD-PERMIT", Action: "open",
		ValidFrom: time.Now().UTC().Add(-time.Minute), ValidUntil: time.Now().UTC().Add(2 * time.Hour),
		ObservedLevel: observed,
	}
}

func approvePermit(t *testing.T, f permitFixture, permit model.DispatchPermit) model.DispatchPermit {
	t.Helper()
	approved, err := f.permits.Approve(context.Background(), permit.ID, dto.PermitReviewRequest{
		ExpectedVersion: permit.Version, Reason: "复核水位、归属与闭锁状态通过",
	}, "reviewer", model.RoleReviewer, "req-permit-approve")
	if err != nil {
		t.Fatalf("approve permit: %v", err)
	}
	return approved
}

func TestPermitHappyFlowConsumesAndMovesGateAtomically(t *testing.T) {
	f := newPermitFixture(t, "normal", 168.2)
	f.approvedDirective(t)
	ctx := context.Background()

	permit, err := f.permits.Apply(ctx, permitApplyInput("DP-HAPPY", 168.2), "operator", "req-permit-apply")
	if err != nil {
		t.Fatalf("apply permit: %v", err)
	}
	if permit.Status != "requested" || permit.AppliedBy != "operator" || len(permit.Decisions) != 1 {
		t.Fatalf("application evidence invalid: %#v", permit)
	}
	permit = approvePermit(t, f, permit)
	if permit.Status != "approved" || permit.ApprovedBy != "reviewer" || permit.ApprovedBy == permit.AppliedBy || len(permit.Decisions) != 2 {
		t.Fatalf("approval evidence invalid: %#v", permit)
	}

	consumed, err := f.permits.Activate(ctx, permit.ID, dto.PermitActivationRequest{
		ExpectedVersion: permit.Version, Reason: "动作前复核水位和闸门版本一致",
	}, "operator", model.RoleOperator, "req-permit-activate")
	if err != nil {
		t.Fatalf("activate permit: %v", err)
	}
	if consumed.Status != "consumed" || consumed.ConsumedAt == nil {
		t.Fatalf("permit not consumed: %#v", consumed)
	}
	directive, _ := f.directiveRepo.GetByCode(ctx, "OD-PERMIT")
	if directive.Status != "executing" {
		t.Fatalf("directive should be executing, got %s", directive.Status)
	}
	gate, _ := f.gates.GetByCode(ctx, "GU-PERMIT")
	if gate.Status != "moving" || gate.Version != 2 {
		t.Fatalf("gate should atomically enter moving at version 2, got %s v%d", gate.Status, gate.Version)
	}
}

func TestPermitApplyRejectsUnsafeWaterLevelOwnershipAndLock(t *testing.T) {
	f := newPermitFixture(t, "normal", 168.2)
	f.approvedDirective(t)
	ctx := context.Background()

	_, err := f.permits.Apply(ctx, permitApplyInput("DP-LEVEL", 170.0), "operator", "req-level")
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("water level outside window must be refused, got %v", err)
	}
	gate, _ := f.gates.GetByCode(ctx, "GU-PERMIT")
	if gate.Status != "closed" {
		t.Fatalf("failed application must not rewrite gate state, got %s", gate.Status)
	}

	locked := gate
	locked.Status = "locked"
	locked.Version++
	if err := f.gates.Update(ctx, locked.ID, locked.Version-1, &locked); err != nil {
		t.Fatalf("lock gate: %v", err)
	}
	_, err = f.permits.Apply(ctx, permitApplyInput("DP-LOCK", 168.2), "operator", "req-lock")
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("locked gate application must be refused, got %v", err)
	}
}

func TestPermitRejectsRestrictedReservoirAndDuplicateApplications(t *testing.T) {
	f := newPermitFixture(t, "restricted", 168.2)
	f.approvedDirective(t)
	ctx := context.Background()

	_, err := f.permits.Apply(ctx, permitApplyInput("DP-RESTRICT", 168.2), "operator", "req-restrict")
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("restricted reservoir must refuse permit application, got %v", err)
	}

	reservoir, _ := f.reservoirs.GetByCode(ctx, "R-PERMIT")
	reservoir.Status = "normal"
	reservoir.Version++
	if err := f.reservoirs.Update(ctx, reservoir.ID, reservoir.Version-1, &reservoir); err != nil {
		t.Fatalf("restore reservoir: %v", err)
	}
	first, err := f.permits.Apply(ctx, permitApplyInput("DP-FIRST", 168.2), "operator", "req-first")
	if err != nil {
		t.Fatalf("first application: %v", err)
	}
	_, err = f.permits.Apply(ctx, permitApplyInput("DP-SECOND", 168.2), "operator", "req-second")
	if !errors.Is(err, ErrPermitConflict) {
		t.Fatalf("duplicate active application must conflict, got %v", err)
	}

	review := dto.PermitReviewRequest{ExpectedVersion: first.Version, Reason: "条件不符，拒绝申请"}
	rejected, err := f.permits.Reject(ctx, first.ID, review, "reviewer", model.RoleReviewer, "req-reject")
	if err != nil {
		t.Fatalf("reject permit: %v", err)
	}
	if rejected.Status != "rejected" {
		t.Fatalf("permit should be rejected, got %s", rejected.Status)
	}
	again, err := f.permits.Apply(ctx, permitApplyInput("DP-AGAIN", 168.2), "operator", "req-again")
	if err != nil {
		t.Fatalf("new application should be allowed after rejection: %v", err)
	}
	if again.Status != "requested" {
		t.Fatalf("new permit should be requested, got %s", again.Status)
	}
}

func TestPermitApprovalEnforcesTwoPersonAndFreshConditions(t *testing.T) {
	f := newPermitFixture(t, "normal", 168.2)
	f.approvedDirective(t)
	ctx := context.Background()
	permit, err := f.permits.Apply(ctx, permitApplyInput("DP-REVIEW", 168.2), "operator", "req-apply")
	if err != nil {
		t.Fatalf("apply permit: %v", err)
	}

	_, err = f.permits.Approve(ctx, permit.ID, dto.PermitReviewRequest{ExpectedVersion: permit.Version, Reason: "申请人不得自批"}, "operator", model.RoleReviewer, "req-self")
	if !errors.Is(err, ErrTwoPersonRequired) {
		t.Fatalf("applicant cannot approve own permit, got %v", err)
	}

	// Gate version changes between application and approval -> approval refused,
	// permit stays requested so the reviewer can reject it.
	gate, _ := f.gates.GetByCode(ctx, "GU-PERMIT")
	gate.Status = "open"
	gate.Version++
	if err := f.gates.Update(ctx, gate.ID, gate.Version-1, &gate); err != nil {
		t.Fatalf("change gate: %v", err)
	}
	_, err = f.permits.Approve(ctx, permit.ID, dto.PermitReviewRequest{ExpectedVersion: permit.Version, Reason: "闸门版本已变化"}, "reviewer", model.RoleReviewer, "req-drift")
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("approval on changed gate must be refused without rewrite, got %v", err)
	}
	stored, _ := f.permits.Get(ctx, permit.ID)
	if stored.Status != "requested" {
		t.Fatalf("refused approval must keep permit requested, got %s", stored.Status)
	}
}

func TestPermitActivationInvalidatesOnGateVersionDriftWithoutTouchingAggregates(t *testing.T) {
	f := newPermitFixture(t, "normal", 168.2)
	f.approvedDirective(t)
	ctx := context.Background()
	permit, err := f.permits.Apply(ctx, permitApplyInput("DP-DRIFT", 168.2), "operator", "req-apply")
	if err != nil {
		t.Fatalf("apply permit: %v", err)
	}
	permit = approvePermit(t, f, permit)

	directiveBefore, _ := f.directiveRepo.GetByCode(ctx, "OD-PERMIT")
	gate, _ := f.gates.GetByCode(ctx, "GU-PERMIT")
	gate.Evidence = "现场闭锁挂牌，状态记录未变"
	gate.Version++
	if err := f.gates.Update(ctx, gate.ID, gate.Version-1, &gate); err != nil {
		t.Fatalf("bump gate version: %v", err)
	}

	_, err = f.permits.Activate(ctx, permit.ID, dto.PermitActivationRequest{
		ExpectedVersion: permit.Version, Reason: "动作前再次核对",
	}, "operator", model.RoleOperator, "req-activate-drift")
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("activation on drifted gate must be refused, got %v", err)
	}
	invalidated, _ := f.permits.Get(ctx, permit.ID)
	if invalidated.Status != "invalidated" || invalidated.InvalidatedBy != "operator" {
		t.Fatalf("permit should be invalidated by the drift, got %#v", invalidated)
	}
	if len(invalidated.Decisions) != 3 || invalidated.Decisions[2].Stage != "invalidated" {
		t.Fatalf("invalidated decision trail missing: %#v", invalidated.Decisions)
	}
	directiveAfter, _ := f.directiveRepo.GetByCode(ctx, "OD-PERMIT")
	if directiveAfter.Status != directiveBefore.Status || directiveAfter.Version != directiveBefore.Version {
		t.Fatalf("drift invalidation must not rewrite directive: before v%d/%s after v%d/%s",
			directiveBefore.Version, directiveBefore.Status, directiveAfter.Version, directiveAfter.Status)
	}
	gateAfter, _ := f.gates.GetByCode(ctx, "GU-PERMIT")
	if gateAfter.Status != "closed" {
		t.Fatalf("drift invalidation must not move the gate, got %s", gateAfter.Status)
	}

	// Re-application is allowed once the stale permit has been invalidated.
	reapplied, err := f.permits.Apply(ctx, permitApplyInput("DP-REAPPLY", 168.2), "operator", "req-reapply")
	if err != nil {
		t.Fatalf("re-applying after invalidation should succeed: %v", err)
	}
	if reapplied.Status != "requested" {
		t.Fatalf("re-applied permit should be requested, got %s", reapplied.Status)
	}
}

func TestPermitActivationInvalidatesOnReservoirLevelDrift(t *testing.T) {
	f := newPermitFixture(t, "normal", 168.2)
	f.approvedDirective(t)
	ctx := context.Background()
	permit, err := f.permits.Apply(ctx, permitApplyInput("DP-WATER", 168.2), "operator", "req-apply")
	if err != nil {
		t.Fatalf("apply permit: %v", err)
	}
	permit = approvePermit(t, f, permit)

	reservoir, _ := f.reservoirs.GetByCode(ctx, "R-PERMIT")
	reservoir.MetricValue = 169.9
	reservoir.Version++
	if err := f.reservoirs.Update(ctx, reservoir.ID, reservoir.Version-1, &reservoir); err != nil {
		t.Fatalf("raise reservoir level: %v", err)
	}
	_, err = f.permits.Activate(ctx, permit.ID, dto.PermitActivationRequest{
		ExpectedVersion: permit.Version, Reason: "动作前水位复核",
	}, "operator", model.RoleOperator, "req-activate-water")
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("activation outside water window must be refused, got %v", err)
	}
	invalidated, _ := f.permits.Get(ctx, permit.ID)
	if invalidated.Status != "invalidated" {
		t.Fatalf("permit should be invalidated by water-level drift, got %s", invalidated.Status)
	}
}

func TestPermitConcurrentApprovalOnlyOneSucceeds(t *testing.T) {
	f := newPermitFixture(t, "normal", 168.2)
	f.approvedDirective(t)
	ctx := context.Background()
	permit, err := f.permits.Apply(ctx, permitApplyInput("DP-CONCURRENT", 168.2), "operator", "req-apply")
	if err != nil {
		t.Fatalf("apply permit: %v", err)
	}
	review := dto.PermitReviewRequest{ExpectedVersion: permit.Version, Reason: "并发批准只允许一份生效"}
	if _, err := f.permits.Approve(ctx, permit.ID, review, "reviewer", model.RoleReviewer, "req-approve-a"); err != nil {
		t.Fatalf("first approval: %v", err)
	}
	_, err = f.permits.Approve(ctx, permit.ID, review, "reviewer2", model.RoleReviewer, "req-approve-b")
	if !errors.Is(err, repository.ErrVersionConflict) {
		t.Fatalf("second approval must hit optimistic lock, got %v", err)
	}
	stored, _ := f.permits.Get(ctx, permit.ID)
	if stored.Status != "approved" || stored.ApprovedBy != "reviewer" || len(stored.Decisions) != 2 {
		t.Fatalf("only one approval should take effect: %#v", stored)
	}
}

func TestPermitExpiresAfterValidityWindow(t *testing.T) {
	f := newPermitFixture(t, "normal", 168.2)
	f.approvedDirective(t)
	ctx := context.Background()
	permit, err := f.permits.Apply(ctx, permitApplyInput("DP-EXPIRED", 168.2), "operator", "req-expired-apply")
	if err != nil {
		t.Fatalf("apply permit: %v", err)
	}

	// Simulate the validity window closing without any lifecycle action.
	var stored model.DispatchPermit
	if err := f.db.Where("id = ?", permit.ID).First(&stored).Error; err != nil {
		t.Fatalf("load permit row: %v", err)
	}
	expired := stored
	expired.ValidUntil = time.Now().UTC().Add(-time.Minute)
	if err := f.db.Model(&model.DispatchPermit{}).Where("id = ?", permit.ID).Update("valid_until", expired.ValidUntil).Error; err != nil {
		t.Fatalf("shrink validity window: %v", err)
	}

	_, err = f.permits.Approve(ctx, permit.ID, dto.PermitReviewRequest{ExpectedVersion: permit.Version, Reason: "尝试批准过期申请"}, "reviewer", model.RoleReviewer, "req-expired-approve")
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expired permit approval must be refused, got %v", err)
	}
	lapsed, _ := f.permits.Get(ctx, permit.ID)
	if lapsed.Status != "expired" || lapsed.ActiveDirectiveKey != nil {
		t.Fatalf("unreviewed permit should be marked expired and release its active slot, got %#v", lapsed)
	}
	if len(lapsed.Decisions) != 2 || lapsed.Decisions[1].Stage != "expired" {
		t.Fatalf("expired decision trail missing: %#v", lapsed.Decisions)
	}

	// A new application is possible once the expired permit released the directive.
	again, err := f.permits.Apply(ctx, permitApplyInput("DP-EXPIRED-NEW", 168.2), "operator", "req-expired-new")
	if err != nil {
		t.Fatalf("new application after expiry should succeed: %v", err)
	}
	if again.Status != "requested" {
		t.Fatalf("new permit should be requested, got %s", again.Status)
	}
}
