package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestControlMessagesRemainInert(t *testing.T) {
	for _, protocol := range []int{1, 2} {
		t.Run("v"+strconv.Itoa(protocol), func(t *testing.T) {
			configureScheduledReporting(t, 1)
			flags.ProtocolVersion = protocol
			marker := filepath.Join(t.TempDir(), "must-not-be-created")
			keptFile := filepath.Join(t.TempDir(), "must-not-be-removed")
			if err := os.WriteFile(keptFile, []byte("keep"), 0600); err != nil {
				t.Fatal(err)
			}
			var networkCalls atomic.Int32
			spy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				networkCalls.Add(1)
				w.WriteHeader(http.StatusOK)
			}))
			t.Cleanup(spy.Close)
			params := map[string]any{
				"command": "touch '" + marker + "'", "task_id": "task-1",
				"type": "http", "ping_type": "http", "url": spy.URL, "target": spy.URL,
				"uuid": "file-1", "request_id": "file-request-1", "op": "remove",
				"args": map[string]string{"path": keptFile}, "secret": "must-not-leak",
			}
			if protocol == 2 {
				for i := 0; i < 2; i++ {
					noteV2AttemptResult(2, newV2ProtocolError(errors.New("previous protocol failure")))
				}
			}

			handled := make(chan struct{})
			var pongSeen atomic.Bool
			upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(handled)
				if r.Header.Get("CF-Access-Client-Id") != "client-id" || r.Header.Get("CF-Access-Client-Secret") != "client-secret" {
					t.Errorf("missing Cloudflare headers: %v", r.Header)
				}
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					t.Errorf("upgrade control-test connection: %v", err)
					return
				}
				defer conn.Close()
				write := func(message any) bool {
					if err := conn.WriteJSON(message); err != nil {
						t.Errorf("send control message: %v", err)
						return false
					}
					return true
				}
				if protocol == 2 {
					if !write(map[string]any{"jsonrpc": "2.0", "id": "report", "result": map[string]any{}}) {
						return
					}
					for _, method := range []string{"agent.exec", "agent.file", "agent.terminal.request", "agent.ping", "agent.event"} {
						if !write(map[string]any{"jsonrpc": "2.0", "method": method, "params": params}) ||
							!write(map[string]any{"jsonrpc": "2.0", "id": nil, "method": method, "params": params}) {
							return
						}
					}
				}
				for _, message := range []string{"exec", "terminal", "ping", "file"} {
					if !write(map[string]any{
						"message": message, "command": params["command"], "task_id": 7,
						"request_id": "terminal-1", "ping_type": "http", "url": spy.URL,
						"params": params,
					}) {
						return
					}
				}
				if protocol == 2 {
					for _, request := range []struct {
						method string
						id     string
					}{
						{"agent.exec", "0"},
						{"agent.ping", `"ping-1"`},
						{"agent.terminal.request", "9007199254740993"},
						{"agent.file", "18446744073709551615"},
						{"agent.file.result", "5"},
						{"agent.event", "6"},
						{"agent.pull", `"pull-1"`},
					} {
						if !write(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(request.id), "method": request.method, "params": params}) {
							return
						}
						conn.SetReadDeadline(time.Now().Add(2 * time.Second))
						_, response, err := conn.ReadMessage()
						if err != nil {
							t.Errorf("read unsupported-method response: %v", err)
							return
						}
						var rejection struct {
							JSONRPC string          `json:"jsonrpc"`
							ID      json.RawMessage `json:"id"`
							Error   struct {
								Code int               `json:"code"`
								Data map[string]string `json:"data"`
							} `json:"error"`
						}
						if err := json.Unmarshal(response, &rejection); err != nil || rejection.JSONRPC != "2.0" || string(rejection.ID) != request.id || rejection.Error.Code != -32601 || rejection.Error.Data["method"] != request.method {
							t.Errorf("invalid rejection for %s: %s, error=%v", request.method, response, err)
							return
						}
						if bytes.Contains(response, []byte("must-not-leak")) || bytes.Contains(response, []byte(marker)) || bytes.Contains(response, []byte(keptFile)) {
							t.Errorf("control parameters leaked into rejection: %s", response)
						}
					}
				}

				// A heartbeat pong proves that all preceding controls were read, even
				// though legacy messages and notifications must produce no response.
				conn.SetPongHandler(func(data string) error {
					if data == "barrier" {
						pongSeen.Store(true)
					}
					return nil
				})
				if err := conn.WriteMessage(websocket.PingMessage, []byte("barrier")); err != nil {
					t.Errorf("send heartbeat barrier: %v", err)
					return
				}
				conn.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
				if _, response, err := conn.ReadMessage(); err == nil {
					t.Errorf("inert control unexpectedly produced a response: %s", response)
				}
			}))
			t.Cleanup(srv.Close)
			flags.Endpoint = srv.URL
			client, err := connectWebSocket(buildWebSocketEndpoint(protocol))
			if err != nil {
				t.Fatalf("connect control-test websocket: %v", err)
			}
			done := make(chan struct{})
			t.Cleanup(func() {
				client.Close()
				select {
				case <-done:
				case <-time.After(2 * time.Second):
					t.Error("control reader did not stop")
				}
			})
			go handleWebSocketMessages(client, protocol, done)
			select {
			case <-handled:
			case <-time.After(4 * time.Second):
				t.Fatal("timed out waiting for control messages to be handled")
			}
			if !pongSeen.Load() {
				t.Error("control messages prevented the retained heartbeat pong")
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Errorf("remote command created a file: %v", err)
			}
			if data, err := os.ReadFile(keptFile); err != nil || string(data) != "keep" {
				t.Errorf("remote file request modified the fixture: %q, error=%v", data, err)
			}
			if got := networkCalls.Load(); got != 0 {
				t.Errorf("remote ping/control initiated %d HTTP requests", got)
			}
			if protocol == 2 {
				count, fallback := noteV2AttemptResult(2, newV2ProtocolError(errors.New("probe after controls")))
				if count != 1 || fallback {
					t.Errorf("control rejection polluted fallback state: failures=%d fallback=%t", count, fallback)
				}
			}
		})
	}
}
