package repository

import (
	"context"
	"time"

	"github.com/Gav1nnn/DanmakuX/internal/model"
	"gorm.io/gorm"
)

type DanmakuRepository interface {
	SaveBatch(ctx context.Context, messages []model.Danmaku) error
	ListByRoom(ctx context.Context, roomID string, limit int, before time.Time) ([]model.Danmaku, error)
}

type GormDanmakuRepository struct {
	db *gorm.DB
}

func NewGormDanmakuRepository(db *gorm.DB) *GormDanmakuRepository {
	return &GormDanmakuRepository{db: db}
}

func (r *GormDanmakuRepository) SaveBatch(ctx context.Context, messages []model.Danmaku) error {
	if len(messages) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Create(&messages).Error
}

func (r *GormDanmakuRepository) ListByRoom(ctx context.Context, roomID string, limit int, before time.Time) ([]model.Danmaku, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if before.IsZero() {
		before = time.Now().Add(24 * time.Hour)
	}

	var out []model.Danmaku
	err := r.db.WithContext(ctx).
		Where("room_id = ? AND created_at < ?", roomID, before).
		Order("created_at DESC").
		Limit(limit).
		Find(&out).Error
	return out, err
}
