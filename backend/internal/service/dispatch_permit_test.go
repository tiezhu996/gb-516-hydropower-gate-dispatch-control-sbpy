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
	service   DispatchPermitService
	directive OperationDirectiveService
	gates     repository.GateUnitRepository
	reservoir repository.ReservoirRepository
	db        *gorm.DB
}

func newPermitFixture(t *testing.T) permitFixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(
		&model.Reservoir{}, &model.GateUnit{}, &model.OperationDirective{},
		&model.DirectiveApproval{}, &model.DispatchPermit{}, &model.PermitDecision{}, &model.AuditLog{},
	); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}
	security := NewSecurityService(repository.NewSecurityRepository(db), config.Config{})
	gateRepo := repository.NewGateUnitRepository(db)
	reservoirRepo := repository.NewReservoirRepository(db)
	directiveRepo := repository.NewOperationDirectiveRepository(db)
	permitRepo := repository.NewDispatchPermitRepository(db)
	directives := NewOperationDirectiveService(directiveRepo, gateRepo, security)
	permits := NewDispatchPermitService(permitRepo, directiveRepo, gateRepo, reservoirRepo, security)

	reservoir := model.Reservoir{BaseModel: model.BaseModel{Code: "R-PERMIT", Name: "库区", Status: "normal", Version: 1},
		Facility: "主坝", Owner: "运行一组", MetricValue: 168.2, MetricUnit: "m", RelatedCode: "R-PERMIT"}
	if err := db.Create(&reservoir).Error; err != nil {
		t.Fatalf("create reservoir: %v", err)
	}
	gate := model.GateUnit{BaseModel: model.BaseModel{Code: "GU-PERMIT", Name: "泄洪闸", Status: "open", Version: 1},
		Facility: "主坝", Owner: "运行一组", RelatedCode: "R-PERMIT"}
	if err := db.Create(&gate).Error; err != nil {
		t.Fatalf("create gate: %v", err)
	}
	return permitFixture{service: permits, directive: directives, gates: gateRepo, reservoir: reservoirRepo, db: db}
}

// prepareApprovedDirective builds an approved directive asking gate to move to closed.
func prepareApprovedDirective(t *testing.T, f permitFixture, code string) model.OperationDirective {
	t.Helper()
	ctx := context.Background()
	created, err := f.directive.Create(ctx, dto.CreateOperationDirective{
		Code: code, Name: "关闭泄洪闸", Facility: "主坝", Owner: "运行一组", Category: "泄洪",
		RiskLevel: "high", MetricValue: 35, MetricUnit: "%", EffectiveAt: time.Now().UTC().Add(time.Hour),
		Evidence: "库水位处于许可窗口", RelatedCode: "GU-PERMIT", GateState: "closed",
	}, "operator", "req-create")
	if err != nil {
		t.Fatalf("create directive: %v", err)
	}
	submitted, err := f.directive.Transition(ctx, created.ID, dto.TransitionRequest{Status: "pending", ExpectedVersion: created.Version, Reason: "提交复核"}, "operator", model.RoleOperator, "req-submit")
	if err != nil {
		t.Fatalf("submit directive: %v", err)
	}
	approved, err := f.directive.Transition(ctx, submitted.ID, dto.TransitionRequest{Status: "approved", ExpectedVersion: submitted.Version, Reason: "复核通过"}, "reviewer", model.RoleReviewer, "req-approve")
	if err != nil {
		t.Fatalf("approve directive: %v", err)
	}
	return approved
}

func permitInput(code, directiveCode string) dto.ApplyDispatchPermit {
	now := time.Now().UTC()
	return dto.ApplyDispatchPermit{
		Code: code, Name: "关闭泄洪闸调度许可", Owner: "运行一组", RelatedCode: directiveCode,
		Action: "closed", ValidFrom: now.Add(-time.Minute), ValidUntil: now.Add(8 * time.Hour),
		Evidence: "已核对库区水位、闸门归属与闭锁状态",
	}
}

