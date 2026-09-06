// Package v2 defines the reporting-only JSON-RPC transport.
package v2

import (
	"encoding/json"

	v1 "github.com/komari-monitor/komari-agent/protocol/v1"
)

const (
	Version              = "2.0"
	MethodAgentReport    = "agent.report"
	MethodAgentBasicInfo = "agent.basicInfo"
)

// Request is a JSON-RPC request or notification. Reporting notifications omit
// ID; HTTP POST reports include an ID so a response can be validated.
type Request struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
	ID      any    `json:"id,omitempty"`
}

// Response is the JSON-RPC response returned by a v2 panel.
type Response struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id,omitempty"`
	Result  any       `json:"result,omitempty"`
	Error   *RPCError `json:"error,omitempty"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func NewNotification(method string, params any) []byte {
	payload, _ := json.Marshal(Request{JSONRPC: Version, Method: method, Params: params})
	return payload
}

func NewRequest(id any, method string, params any) []byte {
	payload, _ := json.Marshal(Request{JSONRPC: Version, Method: method, Params: params, ID: id})
	return payload
}

func BuildReportPayload(report v1.ReportPayload) []byte {
	return NewNotification(MethodAgentReport, reportParams{Report: json.RawMessage(report)})
}

func BuildReportRequest(id any, report v1.ReportPayload) []byte {
	return NewRequest(id, MethodAgentReport, reportParams{Report: json.RawMessage(report)})
}

func BuildBasicInfoPayload(info map[string]any) []byte {
	return NewNotification(MethodAgentBasicInfo, map[string]any{"info": info})
}

// reportParams deliberately embeds the report as RawMessage. Re-marshalling a
// decoded map could turn large integer counters into float64 values.
type reportParams struct {
	Report json.RawMessage `json:"report"`
}
