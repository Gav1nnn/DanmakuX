package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

type authResp struct {
	Token string `json:"token"`
}

type outbound struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

type inbound struct {
	Type    string `json:"type"`
	UserID  string `json:"user_id"`
	Content string `json:"content"`
	Error   string `json:"error"`
}

type stats struct {
	connectedOK    atomic.Int64
	connectFailed  atomic.Int64
	sentOK         atomic.Int64
	sendFailed     atomic.Int64
	received       atomic.Int64
	readErrors     atomic.Int64
	rateLimited    atomic.Int64
	authFailed     atomic.Int64
	authReqSuccess atomic.Int64
}

type errorSamples struct {
	mu   sync.Mutex
	max  int
	list []string
}

func newErrorSamples(max int) *errorSamples {
	if max <= 0 {
		max = 5
	}
	return &errorSamples{max: max}
}

func (e *errorSamples) add(s string) {
	if s == "" {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.list) >= e.max {
		return
	}
	e.list = append(e.list, s)
}

func (e *errorSamples) values() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, len(e.list))
	copy(out, e.list)
	return out
}

type latencyStore struct {
	mu      sync.Mutex
	values  []int64
	dropped atomic.Int64
}

func (l *latencyStore) add(d time.Duration) {
	us := d.Microseconds()
	if us < 0 {
		return
	}
	l.mu.Lock()
	l.values = append(l.values, us)
	l.mu.Unlock()
}

func (l *latencyStore) snapshot() []int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]int64, len(l.values))
	copy(out, l.values)
	return out
}

func main() {
	var (
		baseHTTP     = flag.String("base-http", "http://127.0.0.1:8080", "base HTTP url")
		baseWS       = flag.String("base-ws", "", "base WS url, empty means infer from base-http")
		clients      = flag.Int("clients", 30, "concurrent websocket clients")
		duration     = flag.Duration("duration", 20*time.Second, "test duration")
		sendInterval = flag.Duration("send-interval", 900*time.Millisecond, "send interval per client")
		roomCount    = flag.Int("room-count", 3, "number of rooms")
		roomPrefix   = flag.String("room-prefix", "room-load-", "room prefix")
		timeout      = flag.Duration("http-timeout", 5*time.Second, "http timeout")
	)
	flag.Parse()

	if *clients <= 0 || *duration <= 0 || *sendInterval <= 0 || *roomCount <= 0 {
		fmt.Fprintln(os.Stderr, "invalid arguments")
		os.Exit(1)
	}

	wsBase := *baseWS
	if wsBase == "" {
		wsBase = inferWSBase(*baseHTTP)
	}

	fmt.Println("=== DanmakuX WS Load Test ===")
	fmt.Printf("base-http: %s\n", *baseHTTP)
	fmt.Printf("base-ws: %s\n", wsBase)
	fmt.Printf("clients: %d\n", *clients)
	fmt.Printf("duration: %s\n", *duration)
	fmt.Printf("send-interval: %s\n", *sendInterval)
	fmt.Printf("rooms: %d\n", *roomCount)

	ctx, cancel := context.WithTimeout(context.Background(), *duration)
	defer cancel()

	httpClient := &http.Client{Timeout: *timeout}
	if err := preflight(*baseHTTP, httpClient); err != nil {
		fmt.Fprintf(os.Stderr, "preflight failed: %v\n", err)
		fmt.Fprintln(os.Stderr, "hint: make sure backend is running, e.g. `make run` in another terminal.")
		os.Exit(2)
	}

	var st stats
	var lats latencyStore
	var wg sync.WaitGroup
	inflight := sync.Map{}
	errs := newErrorSamples(8)

	start := time.Now()
	for i := 0; i < *clients; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			worker(ctx, idx, *baseHTTP, wsBase, *roomPrefix, *roomCount, *sendInterval, httpClient, &st, &lats, &inflight, errs)
		}(i)

		// avoid auth spike at startup
		time.Sleep(time.Duration(rand.Intn(30)) * time.Millisecond)
	}

	wg.Wait()
	elapsed := time.Since(start)
	report(elapsed, &st, &lats, &inflight, errs)
}