func TestPermitApplyChecksWaterLevelOwnershipAndLockout(t *testing.T) {
	f := newPermitFixture(t)
	ctx := context.Background()
	approved := prepareApprovedDirective(t, f, "OD-PERMIT-OK")

	created, err := f.service.Apply(ctx, permitInput("DP-OK-1", approved.Code), "operator", "req-apply")
	if err != nil {
		t.Fatalf("apply permit: %v", err)
	}
	if created.Status != "pending" || created.SnapshotGateStatus != "open" || created.SnapshotGateVersion != 1 ||
		created.SnapshotReservoirStatus != "normal" || created.AppliedBy != "operator" {
		t.Fatalf("snapshot not preserved: %#v", created)
	}
	if len(created.Decisions) != 1 || created.Decisions[0].Stage != "applied" || created.Decisions[0].RequestID != "req-apply" {
		t.Fatalf("application decision trail invalid: %#v", created.Decisions)
	}

	// Locked gate must be rejected with the lockout reason and persist nothing.
	gate, err := f.gates.GetByCode(ctx, "GU-PERMIT")
	if err != nil {
		t.Fatalf("get gate: %v", err)
	}
	gate.Status = "locked"
	gate.Version++
	if err := f.gates.Update(ctx, gate.ID, gate.Version-1, &gate); err != nil {
		t.Fatalf("lock gate: %v", err)
	}
	if _, err := f.service.Apply(ctx, permitInput("DP-LOCKED", approved.Code), "operator", "req-locked"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("locked gate must reject application, got %v", err)
	}
	var count int64
	if err := f.db.Model(&model.DispatchPermit{}).Where("code = ?", "DP-LOCKED").Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("rejected application must not persist, count=%d err=%v", count, err)
	}
}

func TestPermitWaterLevelOutsideWindowRejects(t *testing.T) {
	f := newPermitFixture(t)
	ctx := context.Background()
	reservoir, err := f.reservoir.GetByCode(ctx, "R-PERMIT")
	if err != nil {
		t.Fatalf("get reservoir: %v", err)
	}
	reservoir.Status = "critical"
	reservoir.Version++
	// Move through the reservoir transition graph (normal -> critical is allowed).
	if err := f.db.Model(&model.Reservoir{}).Where("id = ?", reservoir.ID).
		Updates(map[string]any{"status": "critical", "version": reservoir.Version}).Error; err != nil {
		t.Fatalf("raise reservoir level: %v", err)
	}
	approved := prepareApprovedDirective(t, f, "OD-PERMIT-LEVEL")
	if _, err := f.service.Apply(ctx, permitInput("DP-LEVEL", approved.Code), "operator", "req-level"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("critical water level must reject permit, got %v", err)
	}
}

func TestPermitTwoPersonApproveAndActivate(t *testing.T) {
	f := newPermitFixture(t)
	ctx := context.Background()
	approved := prepareApprovedDirective(t, f, "OD-PERMIT-FLOW")
	permit, err := f.service.Apply(ctx, permitInput("DP-FLOW", approved.Code), "operator", "req-apply")
	if err != nil {
		t.Fatalf("apply permit: %v", err)
	}

	// The applicant must never be the reviewer.
	if _, err := f.service.Approve(ctx, permit.ID, dto.PermitDecisionRequest{ExpectedVersion: permit.Version, Reason: "自己不得批准自己的申请"}, "operator", model.RoleOperator, "req-self"); !errors.Is(err, ErrTwoPersonRequired) {
		t.Fatalf("self approval must fail two-person rule, got %v", err)
	}
	reviewed, err := f.service.Approve(ctx, permit.ID, dto.PermitDecisionRequest{ExpectedVersion: permit.Version, Reason: "复核水位、归属和闭锁均一致"}, "reviewer", model.RoleReviewer, "req-approve")
	if err != nil {
		t.Fatalf("reviewer approve: %v", err)
	}
	if reviewed.Status != "approved" || reviewed.ApprovedBy != "reviewer" || reviewed.ApprovedGateVersion != 1 || len(reviewed.Decisions) != 2 {
		t.Fatalf("approved permit invalid: %#v", reviewed)
	}

	// Concurrent approve: replaying the stale expectedVersion must never take
	// effect. A serialized replay is rejected by the state guard; a true race is
	// rejected by optimistic CAS. Either way the permit keeps its first decision.
	if _, err := f.service.Approve(ctx, permit.ID, dto.PermitDecisionRequest{ExpectedVersion: permit.Version, Reason: "并发复核不应生效"}, "admin", model.RoleAdmin, "req-race"); !(errors.Is(err, repository.ErrVersionConflict) || errors.Is(err, ErrInvalidTransition)) {
		t.Fatalf("concurrent approval must be refused, got %v", err)
	}
	reloaded, err := f.service.Get(ctx, permit.ID)
	if err != nil {
		t.Fatalf("reload permit: %v", err)
	}
	if reloaded.ApprovedBy != "reviewer" || reloaded.Version != reviewed.Version {
		t.Fatalf("only one approval may take effect: %#v", reloaded)
	}

	activated, err := f.service.Activate(ctx, reviewed.ID, dto.PermitActivationRequest{ExpectedVersion: reviewed.Version, Reason: "动作开始前再次核对水位与闸门版本一致"}, "operator", model.RoleOperator, "req-activate")
	if err != nil {
		t.Fatalf("activate permit: %v", err)
	}
	if activated.Status != "active" || activated.ActivatedBy != "operator" || len(activated.Decisions) != 3 {
		t.Fatalf("active permit invalid: %#v", activated)
	}
}

