package metrics

import (
	"fmt"
	"strings"
	"sync/atomic"
)

// Metrics 保存服务运行期的核心观测指标。
type Metrics struct {
	wsConnectionsActive atomic.Int64
	wsConnectionsTotal  atomic.Int64

	messagesAccepted      atomic.Int64
	messagesPublished     atomic.Int64
	messagesInvalid       atomic.Int64
	messagesRateLimited   atomic.Int64
	messagesUserLimited   atomic.Int64
	messagesIPLimited     atomic.Int64
	messagesRoomLimited   atomic.Int64
	messagesBroadcast     atomic.Int64
	messagesBroadcastDrop atomic.Int64

	persistQueued  atomic.Int64
	persistDropped atomic.Int64
	persistFailed  atomic.Int64
}

// New 创建指标收集器。
func New() *Metrics {
	return &Metrics{}
}

func (m *Metrics) IncWSConnection() {
	m.wsConnectionsActive.Add(1)
	m.wsConnectionsTotal.Add(1)
}

func (m *Metrics) DecWSConnection() {
	m.wsConnectionsActive.Add(-1)
}

func (m *Metrics) IncMessageAccepted() {
	m.messagesAccepted.Add(1)
}

func (m *Metrics) IncMessagePublished() {
	m.messagesPublished.Add(1)
}

func (m *Metrics) IncMessageInvalid() {
	m.messagesInvalid.Add(1)
}

func (m *Metrics) IncMessageRateLimited(scope string) {
	m.messagesRateLimited.Add(1)
	switch scope {
	case "user":
		m.messagesUserLimited.Add(1)
	case "ip":
		m.messagesIPLimited.Add(1)
	case "room":
		m.messagesRoomLimited.Add(1)
	}
}

func (m *Metrics) AddMessageBroadcast(n int) {
	if n > 0 {
		m.messagesBroadcast.Add(int64(n))
	}
}

func (m *Metrics) AddMessageBroadcastDrop(n int) {
	if n > 0 {
		m.messagesBroadcastDrop.Add(int64(n))
	}
}

func (m *Metrics) IncPersistQueued() {
	m.persistQueued.Add(1)
}

func (m *Metrics) IncPersistDropped() {
	m.persistDropped.Add(1)
}

func (m *Metrics) IncPersistFailed() {
	m.persistFailed.Add(1)
}

// PrometheusText 以 Prometheus text exposition 格式输出指标。
func (m *Metrics) PrometheusText() string {
	var b strings.Builder
	writeMetric(&b, "danmakux_ws_connections_active", "gauge", "Current active WebSocket connections.", m.wsConnectionsActive.Load())
	writeMetric(&b, "danmakux_ws_connections_total", "counter", "Total accepted WebSocket connections.", m.wsConnectionsTotal.Load())
	writeMetric(&b, "danmakux_messages_accepted_total", "counter", "Total valid danmaku messages accepted from clients.", m.messagesAccepted.Load())
	writeMetric(&b, "danmakux_messages_published_total", "counter", "Total danmaku messages published to broker.", m.messagesPublished.Load())
	writeMetric(&b, "danmakux_messages_invalid_total", "counter", "Total invalid danmaku messages rejected.", m.messagesInvalid.Load())
	writeMetric(&b, "danmakux_messages_rate_limited_total", "counter", "Total danmaku messages rejected by rate limit.", m.messagesRateLimited.Load())
	writeMetric(&b, "danmakux_messages_user_limited_total", "counter", "Total danmaku messages rejected by the user limit.", m.messagesUserLimited.Load())
	writeMetric(&b, "danmakux_messages_ip_limited_total", "counter", "Total danmaku messages rejected by the IP limit.", m.messagesIPLimited.Load())
	writeMetric(&b, "danmakux_messages_room_limited_total", "counter", "Total danmaku messages rejected by the room limit.", m.messagesRoomLimited.Load())
	writeMetric(&b, "danmakux_messages_broadcast_total", "counter", "Total local WebSocket message enqueue successes.", m.messagesBroadcast.Load())
	writeMetric(&b, "danmakux_messages_broadcast_dropped_total", "counter", "Total local WebSocket message enqueue drops.", m.messagesBroadcastDrop.Load())
	writeMetric(&b, "danmakux_persist_queued_total", "counter", "Total messages queued for persistence.", m.persistQueued.Load())
	writeMetric(&b, "danmakux_persist_dropped_total", "counter", "Total messages dropped because persistence queue was full.", m.persistDropped.Load())
	writeMetric(&b, "danmakux_persist_failed_total", "counter", "Total failed persistence batches.", m.persistFailed.Load())
	return b.String()
}

func writeMetric(b *strings.Builder, name, typ, help string, value int64) {
	fmt.Fprintf(b, "# HELP %s %s\n", name, help)
	fmt.Fprintf(b, "# TYPE %s %s\n", name, typ)
	fmt.Fprintf(b, "%s %d\n", name, value)
}
