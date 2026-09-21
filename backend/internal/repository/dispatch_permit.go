package repository

import (
	"context"
	"errors"
	"strings"

	"github.com/blueship581/hydropower-gate-dispatch-control/backend/internal/dto"
	"github.com/blueship581/hydropower-gate-dispatch-control/backend/internal/model"
	"gorm.io/gorm"
)

// DispatchPermitRepository owns all persistence operations for 调度许可.
type DispatchPermitRepository interface {
	List(context.Context, dto.PageQuery) (Page[model.DispatchPermit], error)
	Get(context.Context, uint) (model.DispatchPermit, error)
	GetByCode(context.Context, string) (model.DispatchPermit, error)
	CreateWithDecision(context.Context, *model.DispatchPermit, *model.PermitDecision) error
	SaveTransition(context.Context, uint, uint, *model.DispatchPermit, *model.PermitDecision) error
	CountActiveByDirectiveID(context.Context, uint) (int64, error)
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

func (r *dispatchPermitRepository) GetByCode(ctx context.Context, code string) (model.DispatchPermit, error) {
	item, err := r.store.GetByCode(ctx, code)
	if err != nil {
		return model.DispatchPermit{}, err
	}
	return r.Get(ctx, item.ID)
}

// CreateWithDecision persists a new permit together with its first append-only
// decision evidence inside the ambient service transaction.
func (r *dispatchPermitRepository) CreateWithDecision(ctx context.Context, item *model.DispatchPermit, decision *model.PermitDecision) error {
	db := databaseForContext(ctx, r.db)
	if err := translatePermitDuplicate(db.Create(item).Error); err != nil {
		return err
	}
	if decision != nil {
		decision.PermitID = item.ID
		if err := db.Create(decision).Error; err != nil {
			return err
		}
	}
	return nil
}

// translatePermitDuplicate maps unique-index violations on the one-active-permit
// index into a typed error. SQLite, MySQL and PostgreSQL each report different
// text, so the message and constraint name are both inspected.
func translatePermitDuplicate(err error) error {
	if err == nil {
		return nil
	}
	message := strings.ToLower(err.Error())
	isUnique := strings.Contains(message, "unique constraint") ||
		strings.Contains(message, "duplicate key") ||
		strings.Contains(message, "error 1062")
	if isUnique && (strings.Contains(message, "directive_active") || strings.Contains(message, "active_directive_key")) {
		return errors.Join(ErrActivePermitExists, err)
	}
	return err
}

// SaveTransition persists the optimistic-lock update and its immutable decision
// evidence. It participates in an ambient service transaction so activation can
// also advance the directive and gate atomically.
func (r *dispatchPermitRepository) SaveTransition(ctx context.Context, id, version uint, item *model.DispatchPermit, decision *model.PermitDecision) error {
	db := databaseForContext(ctx, r.db)
	if err := r.store.update(db, id, version, item); err != nil {
		return err
	}
	if decision != nil {
		decision.PermitID = id
		if err := db.Create(decision).Error; err != nil {
			return err
		}
	}
	return nil
}

func (r *dispatchPermitRepository) CountActiveByDirectiveID(ctx context.Context, directiveID uint) (int64, error) {
	var count int64
	err := databaseForContext(ctx, r.db).Model(&model.DispatchPermit{}).
		Where("directive_id = ? AND status IN ?", directiveID, []string{"requested", "approved"}).
		Count(&count).Error
	return count, err
}

func (r *dispatchPermitRepository) CountByStatus(ctx context.Context) (map[string]int64, error) {
	counts := make(map[string]int64)
	rows, err := databaseForContext(ctx, r.db).Model(&model.DispatchPermit{}).
		Select("status, COUNT(*) AS total").Group("status").Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var total int64
		if err := rows.Scan(&status, &total); err != nil {
			return nil, err
		}
		counts[strings.TrimSpace(status)] = total
	}
	return counts, rows.Err()
}