func TestPermitDuplicateApplicationOnlyOneEffective(t *testing.T) {
	f := newPermitFixture(t)
	ctx := context.Background()
	firstDirective := prepareApprovedDirective(t, f, "OD-DUP-1")
	if _, err := f.service.Apply(ctx, permitInput("DP-DUP-1", firstDirective.Code), "operator", "req-apply-1"); err != nil {
		t.Fatalf("first application: %v", err)
	}
	// Another approved directive on the same gate must still be refused while a
	// permit occupies the gate slot.
	secondDirective := prepareApprovedDirective(t, f, "OD-DUP-2")
	_, err := f.service.Apply(ctx, permitInput("DP-DUP-2", secondDirective.Code), "operator", "req-apply-2")
	if !errors.Is(err, ErrDuplicatePermit) {
		t.Fatalf("duplicate application must be refused, got %v", err)
	}
}

func TestPermitGateVersionDriftInvalidatesBeforeAction(t *testing.T) {
	f := newPermitFixture(t)
	ctx := context.Background()
	approved := prepareApprovedDirective(t, f, "OD-DRIFT")
	permit, err := f.service.Apply(ctx, permitInput("DP-DRIFT", approved.Code), "operator", "req-apply")
	if err != nil {
		t.Fatalf("apply permit: %v", err)
	}
	reviewed, err := f.service.Approve(ctx, permit.ID, dto.PermitDecisionRequest{ExpectedVersion: permit.Version, Reason: "复核通过"}, "reviewer", model.RoleReviewer, "req-approve")
	if err != nil {
		t.Fatalf("approve permit: %v", err)
	}

	// The gate changes after approval (e.g. a legitimate maintenance transition).
	gate, err := f.gates.GetByCode(ctx, "GU-PERMIT")
	if err != nil {
		t.Fatalf("get gate: %v", err)
	}
	gate.Status = "moving"
	gate.Version++
	if err := f.gates.Update(ctx, gate.ID, gate.Version-1, &gate); err != nil {
		t.Fatalf("move gate: %v", err)
	}

	_, err = f.service.Activate(ctx, reviewed.ID, dto.PermitActivationRequest{ExpectedVersion: reviewed.Version, Reason: "动作前复核"}, "operator", model.RoleOperator, "req-activate-drift")
	if !errors.Is(err, ErrPermitInvalidated) {
		t.Fatalf("drifted gate must invalidate permit, got %v", err)
	}
	stored, getErr := f.service.Get(ctx, reviewed.ID)
	if getErr != nil {
		t.Fatalf("reload permit: %v", getErr)
	}
	if stored.Status != "invalidated" || stored.InvalidReason == "" || stored.ActiveSlot != nil {
		t.Fatalf("permit should be invalidated with reason and released slot: %#v", stored)
	}
	// The gate must remain in moving and the directive must remain approved: the
	// failure path is forbidden from rewriting either aggregate.
	gateAfter, err := f.gates.Get(ctx, gate.ID)
	if err != nil {
		t.Fatalf("reload gate: %v", err)
	}
	if gateAfter.Status != "moving" || gateAfter.Version != gate.Version {
		t.Fatalf("gate must not be rewritten by invalidation: %#v", gateAfter)
	}
	directiveAfter, err := f.directive.Get(ctx, approved.ID)
	if err != nil {
		t.Fatalf("reload directive: %v", err)
	}
	if directiveAfter.Status != "approved" || directiveAfter.Version != approved.Version {
		t.Fatalf("directive must not be rewritten by invalidation: %#v", directiveAfter)
	}

	// After the gate settles back to the expected state, the operator can apply
	// again on the same directive.
	gate.Status = "open"
	gate.Version++
	if err := f.gates.Update(ctx, gate.ID, gate.Version-1, &gate); err != nil {
		t.Fatalf("settle gate: %v", err)
	}
	reapplied, err := f.service.Apply(ctx, permitInput("DP-DRIFT-2", approved.Code), "operator", "req-reapply")
	if err != nil {
		t.Fatalf("re-application after invalidation: %v", err)
	}
	if reapplied.Status != "pending" || reapplied.SnapshotGateVersion != gate.Version {
		t.Fatalf("re-applied permit should pin the new gate version: %#v", reapplied)
	}
}

