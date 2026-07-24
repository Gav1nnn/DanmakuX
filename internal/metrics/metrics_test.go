package metrics

import (
	"strings"
	"testing"
)

func TestPrometheusTextIncludesRateLimitScopes(t *testing.T) {
	collector := New()
	collector.IncMessageRateLimited("user")
	collector.IncMessageRateLimited("ip")
	collector.IncMessageRateLimited("room")

	output := collector.PrometheusText()
	for _, line := range []string{
		"danmakux_messages_rate_limited_total 3",
		"danmakux_messages_user_limited_total 1",
		"danmakux_messages_ip_limited_total 1",
		"danmakux_messages_room_limited_total 1",
	} {
		if !strings.Contains(output, line) {
			t.Fatalf("metrics output missing %q", line)
		}
	}
}
