package broker

import (
	"context"

	"github.com/Gav1nnn/DanmakuX/pkg/protocol"
)

// Handler 定义订阅方收到广播消息后的处理函数。
type Handler func(ctx context.Context, message protocol.BroadcastMessage) error

// Broker 抽象节点间消息广播能力（发布、订阅、关闭）。
type Broker interface {
	Publish(ctx context.Context, msg protocol.BroadcastMessage) error
	Subscribe(ctx context.Context, roomID string, handler Handler) (unsubscribe func() error, err error)
	Close() error
}
