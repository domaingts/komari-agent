package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/komari-monitor/komari-agent/dnsresolver"
	"github.com/komari-monitor/komari-agent/monitoring"
	"github.com/komari-monitor/komari-agent/protocol/transport"
	v2 "github.com/komari-monitor/komari-agent/protocol/v2"
	"github.com/komari-monitor/komari-agent/utils"
	"github.com/komari-monitor/komari-agent/ws"
)

// EstablishWebSocketConnection owns one report connection at a time. The
// reader goroutine only parses messages and rejects unsupported requests; all
// writes (reports and heartbeat pings) go through SafeConn.
func EstablishWebSocketConnection() {
	establishWebSocketConnectionContext(context.Background())
}

func establishWebSocketConnectionContext(ctx context.Context) {
	var conn *ws.SafeConn
	var readDone <-chan struct{}
	var stopConnWatch func()
	defer func() {
		if stopConnWatch != nil {
			stopConnWatch()
		}
		closeWebSocketAndWait(conn, readDone)
		resetConnectionProtocolVersion()
	}()

	interval := normalizedInterval(flags.Interval)
	dataTicker := time.NewTicker(time.Duration(interval * float64(time.Second)))
	defer dataTicker.Stop()
	heartbeatTicker := time.NewTicker(30 * time.Second)
	defer heartbeatTicker.Stop()

	nextProtocol := requestedProtocolVersion()
	activeProtocol := 0

	for {
		select {
		case <-ctx.Done():
			return
		case <-dataTicker.C:
			if conn == nil {
				var err error
				connectProtocol := nextProtocol
				retry := 0
				for retry <= flags.MaxRetries {
					if retry > 0 {
						log.Println("Retrying websocket connection, attempt:", retry)
					}
					conn, err = connectWebSocketContext(ctx, buildWebSocketEndpoint(connectProtocol))
					if err == nil {
						activeProtocol = connectProtocol
						nextProtocol = connectProtocol
						setConnectionProtocolVersion(activeProtocol)
						log.Printf("WebSocket connected using v%d protocol", activeProtocol)
						done := make(chan struct{})
						readDone = done
						stopConnWatch = watchConnectionContext(ctx, conn)
						go handleWebSocketMessages(conn, activeProtocol, done)
						break
					}
					if connectProtocol >= 2 && shouldFallbackToV1(connectProtocol, err) {
						log.Printf("v2 WebSocket endpoint failed (%v), falling back to v1", err)
						connectProtocol = 1
						nextProtocol = 1
						retry = 0
						continue
					}
					log.Println("Failed to connect to WebSocket:", err)
					retry++
					if retry <= flags.MaxRetries && !waitForReconnect(ctx) {
						return
					}
				}

				if conn == nil {
					log.Println("Max retries reached.")
					if connectProtocol < 2 {
						return
					}
					var fallbackErr error
					conn, fallbackErr = runPostFallbackContext(ctx, buildWebSocketEndpoint(2), interval)
					if fallbackErr != nil {
						var thresholdErr *v2FallbackError
						if errors.As(fallbackErr, &thresholdErr) {
							log.Printf("v2 POST fallback failed (%v), falling back to v1", fallbackErr)
							nextProtocol = 1
							setConnectionProtocolVersion(1)
							continue
						}
						log.Println("POST fallback stopped:", fallbackErr)
						return
					}
					activeProtocol = 2
					nextProtocol = 2
					setConnectionProtocolVersion(activeProtocol)
					log.Println("WebSocket recovered from POST fallback")
					done := make(chan struct{})
					readDone = done
					stopConnWatch = watchConnectionContext(ctx, conn)
					go handleWebSocketMessages(conn, activeProtocol, done)
				}
			}

			if conn == nil {
				continue
			}
			data := monitoring.GenerateReport()
			if activeProtocol >= 2 {
				data = v2.BuildReportPayload(data)
			}
			if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
				log.Println("Failed to send WebSocket message:", err)
				oldConn, oldReadDone, oldStopWatch := conn, readDone, stopConnWatch
				conn = nil
				readDone = nil
				stopConnWatch = nil
				if oldStopWatch != nil {
					oldStopWatch()
				}
				closeWebSocketAndWait(oldConn, oldReadDone)
				activeProtocol = 0
				resetConnectionProtocolVersion()
				if requestedProtocolVersion() >= 2 {
					nextProtocol = 2
				}
			}

		case <-heartbeatTicker.C:
			if conn == nil {
				continue
			}
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				log.Println("Failed to send heartbeat:", err)
				oldConn, oldReadDone, oldStopWatch := conn, readDone, stopConnWatch
				conn = nil
				readDone = nil
				stopConnWatch = nil
				if oldStopWatch != nil {
					oldStopWatch()
				}
				closeWebSocketAndWait(oldConn, oldReadDone)
				activeProtocol = 0
				resetConnectionProtocolVersion()
				if requestedProtocolVersion() >= 2 {
					nextProtocol = 2
				}
			}

		case <-readDone:
			// Reader termination is a connection failure even if no writer has
			// attempted an operation yet. Close and clear everything before the
			// next tick so a second reporter cannot be spawned for this socket.
			log.Println("WebSocket disconnected")
			downgraded := requestedProtocolVersion() >= 2 && uploadProtocolVersion() == 1
			oldConn, oldReadDone, oldStopWatch := conn, readDone, stopConnWatch
			conn = nil
			readDone = nil
			stopConnWatch = nil
			if oldStopWatch != nil {
				oldStopWatch()
			}
			closeWebSocketAndWait(oldConn, oldReadDone)
			activeProtocol = 0
			resetConnectionProtocolVersion()
			if downgraded {
				nextProtocol = 1
			} else if requestedProtocolVersion() >= 2 {
				nextProtocol = 2
			}
		}
	}
}

