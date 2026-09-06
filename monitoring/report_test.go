package monitoring

import (
	"encoding/json"
	"math"
	"testing"

	unit "github.com/komari-monitor/komari-agent/monitoring/unit"
)

func TestBuildGPUReportEmpty(t *testing.T) {
	gpu, ok := buildGPUReport(nil)
	if ok {
		t.Fatal("empty GPU fixture unexpectedly produced a report")
	}
	if gpu.Count != 0 || gpu.AverageUsage != 0 || gpu.DetailedInfo != nil {
		t.Fatalf("empty GPU report = %#v, want zero value", gpu)
	}
}

func TestBuildGPUReport(t *testing.T) {
	gpu, ok := buildGPUReport([]unit.DetailedGPUInfo{
		{Name: "GPU A", MemoryTotal: 100, MemoryUsed: 40, Utilization: 20, Temperature: 50},
		{Name: "GPU B", MemoryTotal: 200, MemoryUsed: 80, Utilization: 60, Temperature: 55},
	})
	if !ok {
		t.Fatal("non-empty GPU fixture did not produce a report")
	}
	if gpu.Count != 2 || gpu.AverageUsage != 40 {
		t.Fatalf("GPU report summary = count %d average %v, want 2/40", gpu.Count, gpu.AverageUsage)
	}
	if len(gpu.DetailedInfo) != 2 || gpu.DetailedInfo[1].MemoryUsed != 80 {
		t.Fatalf("GPU report details = %#v", gpu.DetailedInfo)
	}
}

func TestReportJSONPayload(t *testing.T) {
	payload := report{
		CPU:         cpuReport{Usage: 12.5},
		Ram:         usageReport{Total: math.MaxUint64, Used: 42},
		Network:     networkReport{Up: 3, Down: 4, TotalUp: math.MaxUint64, TotalDown: 8},
		Connections: connectionsReport{TCP: 5, UDP: 6},
		Uptime:      7,
		Process:     8,
		Message:     "",
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal(report) failed: %v", err)
	}

	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatalf("json.Unmarshal(report) failed: %v", err)
	}
	for _, key := range []string{"cpu", "ram", "swap", "load", "disk", "network", "connections", "uptime", "process", "message"} {
		if _, ok := object[key]; !ok {
			t.Errorf("report payload missing %q: %s", key, raw)
		}
	}
	if _, ok := object["gpu"]; ok {
		t.Error("empty GPU field should be omitted")
	}

	var network networkReport
	if err := json.Unmarshal(object["network"], &network); err != nil {
		t.Fatal(err)
	}
	if network.TotalUp != math.MaxUint64 || network.TotalDown != 8 {
		t.Fatalf("network payload = %#v", network)
	}
}
