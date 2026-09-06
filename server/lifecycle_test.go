package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestEstablishV2PostFallbackRecoversWebSocket(t *testing.T) {
	old := *flags
	defer func() { *flags = old }()
	flags.ProtocolVersion = 2
	flags.Interval = 1
	flags.MaxRetries = 0
	flags.ReconnectInterval = 0
	flags.DisableCompression = true
	flags.Token = "token"
	resetConnectionProtocolVersion()

	var wsAttempts atomic.Int32
	var wsSuccesses atomic.Int32
	var postReports atomic.Int32
	var allowWebSocket atomic.Bool
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/clients/v2/rpc" {
			http.NotFound(w, r)
			return
		}
		if websocket.IsWebSocketUpgrade(r) {
			wsAttempts.Add(1)
			if !allowWebSocket.Load() {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			wsSuccesses.Add(1)
			defer conn.Close()
			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
			}
		}
		postReports.Add(1)
		allowWebSocket.Store(true)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"report","result":{}}`))
	}))
	defer server.Close()
	flags.Endpoint = server.URL

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		establishWebSocketConnectionContext(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(8 * time.Second):
		t.Fatal("reporting loop did not stop after context cancellation")
	}
	if postReports.Load() == 0 {
		t.Fatal("v2 POST fallback did not report")
	}
	if wsAttempts.Load() < 2 || wsSuccesses.Load() == 0 {
		t.Fatalf("websocket was not recovered: attempts=%d successes=%d", wsAttempts.Load(), wsSuccesses.Load())
	}
}

func TestV2DowngradesToV1AfterClassifiedFailures(t *testing.T) {
	old := *flags
	defer func() { *flags = old }()
	flags.ProtocolVersion = 2
	flags.DisableCompression = true
	flags.Token = "token"
	resetConnectionProtocolVersion()

	var v2Attempts atomic.Int32
	var v1Attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/clients/v2/rpc":
			v2Attempts.Add(1)
			http.Error(w, "unsupported", http.StatusNotFound)
		case "/api/clients/uploadBasicInfo":
			v1Attempts.Add(1)
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	flags.Endpoint = server.URL

	for attempt := 1; attempt <= v2ProtocolFallbackThreshold; attempt++ {
		err := tryUploadData(map[string]any{"cpu_cores": 4})
		if attempt < v2ProtocolFallbackThreshold && err == nil {
			t.Fatalf("attempt %d unexpectedly succeeded", attempt)
		}
		if attempt == v2ProtocolFallbackThreshold && err != nil {
			t.Fatalf("final downgrade attempt failed: %v", err)
		}
	}
	if v2Attempts.Load() != v2ProtocolFallbackThreshold || v1Attempts.Load() != 1 {
		t.Fatalf("unexpected fallback counts: v2=%d v1=%d", v2Attempts.Load(), v1Attempts.Load())
	}
	if got := uploadProtocolVersion(); got != 1 {
		t.Fatalf("protocol state = %d, want v1", got)
	}
}

func TestReaderTerminationClosesDone(t *testing.T) {
	old := *flags
	defer func() { *flags = old }()
	flags.ProtocolVersion = 2
	flags.Token = "token"
	serverClosed := make(chan struct{})
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		_ = conn.Close()
		close(serverClosed)
	}))
	defer server.Close()
	flags.Endpoint = server.URL

	conn, err := connectWebSocket(buildWebSocketEndpoint(2))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close()
	done := make(chan struct{})
	go handleWebSocketMessages(conn, 2, done)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("reader did not terminate after peer close")
	}
	select {
	case <-serverClosed:
	case <-time.After(time.Second):
		t.Fatal("server close was not observed")
	}
}

func TestBasicInfoV2RetryPreservesCloudflareHeaders(t *testing.T) {
	old := *flags
	defer func() { *flags = old }()
	flags.ProtocolVersion = 2
	flags.DisableCompression = true
	flags.CFAccessClientID = "client-id"
	flags.CFAccessClientSecret = "client-secret"
	flags.CustomIpv4 = "127.0.0.1"
	flags.CustomIpv6 = "::1"
	flags.Token = "token"
	resetConnectionProtocolVersion()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/clients/v2/rpc" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("CF-Access-Client-Id") != "client-id" || r.Header.Get("CF-Access-Client-Secret") != "client-secret" {
			t.Errorf("missing Cloudflare headers: %v", r.Header)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read basic info body: %v", err)
			return
		}
		var envelope struct {
			Params struct {
				Info map[string]any `json:"info"`
			} `json:"params"`
		}
		if err := json.Unmarshal(body, &envelope); err != nil {
			t.Errorf("decode basic info body: %v", err)
			return
		}
		requestNumber := requests.Add(1)
		_, hasKernel := envelope.Params.Info["kernel_version"]
		_, hasPhysical := envelope.Params.Info["cpu_physical_cores"]
		if requestNumber == 1 && (!hasKernel || !hasPhysical) {
			t.Errorf("first retry payload dropped fields: %v", envelope.Params.Info)
		}
		if requestNumber == 2 && (hasKernel || hasPhysical) {
			t.Errorf("retry payload retained rejected fields: %v", envelope.Params.Info)
		}
		if requestNumber == 1 {
			http.Error(w, "old field", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","result":{}}`))
	}))
	defer server.Close()
	flags.Endpoint = server.URL

	if err := uploadBasicInfo(); err != nil {
		t.Fatalf("basic info retry: %v", err)
	}
	if requests.Load() != 2 {
		t.Fatalf("basic info requests = %d, want 2", requests.Load())
	}
}

