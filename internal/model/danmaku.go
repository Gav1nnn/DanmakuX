package model

import "time"

// Danmaku is a persisted barrage message.
type Danmaku struct {
	ID        string    `gorm:"primaryKey;type:char(36)" json:"id"`
	RoomID    string    `gorm:"index;size:64;not null" json:"room_id"`
	UserID    string    `gorm:"index;size:64;not null" json:"user_id"`
	Content   string    `gorm:"type:text;not null" json:"content"`
	CreatedAt time.Time `gorm:"index;autoCreateTime" json:"created_at"`
}