func TestPermitApprovalRechecksConditions(t *testing.T) {
	f := newPermitFixture(t)
	ctx := context.Background()
	approved := prepareApprovedDirective(t, f, "OD-RECHECK")
	permit, err := f.service.Apply(ctx, permitInput("DP-RECHECK", approved.Code), "operator", "req-apply")
	if err != nil {
		t.Fatalf("apply permit: %v", err)
	}
	// Reservoir drifts to restricted before the reviewer decides: approval must
	// fail and must not rewrite the permit.
	reservoir, err := f.reservoir.GetByCode(ctx, "R-PERMIT")
	if err != nil {
		t.Fatalf("get reservoir: %v", err)
	}
	reservoir.Status = "warning"
	reservoir.Version++
	if err := f.db.Model(&model.Reservoir{}).Where("id = ?", reservoir.ID).
		Updates(map[string]any{"status": "warning", "version": reservoir.Version}).Error; err != nil {
		t.Fatalf("reservoir to warning: %v", err)
	}
	reservoir.Status = "restricted"
	reservoir.Version++
	if err := f.db.Model(&model.Reservoir{}).Where("id = ?", reservoir.ID).
		Updates(map[string]any{"status": "restricted", "version": reservoir.Version}).Error; err != nil {
		t.Fatalf("reservoir to restricted: %v", err)
	}
	if _, err := f.service.Approve(ctx, permit.ID, dto.PermitDecisionRequest{ExpectedVersion: permit.Version, Reason: "水位已超限不应批准"}, "reviewer", model.RoleReviewer, "req-approve-bad"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("approval against changed water level must fail, got %v", err)
	}
	stored, err := f.service.Get(ctx, permit.ID)
	if err != nil {
		t.Fatalf("reload permit: %v", err)
	}
	if stored.Status != "pending" || stored.Version != permit.Version {
		t.Fatalf("failed approval must not rewrite permit: %#v", stored)
	}
}

func TestPermitValidityWindowBounds(t *testing.T) {
	f := newPermitFixture(t)
	ctx := context.Background()
	approved := prepareApprovedDirective(t, f, "OD-WINDOW")
	now := time.Now().UTC()
	cases := []struct {
		name       string
		from, till time.Time
	}{
		{"too short", now, now.Add(30 * time.Second)},
		{"too long", now, now.Add(25 * time.Hour)},
		{"already ended", now.Add(-2 * time.Hour), now.Add(-time.Hour)},
	}
	for index, tc := range cases {
		input := permitInput(fmt.Sprintf("DP-WINDOW-%d", index), approved.Code)
		input.ValidFrom = tc.from.UTC()
		input.ValidUntil = tc.till.UTC()
		if _, err := f.service.Apply(ctx, input, "operator", "req-window"); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("%s: expected invalid input, got %v", tc.name, err)
		}
	}
}

func TestPermitOnlyAcceptsApprovedDirective(t *testing.T) {
	f := newPermitFixture(t)
	ctx := context.Background()
	created, err := f.directive.Create(ctx, dto.CreateOperationDirective{
		Code: "OD-NOT-APPROVED", Name: "未获批指令", Facility: "主坝", Owner: "运行一组", Category: "泄洪",
		RiskLevel: "high", EffectiveAt: time.Now().UTC().Add(time.Hour),
		Evidence: "草稿", RelatedCode: "GU-PERMIT", GateState: "closed",
	}, "operator", "req-create")
	if err != nil {
		t.Fatalf("create directive: %v", err)
	}
	if _, err := f.service.Apply(ctx, permitInput("DP-NOT-APPROVED", created.Code), "operator", "req-apply"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("only approved directive may back a permit, got %v", err)
	}
}