func normalizedInterval(interval float64) float64 {
	if interval < 1 || math.IsNaN(interval) || math.IsInf(interval, 0) {
		return 1
	}
	return interval
}

func reconnectDelay() time.Duration {
	if flags.ReconnectInterval <= 0 {
		return 0
	}
	return time.Duration(flags.ReconnectInterval) * time.Second
}

func recoveryInterval() time.Duration {
	if delay := reconnectDelay(); delay > 0 {
		return delay
	}
	return time.Second
}

func waitForReconnect(ctx context.Context) bool {
	timer := time.NewTimer(reconnectDelay())
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func closeWebSocketAndWait(conn *ws.SafeConn, done <-chan struct{}) {
	if conn == nil {
		return
	}
	_ = conn.Close()
	if done != nil {
		<-done
	}
}

func watchConnectionContext(ctx context.Context, conn *ws.SafeConn) func() {
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-stop:
		}
	}()
	return func() {
		close(stop)
		<-done
	}
}

func buildPanelEndpoint(path string) string {
	endpoint := strings.TrimSuffix(flags.Endpoint, "/") + path
	if converted, err := utils.ConvertIDNToASCII(endpoint); err == nil {
		return converted
	}
	return endpoint
}

func buildWebSocketEndpoint(protocolVersion int) string {
	path := "/api/clients/report?token=" + flags.Token
	if protocolVersion >= 2 {
		path = "/api/clients/v2/rpc?token=" + flags.Token
	}
	endpoint := "ws" + strings.TrimPrefix(buildPanelEndpoint(path), "http")
	return endpoint
}

// runPostFallback keeps reporting over HTTP while periodically trying to
// restore the v2 websocket. It does not run any remote-control loop.
func runPostFallback(websocketEndpoint string, interval float64) (*ws.SafeConn, error) {
	return runPostFallbackContext(context.Background(), websocketEndpoint, interval)
}

type websocketRecoveryResult struct {
	conn *ws.SafeConn
	err  error
}

