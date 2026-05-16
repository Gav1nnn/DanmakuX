package repository

import (
	"context"
	"time"

	"github.com/Gav1nnn/DanmakuX/internal/model"
	"gorm.io/gorm"
)

// DanmakuRepository 定义弹幕持久化与查询能力。
type DanmakuRepository interface {
	SaveBatch(ctx context.Context, messages []model.Danmaku) error
	ListByRoom(ctx context.Context, roomID string, limit int, before time.Time) ([]model.Danmaku, error)
}

// GormDanmakuRepository 是基于 GORM 的仓储实现。
type GormDanmakuRepository struct {
	db *gorm.DB
}

// NewGormDanmakuRepository 创建 GORM 仓储实现。
func NewGormDanmakuRepository(db *gorm.DB) *GormDanmakuRepository {
	return &GormDanmakuRepository{db: db}
}

// SaveBatch 批量保存弹幕，空切片直接返回。
func (r *GormDanmakuRepository) SaveBatch(ctx context.Context, messages []model.Danmaku) error {
	if len(messages) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Create(&messages).Error
}

// ListByRoom 按房间 + 时间游标逆序查询历史弹幕。
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
