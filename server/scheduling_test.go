package server

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestNormalizedInterval(t *testing.T) {
	for _, tc := range []struct {
		name     string
		interval float64
		want     float64
	}{
		{"zero", 0, 1},
		{"negative", -1, 1},
		{"subsecond", 0.5, 1},
		{"NaN", math.NaN(), 1},
		{"positive infinity", math.Inf(1), 1},
		{"negative infinity", math.Inf(-1), 1},
		{"default", 1, 1},
		{"fractional", 1.5, 1.5},
		{"long", 60, 60},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizedInterval(tc.interval); got != tc.want {
				t.Fatalf("normalizedInterval(%v) = %v, want %v", tc.interval, got, tc.want)
			}
		})
	}
}

func configureScheduledReporting(t *testing.T, interval float64) {
	t.Helper()
	old := *flags
	t.Cleanup(func() {
		*flags = old
		resetConnectionProtocolVersion()
	})
	flags.ProtocolVersion = 2
	flags.Interval = interval
	flags.MaxRetries = 0
	flags.ReconnectInterval = 0
	flags.DisableCompression = true
	flags.Token = "token"
	flags.CFAccessClientID = "client-id"
	flags.CFAccessClientSecret = "client-secret"
	resetConnectionProtocolVersion()
}

func startScheduledReporting(t *testing.T, report func() []byte) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("reporting loop did not stop after cancellation")
		}
	})
	go func() {
		defer close(done)
		establishWebSocketConnectionWithReport(ctx, report)
	}()
}

func TestWebSocketRecoveryDoesNotWaitForLongReportInterval(t *testing.T) {
	configureScheduledReporting(t, 60)
	connections := make(chan int32, 3)
	var attempts atomic.Int32
	var samples atomic.Int32
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/clients/v2/rpc" || r.Header.Get("CF-Access-Client-Id") != "client-id" || r.Header.Get("CF-Access-Client-Secret") != "client-secret" {
			t.Errorf("unexpected handshake: %s, headers=%v", r.URL, r.Header)
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		attempt := attempts.Add(1)
		connections <- attempt
		if attempt == 1 {
			_, payload, err := conn.ReadMessage()
			if err != nil {
				t.Errorf("read first report: %v", err)
				return
			}
			var envelope struct {
				Method string `json:"method"`
				Params struct {
					Report struct {
						Uptime int `json:"uptime"`
					} `json:"report"`
				} `json:"params"`
			}
			if err := json.Unmarshal(payload, &envelope); err != nil || envelope.Method != "agent.report" || envelope.Params.Report.Uptime != 7 {
				t.Errorf("unexpected first report: %s, error=%v", payload, err)
			}
			return // Drop the socket while the next report is still a minute away.
		}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	flags.Endpoint = srv.URL
	startScheduledReporting(t, func() []byte {
		samples.Add(1)
		return []byte(`{"uptime":7}`)
	})

	for want := int32(1); want <= 2; want++ {
		select {
		case got := <-connections:
			if got != want {
				t.Fatalf("connection attempt = %d, want %d", got, want)
			}
		case <-time.After(4 * time.Second):
			t.Fatal("connection/recovery waited for the long report interval")
		}
	}
	if got := samples.Load(); got != 1 {
		t.Fatalf("connection recovery disturbed the sampling window: %d samples, want 1", got)
	}
}

func TestWebSocketFractionalReportInterval(t *testing.T) {
	configureScheduledReporting(t, 1.5)
	samples := make(chan time.Time, 4)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	flags.Endpoint = srv.URL
	startScheduledReporting(t, func() []byte {
		samples <- time.Now()
		return []byte(`{"uptime":7}`)
	})

	var previous time.Time
	for i := 0; i < 3; i++ {
		select {
		case sampledAt := <-samples:
			if !previous.IsZero() {
				gap := sampledAt.Sub(previous)
				if gap < 1450*time.Millisecond || gap >= 1900*time.Millisecond {
					t.Fatalf("fractional sampling interval = %s, want approximately 1.5s", gap)
				}
			}
			previous = sampledAt
		case <-time.After(4 * time.Second):
			t.Fatal("timed out waiting for a metric sample")
		}
	}
}

func TestWebSocketDowngradeRetainsLegacyReporting(t *testing.T) {
	configureScheduledReporting(t, 1)
	flags.MaxRetries = v2ProtocolFallbackThreshold
	var v2Attempts atomic.Int32
	reports := make(chan []byte, 1)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("CF-Access-Client-Id") != "client-id" || r.Header.Get("CF-Access-Client-Secret") != "client-secret" {
			t.Errorf("missing Cloudflare headers during downgrade: %v", r.Header)
		}
		switch r.URL.Path {
		case "/api/clients/v2/rpc":
			v2Attempts.Add(1)
			http.Error(w, "v2 unsupported", http.StatusNotFound)
		case "/api/clients/report":
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			_, payload, err := conn.ReadMessage()
			if err == nil {
				reports <- payload
			}
			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	flags.Endpoint = srv.URL
	startScheduledReporting(t, func() []byte { return []byte(`{"uptime":7}`) })

	select {
	case payload := <-reports:
		var report struct {
			JSONRPC string `json:"jsonrpc"`
			Uptime  int    `json:"uptime"`
		}
		if err := json.Unmarshal(payload, &report); err != nil || report.JSONRPC != "" || report.Uptime != 7 {
			t.Fatalf("unexpected legacy report: %s, error=%v", payload, err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("legacy websocket reporting was not restored after downgrade")
	}
	if got := v2Attempts.Load(); got != v2ProtocolFallbackThreshold {
		t.Fatalf("v2 attempts = %d, want %d", got, v2ProtocolFallbackThreshold)
	}
	if got := uploadProtocolVersion(); got != 1 {
		t.Fatalf("shared basic-info protocol = %d, want 1", got)
	}
}

func TestPostReportingContinuesDuringBlockedWebSocketRecovery(t *testing.T) {
	configureScheduledReporting(t, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	var attempts atomic.Int32
	var posts atomic.Int32
	stopped := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if websocket.IsWebSocketUpgrade(r) {
			attempts.Add(1)
			<-r.Context().Done()
			close(stopped)
			return
		}
		var request struct {
			ID     string `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.ID == "" || request.Method != "agent.report" {
			t.Errorf("unexpected fallback request: %+v, error=%v", request, err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{}}); err != nil {
			t.Errorf("encode fallback response: %v", err)
		}
		if posts.Add(1) == 3 {
			cancel()
		}
	}))
	defer srv.Close()
	flags.Endpoint = srv.URL

	conn, err := runPostFallbackWithReport(ctx, buildWebSocketEndpoint(2), 1, func() []byte { return []byte(`{"uptime":7}`) })
	if conn != nil {
		conn.Close()
		t.Fatal("blocked recovery unexpectedly returned a websocket")
	}
	if !errors.Is(err, context.Canceled) || posts.Load() != 3 || attempts.Load() != 1 {
		t.Fatalf("fallback did not report independently: posts=%d recovery attempts=%d error=%v", posts.Load(), attempts.Load(), err)
	}
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("blocked recovery handshake was not canceled")
	}
}