// runPostFallbackContext keeps report POSTs flowing while websocket recovery
// handshakes run independently. A failed handshake is bounded by the dialer's
// handshake timeout and cannot block the report ticker.
func runPostFallbackContext(ctx context.Context, websocketEndpoint string, interval float64) (*ws.SafeConn, error) {
	log.Println("Entering v2 POST fallback mode")
	runCtx, cancel := context.WithCancel(ctx)
	reportTicker := time.NewTicker(time.Duration(normalizedInterval(interval) * float64(time.Second)))
	defer reportTicker.Stop()
	reconnectTicker := time.NewTicker(recoveryInterval())
	defer reconnectTicker.Stop()

	recoveryResults := make(chan websocketRecoveryResult)
	recoveryInFlight := false
	var recoveryWG sync.WaitGroup
	defer func() {
		cancel()
		recoveryWG.Wait()
	}()
	for {
		select {
		case <-runCtx.Done():
			return nil, runCtx.Err()
		case <-reportTicker.C:
			reportID := fmt.Sprintf("report-%d", time.Now().UnixNano())
			_, err := postV2RequestContext(runCtx, v2.BuildReportRequest(reportID, monitoring.GenerateReport()))
			if err != nil {
				if shouldFallbackToV1(2, err) {
					return nil, &v2FallbackError{Err: err}
				}
				if runCtx.Err() == nil {
					log.Println("Failed to POST v2 report:", err)
				}
			}
		case <-reconnectTicker.C:
			if recoveryInFlight {
				continue
			}
			recoveryInFlight = true
			recoveryWG.Add(1)
			go func() {
				defer recoveryWG.Done()
				conn, err := connectWebSocketContext(runCtx, websocketEndpoint)
				select {
				case recoveryResults <- websocketRecoveryResult{conn: conn, err: err}:
					// Ownership transfers to the receiver after the unbuffered send.
				case <-runCtx.Done():
					if conn != nil {
						_ = conn.Close()
					}
				}
			}()
		case result := <-recoveryResults:
			recoveryInFlight = false
			if runCtx.Err() != nil {
				if result.conn != nil {
					_ = result.conn.Close()
				}
				return nil, runCtx.Err()
			}
			if result.err == nil {
				return result.conn, nil
			}
			if shouldFallbackToV1(2, result.err) {
				return nil, &v2FallbackError{Err: result.err}
			}
			log.Println("POST fallback WebSocket recovery failed:", result.err)
		}
	}
}

func postV2Request(payload []byte) (*v2.Response, error) {
	return postV2RequestContext(context.Background(), payload)
}

func postV2RequestContext(ctx context.Context, payload []byte) (*v2.Response, error) {
	endpoint := buildPanelEndpoint("/api/clients/v2/rpc?token=" + flags.Token)
	body := payload
	compressed := false
	var err error
	if !flags.DisableCompression {
		body, err = transport.GzipBytes(payload)
		if err != nil {
			return nil, err
		}
		compressed = true
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if compressed {
		req.Header.Set("Content-Encoding", "gzip")
	}
	utils.SetCloudflareAccessHeaders(req.Header, flags.CFAccessClientID, flags.CFAccessClientSecret)

	client := dnsresolver.GetHTTPClientWithPreference(35*time.Second, flags.PreferIPVersion)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &httpStatusError{StatusCode: resp.StatusCode, Status: resp.Status, Body: string(respBody)}
	}
	rpcResp, err := parseV2Response(respBody)
	if err != nil {
		return nil, err
	}
	if err := validateV2ResponseID(payload, respBody); err != nil {
		return nil, err
	}
	resetV2ProtocolFailures(2)
	return rpcResp, nil
}

func connectWebSocket(websocketEndpoint string) (*ws.SafeConn, error) {
	return connectWebSocketContext(context.Background(), websocketEndpoint)
}

type cancelableNetConn struct {
	net.Conn
	stop      chan struct{}
	closeOnce sync.Once
	closeErr  error
}

func newCancelableNetConn(conn net.Conn, ctx context.Context) net.Conn {
	wrapped := &cancelableNetConn{Conn: conn, stop: make(chan struct{})}
	go func() {
		select {
		case <-ctx.Done():
			_ = wrapped.Close()
		case <-wrapped.stop:
		}
	}()
	return wrapped
}

func (conn *cancelableNetConn) Close() error {
	conn.closeOnce.Do(func() {
		close(conn.stop)
		conn.closeErr = conn.Conn.Close()
	})
	return conn.closeErr
}

