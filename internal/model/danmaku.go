package model

import "time"

// Danmaku 对应 MySQL 中持久化的弹幕记录。
type Danmaku struct {
	ID        string    `gorm:"primaryKey;type:char(36)" json:"id"`
	RoomID    string    `gorm:"index;size:64;not null" json:"room_id"`
	UserID    string    `gorm:"index;size:64;not null" json:"user_id"`
	Content   string    `gorm:"type:text;not null" json:"content"`
	CreatedAt time.Time `gorm:"index;autoCreateTime" json:"created_at"`
}
