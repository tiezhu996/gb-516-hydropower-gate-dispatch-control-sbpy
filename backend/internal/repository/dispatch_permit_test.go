package repository

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/blueship581/hydropower-gate-dispatch-control/backend/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func newPermitIndexDB(t *testing.T) (*gorm.DB, DispatchPermitRepository) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	if err := db.AutoMigrate(&model.DispatchPermit{}, &model.PermitDecision{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db, NewDispatchPermitRepository(db)
}

func permitRow(code string, directiveID uint) model.DispatchPermit {
	return model.DispatchPermit{
		BaseModel:          model.BaseModel{Code: code, Name: code, Status: "requested", Version: 1},
		DirectiveID:        directiveID,
		ActiveDirectiveKey: &directiveID,
		Action:             "open",
	}
}

func TestActivePermitUniqueIndexAllowsOneActiveAndManyTerminal(t *testing.T) {
	db, permits := newPermitIndexDB(t)
	ctx := context.Background()
	directiveID := uint(7)

	first := permitRow("DP-IDX-1", directiveID)
	if err := permits.CreateWithDecision(ctx, &first, nil); err != nil {
		t.Fatalf("create first active permit: %v", err)
	}

	second := permitRow("DP-IDX-2", directiveID)
	second.Status = "approved"
	err := permits.CreateWithDecision(ctx, &second, nil)
	if err == nil {
		t.Fatal("database must reject two active permits for one directive")
	}
	if !errors.Is(err, ErrActivePermitExists) {
		t.Fatalf("second active permit must map to ErrActivePermitExists, got %v", err)
	}

	// A permit for a different directive may still be active.
	other := permitRow("DP-IDX-OTHER", 8)
	other.Action = "closed"
	if err := permits.CreateWithDecision(ctx, &other, nil); err != nil {
		t.Fatalf("permit for another directive should succeed: %v", err)
	}

	// Once the first permit terminates (active key cleared), a new active permit
	// for the same directive is allowed.
	if err := db.WithContext(ctx).Model(&model.DispatchPermit{}).
		Where("id = ?", first.ID).Updates(map[string]any{"status": "invalidated", "active_directive_key": nil}).Error; err != nil {
		t.Fatalf("terminate first permit: %v", err)
	}
	third := permitRow("DP-IDX-3", directiveID)
	if err := permits.CreateWithDecision(ctx, &third, nil); err != nil {
		t.Fatalf("new active permit after termination should succeed: %v", err)
	}

	var active int64
	if err := db.WithContext(ctx).Model(&model.DispatchPermit{}).
		Where("directive_id = ? AND active_directive_key IS NOT NULL", directiveID).Count(&active).Error; err != nil {
		t.Fatalf("count active permits: %v", err)
	}
	if active != 1 {
		t.Fatalf("exactly one active permit expected for directive, got %d", active)
	}
}
