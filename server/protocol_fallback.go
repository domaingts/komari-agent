package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	v2 "github.com/komari-monitor/komari-agent/protocol/v2"
)

// httpStatusError keeps the response body for diagnostics and, importantly,
// distinguishes an HTTP/protocol response from a transport failure. Only the
// former is eligible to count toward a v2-to-v1 downgrade.
type httpStatusError struct {
	StatusCode int
	Status     string
	Body       string
}

func (e *httpStatusError) Error() string {
	if e == nil {
		return ""
	}
	if e.Body != "" {
		return fmt.Sprintf("status code: %d,%s", e.StatusCode, e.Body)
	}
	if e.Status != "" {
		return e.Status
	}
	return fmt.Sprintf("status code: %d", e.StatusCode)
}

// v2ProtocolError marks a syntactically valid HTTP exchange which cannot be
// understood as a v2 JSON-RPC response (or which contains a JSON-RPC error).
type v2ProtocolError struct{ Err error }

// v2FallbackError marks an error whose threshold was already recorded by the
// POST fallback loop. The outer lifecycle must not count it a second time.
type v2FallbackError struct{ Err error }

func (e *v2FallbackError) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

func (e *v2FallbackError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func (e *v2ProtocolError) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

func (e *v2ProtocolError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

const v2ProtocolFallbackThreshold = 3

func isHTTPStatus(err error, statusCode int) bool {
	var statusErr *httpStatusError
	return errors.As(err, &statusErr) && statusErr.StatusCode == statusCode
}

func newV2ProtocolError(err error) error {
	if err == nil {
		return nil
	}
	return &v2ProtocolError{Err: err}
}

func isV2ProtocolFailure(err error) bool {
	var statusErr *httpStatusError
	if errors.As(err, &statusErr) {
		return true
	}
	var protocolErr *v2ProtocolError
	return errors.As(err, &protocolErr)
}

func noteV2AttemptResult(protocolVersion int, err error) (int, bool) {
	if protocolVersion < 2 || requestedProtocolVersion() < 2 {
		return 0, false
	}
	runtimeProtocolState.Lock()
	defer runtimeProtocolState.Unlock()
	if err == nil {
		runtimeProtocolState.v2ProtocolFailures = 0
		return 0, false
	}
	// DNS, dial, timeout, and other transport errors are transient and must not
	// force a protocol downgrade.
	if !isV2ProtocolFailure(err) {
		return runtimeProtocolState.v2ProtocolFailures, false
	}
	runtimeProtocolState.v2ProtocolFailures++
	return runtimeProtocolState.v2ProtocolFailures, runtimeProtocolState.v2ProtocolFailures >= v2ProtocolFallbackThreshold
}

func resetV2ProtocolFailures(protocolVersion int) {
	_, _ = noteV2AttemptResult(protocolVersion, nil)
}

func shouldFallbackToV1(protocolVersion int, err error) bool {
	_, fallback := noteV2AttemptResult(protocolVersion, err)
	return fallback
}

func parseV2Response(body []byte) (*v2.Response, error) {
	var rpcResp v2.Response
	if err := json.Unmarshal(body, &rpcResp); err != nil {
		return nil, newV2ProtocolError(fmt.Errorf("invalid v2 JSON-RPC response: %w, body: %s", err, bodySnippet(body)))
	}
	if rpcResp.JSONRPC != v2.Version {
		return nil, newV2ProtocolError(fmt.Errorf("invalid v2 JSON-RPC version %q, body: %s", rpcResp.JSONRPC, bodySnippet(body)))
	}
	if rpcResp.Error != nil {
		return &rpcResp, newV2ProtocolError(fmt.Errorf("v2 rpc error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message))
	}
	return &rpcResp, nil
}

func validateV2ResponseID(requestBody, responseBody []byte) error {
	var request struct {
		ID json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(requestBody, &request); err != nil {
		return newV2ProtocolError(fmt.Errorf("invalid v2 request: %w", err))
	}
	requestID := bytes.TrimSpace(request.ID)
	if len(requestID) == 0 || bytes.Equal(requestID, []byte("null")) {
		return nil
	}

	var response struct {
		ID json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return newV2ProtocolError(fmt.Errorf("invalid v2 response ID: %w", err))
	}
	responseID := bytes.TrimSpace(response.ID)
	if len(responseID) == 0 || !bytes.Equal(requestID, responseID) {
		return newV2ProtocolError(fmt.Errorf("v2 response ID mismatch: expected %s, got %s", requestID, responseID))
	}
	return nil
}

func bodySnippet(body []byte) string {
	const max = 120
	if len(body) > max {
		body = body[:max]
	}
	return fmt.Sprintf("%q", string(body))
}

func requestedProtocolVersion() int {
	// Cobra initializes the default to two, but treating a zero-value Config as
	// the default keeps direct library use and tests consistent with the CLI.
	if flags.ProtocolVersion == 0 || flags.ProtocolVersion >= 2 {
		return 2
	}
	return 1
}

// runtimeProtocolState is shared by the websocket and basic-info paths. A
// successful v2 exchange clears the failure count; a v1 downgrade remains in
// force until the connection lifecycle is reset.
var runtimeProtocolState struct {
	sync.RWMutex
	connectionProtocol int
	v2ProtocolFailures int
}

func setConnectionProtocolVersion(version int) {
	runtimeProtocolState.Lock()
	defer runtimeProtocolState.Unlock()
	runtimeProtocolState.connectionProtocol = version
	if version >= 2 {
		runtimeProtocolState.v2ProtocolFailures = 0
	}
}

func resetConnectionProtocolVersion() {
	runtimeProtocolState.Lock()
	defer runtimeProtocolState.Unlock()
	runtimeProtocolState.connectionProtocol = 0
	runtimeProtocolState.v2ProtocolFailures = 0
}

func uploadProtocolVersion() int {
	runtimeProtocolState.RLock()
	defer runtimeProtocolState.RUnlock()
	if runtimeProtocolState.connectionProtocol > 0 {
		return runtimeProtocolState.connectionProtocol
	}
	return requestedProtocolVersion()
}
