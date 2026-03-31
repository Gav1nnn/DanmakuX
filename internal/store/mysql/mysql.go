package mysql

import (
	"context"

	"github.com/Gav1nnn/DanmakuX/internal/config"
	"github.com/Gav1nnn/DanmakuX/internal/model"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func New(cfg config.MySQLConfig) (*gorm.DB, error) {
	db, err := gorm.Open(mysql.Open(cfg.DSN), &gorm.Config{})
	if err != nil {
		return nil, err
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(cfg.MaxLifeTime)

	return db, nil
}

func AutoMigrate(ctx context.Context, db *gorm.DB) error {
	return db.WithContext(ctx).AutoMigrate(&model.Danmaku{})
}