func worker(
	ctx context.Context,
	idx int,
	baseHTTP string,
	wsBase string,
	roomPrefix string,
	roomCount int,
	sendInterval time.Duration,
	httpClient *http.Client,
	st *stats,
	lats *latencyStore,
	inflight *sync.Map,
	errs *errorSamples,
) {
	userID := fmt.Sprintf("u-load-%d", idx)
	roomID := fmt.Sprintf("%s%d", roomPrefix, idx%roomCount)

	token, err := getToken(ctx, httpClient, baseHTTP, userID)
	if err != nil {
		st.authFailed.Add(1)
		errs.add("auth: " + err.Error())
		return
	}
	st.authReqSuccess.Add(1)

	wsURL := fmt.Sprintf("%s/ws?room_id=%s&token=%s", strings.TrimRight(wsBase, "/"), url.QueryEscape(roomID), url.QueryEscape(token))
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		st.connectFailed.Add(1)
		errs.add("connect: " + err.Error())
		return
	}
	st.connectedOK.Add(1)
	defer conn.Close()

	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				select {
				case <-ctx.Done():
					return
				default:
					st.readErrors.Add(1)
					errs.add("read: " + err.Error())
					return
				}
			}

			var msg inbound
			if err := json.Unmarshal(raw, &msg); err != nil {
				continue
			}
			switch msg.Type {
			case "danmaku":
				st.received.Add(1)
				if sentAtAny, ok := inflight.LoadAndDelete(msg.Content); ok {
					if sentAt, ok2 := sentAtAny.(time.Time); ok2 {
						lats.add(time.Since(sentAt))
					}
				}
			case "error":
				if strings.Contains(strings.ToLower(msg.Error), "limit") {
					st.rateLimited.Add(1)
				}
			}
		}
	}()

	ticker := time.NewTicker(sendInterval)
	defer ticker.Stop()
	seq := 0

	for {
		select {
		case <-ctx.Done():
			_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "bye"))
			<-readDone
			return
		case <-ticker.C:
			content := fmt.Sprintf("load|%s|%d|%d", userID, seq, time.Now().UnixNano())
			seq++
			inflight.Store(content, time.Now())

			err := conn.WriteJSON(outbound{
				Type:    "danmaku",
				Content: content,
			})
			if err != nil {
				st.sendFailed.Add(1)
				inflight.Delete(content)
				errs.add("send: " + err.Error())
				return
			}
			st.sentOK.Add(1)
		}
	}
}

func getToken(ctx context.Context, hc *http.Client, baseHTTP, userID string) (string, error) {
	payload, _ := json.Marshal(map[string]string{"user_id": userID})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseHTTP, "/")+"/api/v1/auth/guest", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("auth status=%d body=%s", resp.StatusCode, string(body))
	}

	var out authResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.Token == "" {
		return "", fmt.Errorf("empty token")
	}
	return out.Token, nil
}

func preflight(baseHTTP string, hc *http.Client) error {
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(baseHTTP, "/")+"/healthz", nil)
	if err != nil {
		return err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("healthz status=%d body=%s", resp.StatusCode, string(body))
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 512))
	if err != nil {
		return err
	}
	if !strings.Contains(string(data), "ok") {
		return errors.New("healthz unexpected response")
	}
	return nil
}

func inferWSBase(baseHTTP string) string {
	u, err := url.Parse(baseHTTP)
	if err != nil {
		return "ws://127.0.0.1:8080"
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	default:
		u.Scheme = "ws"
	}
	u.Path = ""
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	return strings.TrimRight(u.String(), "/")
}

func report(elapsed time.Duration, st *stats, lats *latencyStore, inflight *sync.Map, errs *errorSamples) {
	inflightCount := int64(0)
	inflight.Range(func(_, _ any) bool {
		inflightCount++
		return true
	})

	sent := st.sentOK.Load()
	recv := st.received.Load()
	latVals := lats.snapshot()
	sort.Slice(latVals, func(i, j int) bool { return latVals[i] < latVals[j] })

	fmt.Println()
	fmt.Println("=== Summary ===")
	fmt.Printf("elapsed: %s\n", elapsed.Truncate(time.Millisecond))
	fmt.Printf("auth success: %d, auth failed: %d\n", st.authReqSuccess.Load(), st.authFailed.Load())
	fmt.Printf("connected: %d, connect failed: %d\n", st.connectedOK.Load(), st.connectFailed.Load())
	fmt.Printf("sent ok: %d, send failed: %d, send rps: %.2f\n", sent, st.sendFailed.Load(), float64(sent)/elapsed.Seconds())
	fmt.Printf("received: %d, recv rps: %.2f\n", recv, float64(recv)/elapsed.Seconds())
	fmt.Printf("rate-limited responses: %d\n", st.rateLimited.Load())
	fmt.Printf("read errors: %d\n", st.readErrors.Load())
	fmt.Printf("inflight(not echoed yet): %d\n", inflightCount)
	if errLines := errs.values(); len(errLines) > 0 {
		fmt.Println("sample errors:")
		for _, line := range errLines {
			fmt.Printf("  - %s\n", line)
		}
	}

	if len(latVals) == 0 {
		fmt.Println("latency samples: 0")
		return
	}

	fmt.Printf("latency samples: %d\n", len(latVals))
	fmt.Printf("latency p50: %s\n", formatUS(percentile(latVals, 50)))
	fmt.Printf("latency p95: %s\n", formatUS(percentile(latVals, 95)))
	fmt.Printf("latency p99: %s\n", formatUS(percentile(latVals, 99)))
	fmt.Printf("latency max: %s\n", formatUS(latVals[len(latVals)-1]))
}

func percentile(sorted []int64, p int) int64 {
	if len(sorted) == 0 {
		return 0
	}
	if p <= 0 {
		return sorted[0]
	}
	if p >= 100 {
		return sorted[len(sorted)-1]
	}
	idx := int(float64(len(sorted)-1) * float64(p) / 100.0)
	return sorted[idx]
}

func formatUS(us int64) string {
	return time.Duration(us * int64(time.Microsecond)).String()
}