func connectWebSocketContext(ctx context.Context, websocketEndpoint string) (*ws.SafeConn, error) {
	dialer := newWSDialer()
	if baseDial := dialer.NetDialContext; baseDial != nil && ctx.Done() != nil {
		dialer.NetDialContext = func(dialCtx context.Context, network, address string) (net.Conn, error) {
			conn, err := baseDial(dialCtx, network, address)
			if err != nil {
				return nil, err
			}
			return newCancelableNetConn(conn, ctx), nil
		}
	}
	conn, resp, err := dialer.DialContext(ctx, websocketEndpoint, newWSHeaders())
	if err != nil {
		if resp != nil && resp.StatusCode != http.StatusSwitchingProtocols {
			return nil, &httpStatusError{StatusCode: resp.StatusCode, Status: resp.Status}
		}
		return nil, err
	}
	safe := ws.NewSafeConn(conn)
	// Gorilla's default ping handler writes directly from the reader goroutine.
	// Route pong frames through SafeConn so report, ping, and pong writes remain
	// serialized under one mutex.
	if raw := safe.GetConn(); raw != nil {
		raw.SetPingHandler(func(appData string) error {
			return safe.WriteMessage(websocket.PongMessage, []byte(appData))
		})
	}
	return safe, nil
}

// handleWebSocketMessages intentionally has no task/control dispatch. It
// remains a reader so server-side close/error frames promptly trigger cleanup.
func handleWebSocketMessages(conn *ws.SafeConn, protocolVersion int, done chan<- struct{}) {
	defer close(done)
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			log.Println("WebSocket read error:", err)
			return
		}
		var message struct {
			JSONRPC string          `json:"jsonrpc,omitempty"`
			Method  string          `json:"method,omitempty"`
			ID      json.RawMessage `json:"id,omitempty"`
			Result  json.RawMessage `json:"result,omitempty"`
			Error   *v2.RPCError    `json:"error,omitempty"`
			Message string          `json:"message,omitempty"`
		}
		if err := json.Unmarshal(raw, &message); err != nil {
			log.Println("Bad ws message:", err)
			continue
		}
		if protocolVersion >= 2 && message.JSONRPC == v2.Version {
			if message.Method == "" {
				// Responses to report/handshake requests are not requests and must
				// never receive a second response. A valid response also proves the
				// v2 exchange is healthy and clears transient failure state.
				if message.Error != nil {
					_, fallback := noteV2AttemptResult(2, newV2ProtocolError(fmt.Errorf("v2 rpc error %d: %s", message.Error.Code, message.Error.Message)))
					if fallback {
						// Preserve the downgrade signal until the owner observes done.
						setConnectionProtocolVersion(1)
						_ = conn.Close()
						return
					}
				} else {
					resetV2ProtocolFailures(2)
				}
				continue
			}
			rejectUnsupportedV2Request(conn, message.Method, message.ID)
			continue
		}
		// Legacy control messages are deliberately inert in the reporting-only agent.
	}
}

func rejectUnsupportedV2Request(conn *ws.SafeConn, method string, id json.RawMessage) {
	trimmedID := bytes.TrimSpace(id)
	if len(trimmedID) == 0 || bytes.Equal(trimmedID, []byte("null")) {
		return // JSON-RPC notification: no response is required.
	}
	response := v2.Response{
		JSONRPC: v2.Version,
		ID:      json.RawMessage(id),
		Error: &v2.RPCError{
			Code:    -32601,
			Message: "method not supported",
			Data:    map[string]string{"method": method},
		},
	}
	if err := conn.WriteJSON(response); err != nil {
		log.Println("Failed to reject unsupported v2 request:", err)
	}
}

func newWSDialer() *websocket.Dialer {
	d := &websocket.Dialer{
		HandshakeTimeout:  15 * time.Second,
		NetDialContext:    dnsresolver.GetDialContextWithPreference(15*time.Second, flags.PreferIPVersion),
		Proxy:             http.ProxyFromEnvironment,
		EnableCompression: !flags.DisableCompression,
	}
	if flags.IgnoreUnsafeCert {
		d.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return d
}

func newWSHeaders() http.Header {
	headers := make(http.Header)
	utils.SetCloudflareAccessHeaders(headers, flags.CFAccessClientID, flags.CFAccessClientSecret)
	return headers
}
