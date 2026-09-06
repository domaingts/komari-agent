package server

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	v2 "github.com/komari-monitor/komari-agent/protocol/v2"
)

func TestV2PostCompressionHeadersAndLargeIntegers(t *testing.T) {
	old := *flags
	defer func() { *flags = old }()
	flags.ProtocolVersion = 2
	flags.DisableCompression = false
	flags.CFAccessClientID = "client-id"
	flags.CFAccessClientSecret = "client-secret"
	flags.Token = "token"
	resetConnectionProtocolVersion()

	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/api/clients/v2/rpc" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if got := r.Header.Get("CF-Access-Client-Id"); got != "client-id" {
			t.Errorf("missing client id header: %q", got)
		}
		if got := r.Header.Get("CF-Access-Client-Secret"); got != "client-secret" {
			t.Errorf("missing client secret header: %q", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
			return
		}
		if requests == 1 {
			if r.Header.Get("Content-Encoding") != "gzip" {
				t.Errorf("first request encoding = %q", r.Header.Get("Content-Encoding"))
			}
			zr, err := gzip.NewReader(bytes.NewReader(body))
			if err != nil {
				t.Errorf("open gzip body: %v", err)
				return
			}
			body, err = io.ReadAll(zr)
			_ = zr.Close()
			if err != nil {
				t.Errorf("read gzip body: %v", err)
				return
			}
		} else if encoding := r.Header.Get("Content-Encoding"); encoding != "" {
			t.Errorf("uncompressed request encoding = %q", encoding)
		}
		if !bytes.Contains(body, []byte("9007199254740993")) {
			t.Errorf("large integer was not preserved in payload: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":"r","result":{}}`)
	}))
	defer srv.Close()
	flags.Endpoint = srv.URL

	payload := v2.BuildReportRequest("r", []byte(`{"counter":9007199254740993}`))
	if _, err := postV2Request(payload); err != nil {
		t.Fatalf("compressed POST: %v", err)
	}
	flags.DisableCompression = true
	if _, err := postV2Request(payload); err != nil {
		t.Fatalf("uncompressed POST: %v", err)
	}
	if requests != 2 {
		t.Fatalf("got %d requests, want 2", requests)
	}
}

func TestV2PostRejectsMismatchedResponseID(t *testing.T) {
	old := *flags
	defer func() { *flags = old }()
	flags.ProtocolVersion = 2
	flags.DisableCompression = true
	flags.Token = "token"
	resetConnectionProtocolVersion()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":"wrong","result":{}}`)
	}))
	defer srv.Close()
	flags.Endpoint = srv.URL

	payload := v2.BuildReportRequest("expected", []byte(`{"counter":1}`))
	if _, err := postV2Request(payload); err == nil {
		t.Fatal("mismatched response ID was accepted")
	} else if !isV2ProtocolFailure(err) {
		t.Fatalf("mismatched response ID returned unclassified error: %v", err)
	}
}

func TestV1BasicInfoUsesCloudflareHeaders(t *testing.T) {
	old := *flags
	defer func() { *flags = old }()
	flags.ProtocolVersion = 1
	flags.CFAccessClientID = "client-id"
	flags.CFAccessClientSecret = "client-secret"
	flags.Token = "token"

	seen := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/clients/uploadBasicInfo" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("CF-Access-Client-Id") != "client-id" || r.Header.Get("CF-Access-Client-Secret") != "client-secret" {
			t.Errorf("missing Cloudflare headers: %v", r.Header)
		}
		seen <- struct{}{}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	flags.Endpoint = srv.URL

	if err := tryUploadDataWithProtocol(map[string]any{"cpu_cores": 4}, 1); err != nil {
		t.Fatalf("v1 upload: %v", err)
	}
	select {
	case <-seen:
	case <-time.After(time.Second):
		t.Fatal("server did not receive v1 upload")
	}
}

func TestUnsupportedV2RequestIsRejectedWithoutDispatch(t *testing.T) {
	old := *flags
	defer func() { *flags = old }()
	flags.CFAccessClientID = "client-id"
	flags.CFAccessClientSecret = "client-secret"

	serverResponse := make(chan []byte, 1)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		if r.Header.Get("CF-Access-Client-Id") != "client-id" || r.Header.Get("CF-Access-Client-Secret") != "client-secret" {
			t.Errorf("missing websocket Cloudflare headers: %v", r.Header)
		}
		if err := conn.WriteJSON(map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"result":  map[string]string{"status": "ok"},
		}); err != nil {
			t.Errorf("send successful response: %v", err)
			return
		}
		if err := conn.WriteJSON(map[string]any{
			"jsonrpc": "2.0",
			"id":      7,
			"method":  "agent.exec",
			"params":  map[string]string{"command": "must-not-run"},
		}); err != nil {
			t.Errorf("send unsupported request: %v", err)
			return
		}
		_, response, err := conn.ReadMessage()
		if err == nil {
			serverResponse <- response
		}
	}))
	defer srv.Close()
	flags.Endpoint = srv.URL
	client, err := connectWebSocket(buildWebSocketEndpoint(2))
	if err != nil {
		t.Fatalf("connect websocket: %v", err)
	}
	defer client.Close()
	done := make(chan struct{})
	go handleWebSocketMessages(client, 2, done)
	select {
	case response := <-serverResponse:
		var parsed v2.Response
		if err := json.Unmarshal(response, &parsed); err != nil {
			t.Fatalf("decode rejection: %v", err)
		}
		if parsed.ID != float64(7) {
			t.Fatalf("successful response was spuriously rejected: %s", response)
		}
		if parsed.Error == nil || parsed.Error.Code != -32601 {
			t.Fatalf("unexpected rejection: %s", response)
		}
		if strings.Contains(string(response), "must-not-run") {
			t.Fatalf("request parameters leaked into rejection: %s", response)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for unsupported-request rejection")
	}
}