func TestPostFallbackCancelsInFlightRecoveryAfterThreshold(t *testing.T) {
	old := *flags
	defer func() { *flags = old }()
	flags.ProtocolVersion = 2
	flags.DisableCompression = true
	flags.ReconnectInterval = 0
	flags.Token = "token"
	resetConnectionProtocolVersion()

	recoveryStarted := make(chan struct{})
	recoveryStopped := make(chan struct{})
	var started atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if websocket.IsWebSocketUpgrade(r) {
			if started.CompareAndSwap(false, true) {
				close(recoveryStarted)
			}
			<-r.Context().Done()
			close(recoveryStopped)
			return
		}
		http.Error(w, "unsupported", http.StatusNotFound)
	}))
	defer server.Close()
	flags.Endpoint = server.URL
	wsEndpoint := "ws" + server.URL[len("http"):]

	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
	defer cancel()
	result, err := runPostFallbackContext(ctx, wsEndpoint, 1)
	if result != nil {
		_ = result.Close()
		t.Fatal("fallback unexpectedly recovered websocket")
	}
	var thresholdErr *v2FallbackError
	if !errors.As(err, &thresholdErr) {
		t.Fatalf("expected threshold fallback error, got %v", err)
	}
	select {
	case <-recoveryStarted:
	case <-time.After(time.Second):
		t.Fatal("recovery handshake did not start")
	}
	select {
	case <-recoveryStopped:
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight recovery was not canceled")
	}
}

func TestBasicInfoSkipsRetryOnTransportError(t *testing.T) {
	old := *flags
	defer func() { *flags = old }()
	flags.ProtocolVersion = 2
	flags.DisableCompression = true
	flags.CustomIpv4 = "127.0.0.1"
	flags.CustomIpv6 = "::1"
	flags.Token = "token"
	resetConnectionProtocolVersion()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		// Drop the connection so the client observes a transport failure rather
		// than a panel response.
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Errorf("response writer does not support hijacking")
			return
		}
		conn, _, err := hijacker.Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		conn.Close()
	}))
	defer server.Close()
	flags.Endpoint = server.URL

	if err := uploadBasicInfo(); err == nil {
		t.Fatal("expected transport error from basic info upload")
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("basic info requests = %d, want 1 (no compatibility retry)", got)
	}
}

func TestBasicInfoSkipsRetryOnServerError(t *testing.T) {
	old := *flags
	defer func() { *flags = old }()
	flags.ProtocolVersion = 2
	flags.DisableCompression = true
	flags.CustomIpv4 = "127.0.0.1"
	flags.CustomIpv6 = "::1"
	flags.Token = "token"
	resetConnectionProtocolVersion()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "panel restarting", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	flags.Endpoint = server.URL

	if err := uploadBasicInfo(); err == nil {
		t.Fatal("expected 503 error from basic info upload")
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("basic info requests = %d, want 1 (no compatibility retry)", got)
	}
}
