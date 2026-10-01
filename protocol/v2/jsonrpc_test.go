package v2

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestReportEnvelopesPreserveLargeCounters(t *testing.T) {
	report := []byte(`{"network":{"totalUp":9007199254740993,"totalDown":18446744073709551615}}`)
	for _, tc := range []struct {
		name  string
		build func([]byte) []byte
		id    string
	}{
		{name: "notification", build: BuildReportPayload},
		{name: "request", build: func(report []byte) []byte { return BuildReportRequest("report-1", report) }, id: `"report-1"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := tc.build(report)
			var envelope struct {
				JSONRPC string          `json:"jsonrpc"`
				Method  string          `json:"method"`
				ID      json.RawMessage `json:"id"`
				Params  struct {
					Report struct {
						Network struct {
							TotalUp   uint64 `json:"totalUp"`
							TotalDown uint64 `json:"totalDown"`
						} `json:"network"`
					} `json:"report"`
				} `json:"params"`
			}
			if err := json.Unmarshal(payload, &envelope); err != nil {
				t.Fatalf("decode report envelope: %v", err)
			}
			if envelope.JSONRPC != Version || envelope.Method != MethodAgentReport || string(envelope.ID) != tc.id {
				t.Fatalf("unexpected envelope: %s", payload)
			}
			if got := envelope.Params.Report.Network.TotalUp; got != 9007199254740993 {
				t.Fatalf("large counter changed: %d", got)
			}
			if got := envelope.Params.Report.Network.TotalDown; got != ^uint64(0) {
				t.Fatalf("maximum uint64 counter changed: %d", got)
			}
			if bytes.Contains(payload, []byte("ack_event_ids")) {
				t.Fatalf("report unexpectedly acknowledges remote events: %s", payload)
			}
		})
	}
}

func TestBasicInfoEnvelopeIsNotification(t *testing.T) {
	payload := BuildBasicInfoPayload(map[string]any{"cpu_cores": 8, "kernel_version": "6.8"})
	var envelope struct {
		JSONRPC string          `json:"jsonrpc"`
		Method  string          `json:"method"`
		ID      json.RawMessage `json:"id"`
		Params  struct {
			Info struct {
				CPUCores      int    `json:"cpu_cores"`
				KernelVersion string `json:"kernel_version"`
			} `json:"info"`
		} `json:"params"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatalf("decode basic-info envelope: %v", err)
	}
	if envelope.JSONRPC != Version || envelope.Method != MethodAgentBasicInfo || len(envelope.ID) != 0 {
		t.Fatalf("unexpected basic-info envelope: %s", payload)
	}
	if envelope.Params.Info.CPUCores != 8 || envelope.Params.Info.KernelVersion != "6.8" {
		t.Fatalf("basic-info fields changed: %s", payload)
	}
}
