package repository

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/blueship581/hydropower-gate-dispatch-control/backend/internal/dto"
	"github.com/blueship581/hydropower-gate-dispatch-control/backend/internal/model"
	"gorm.io/gorm"
)

// DispatchPermitRepository owns all persistence operations for 调度许可.
type DispatchPermitRepository interface {
	List(context.Context, dto.PageQuery) (Page[model.DispatchPermit], error)
	Get(context.Context, uint) (model.DispatchPermit, error)
	CreateWithDecision(context.Context, *model.DispatchPermit, *model.PermitDecision, *model.AuditLog) error
	ExpireStaleSlots(context.Context, time.Time, string) ([]model.DispatchPermit, error)
	CountActiveSlot(context.Context, string) (int64, error)
	SaveWithDecision(context.Context, uint, uint, *model.DispatchPermit, *model.PermitDecision, *model.AuditLog) error
	CountByStatus(context.Context) (map[string]int64, error)
}

type dispatchPermitRepository struct {
	store *Store[model.DispatchPermit]
	db    *gorm.DB
}

func NewDispatchPermitRepository(db *gorm.DB) DispatchPermitRepository {
	return &dispatchPermitRepository{store: NewStore[model.DispatchPermit](db), db: db}
}

func (r *dispatchPermitRepository) List(ctx context.Context, q dto.PageQuery) (Page[model.DispatchPermit], error) {
	page, err := r.store.List(ctx, q)
	if err != nil {
		return Page[model.DispatchPermit]{}, err
	}
	for index := range page.Items {
		if err := databaseForContext(ctx, r.db).Where("permit_id = ?", page.Items[index].ID).
			Order("created_at ASC, id ASC").Find(&page.Items[index].Decisions).Error; err != nil {
			return Page[model.DispatchPermit]{}, err
		}
	}
	return page, nil
}

func (r *dispatchPermitRepository) Get(ctx context.Context, id uint) (model.DispatchPermit, error) {
	var item model.DispatchPermit
	err := databaseForContext(ctx, r.db).Preload("Decisions", func(db *gorm.DB) *gorm.DB {
		return db.Order("created_at ASC, id ASC")
	}).First(&item, id).Error
	return item, err
}

func (r *dispatchPermitRepository) CreateWithDecision(ctx context.Context, item *model.DispatchPermit, decision *model.PermitDecision, audit *model.AuditLog) error {
	return databaseForContext(ctx, r.db).Transaction(func(tx *gorm.DB) error {
		if err := tx.WithContext(ctx).Create(item).Error; err != nil {
			if isUniqueConstraintError(err) && item.ActiveSlot != nil {
				return ErrDuplicatePermit
			}
			return err
		}
		decision.PermitID = item.ID
		if err := tx.WithContext(ctx).Create(decision).Error; err != nil {
			return err
		}
		audit.EntityID = item.ID
		return tx.WithContext(ctx).Create(audit).Error
	})
}

func (r *dispatchPermitRepository) ExpireStaleSlots(ctx context.Context, now time.Time, requestID string) ([]model.DispatchPermit, error) {
	var stale []model.DispatchPermit
	db := databaseForContext(ctx, r.db)
	if err := db.Where("active_slot IS NOT NULL AND valid_until < ?", now.UTC()).Find(&stale).Error; err != nil {
		return nil, err
	}
	for index := range stale {
		before := stale[index].Status
		result := db.Model(&model.DispatchPermit{}).
			Where("id = ? AND active_slot IS NOT NULL AND valid_until < ?", stale[index].ID, now.UTC()).
			Updates(map[string]any{
				"active_slot": nil, "status": "expired", "version": gorm.Expr("version + 1"),
				"closed_by": "system", "closed_at": now.UTC(), "updated_at": now.UTC(),
			})
		if result.Error != nil {
			return nil, result.Error
		}
		if result.RowsAffected == 0 {
			continue
		}
		closedAt := now.UTC()
		stale[index].ActiveSlot, stale[index].Status, stale[index].Version = nil, "expired", stale[index].Version+1
		stale[index].ClosedBy, stale[index].ClosedAt = "system", &closedAt
		audit := &model.AuditLog{Actor: "system", RequestID: requestID, Action: "permit_expired",
			EntityType: "DispatchPermit", EntityID: stale[index].ID, BeforeState: before, AfterState: "expired",
			Detail: "有效期结束，调度许可自动失效并释放闸门槽位", CreatedAt: now.UTC()}
		if err := db.Create(audit).Error; err != nil {
			return nil, err
		}
	}
	return stale, nil
}

func (r *dispatchPermitRepository) CountActiveSlot(ctx context.Context, gateCode string) (int64, error) {
	var count int64
	err := databaseForContext(ctx, r.db).Model(&model.DispatchPermit{}).
		Where("active_slot = ?", strings.ToUpper(strings.TrimSpace(gateCode))).Count(&count).Error
	return count, err
}

func (r *dispatchPermitRepository) SaveWithDecision(ctx context.Context, id, expectedVersion uint, item *model.DispatchPermit, decision *model.PermitDecision, audit *model.AuditLog) error {
	return databaseForContext(ctx, r.db).Transaction(func(tx *gorm.DB) error {
		if err := r.store.update(tx, id, expectedVersion, item); err != nil {
			return err
		}
		if decision != nil {
			decision.PermitID = id
			if err := tx.Create(decision).Error; err != nil {
				return err
			}
		}
		return tx.Create(audit).Error
	})
}

func (r *dispatchPermitRepository) CountByStatus(ctx context.Context) (map[string]int64, error) {
	return r.store.CountByStatus(ctx)
}

var ErrDuplicatePermit = errors.New("an effective dispatch permit already exists for this gate")

// isUniqueConstraintError covers PostgreSQL ("duplicate key"), SQLite ("UNIQUE
// constraint failed", both mattn and glebarez drivers) and MySQL error 1062.
func isUniqueConstraintError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "duplicate key") ||
		strings.Contains(message, "unique constraint") ||
		strings.Contains(message, "error 1062")
}
