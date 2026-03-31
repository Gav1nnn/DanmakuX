package broker

import (
	"context"

	"github.com/Gav1nnn/DanmakuX/pkg/protocol"
)

type Handler func(ctx context.Context, message protocol.BroadcastMessage) error

type Broker interface {
	Publish(ctx context.Context, msg protocol.BroadcastMessage) error
	Subscribe(ctx context.Context, roomID string, handler Handler) (unsubscribe func() error, err error)
	Close() error
}
