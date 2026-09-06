package monitoring

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/komari-monitor/komari-agent/monitoring/netstatic"
)

func TestConnectionsCount(t *testing.T) {
	tcpCount, udpCount, err := ConnectionsCount()
	if err != nil {
		t.Fatalf("ConnectionsCount failed: %v", err)
	}

	if tcpCount < 0 {
		t.Errorf("Expected non-negative TCP count, got %d", tcpCount)
	}

	if udpCount < 0 {
		t.Errorf("Expected non-negative UDP count, got %d", udpCount)
	}

	t.Logf("TCP connections: %d, UDP connections: %d", tcpCount, udpCount)
}

func TestParseNics(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected map[string]struct{}
	}{
		{
			name:     "empty string",
			input:    "",
			expected: nil,
		},
		{
			name:  "single nic",
			input: "eth0",
			expected: map[string]struct{}{
				"eth0": {},
			},
		},
		{
			name:  "multiple nics",
			input: "eth0,wlan0,enp0s3",
			expected: map[string]struct{}{
				"eth0":   {},
				"wlan0":  {},
				"enp0s3": {},
			},
		},
		{
			name:  "nics with spaces",
			input: " eth0 , wlan0 , enp0s3 ",
			expected: map[string]struct{}{
				"eth0":   {},
				"wlan0":  {},
				"enp0s3": {},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseNics(tt.input)

			if tt.expected == nil && result != nil {
				t.Errorf("Expected nil, got %v", result)
				return
			}

			if tt.expected != nil && result == nil {
				t.Errorf("Expected %v, got nil", tt.expected)
				return
			}

			if len(result) != len(tt.expected) {
				t.Errorf("Expected %d items, got %d", len(tt.expected), len(result))
				return
			}

			for key := range tt.expected {
				if _, exists := result[key]; !exists {
					t.Errorf("Expected key %s not found in result", key)
				}
			}
		})
	}
}

func TestShouldInclude(t *testing.T) {
	tests := []struct {
		name        string
		nicName     string
		includeNics map[string]struct{}
		excludeNics map[string]struct{}
		expected    bool
	}{
		{
			name:        "loopback interface should be excluded",
			nicName:     "lo",
			includeNics: nil,
			excludeNics: nil,
			expected:    false,
		},
		{
			name:        "docker interface should be excluded",
			nicName:     "docker0",
			includeNics: nil,
			excludeNics: nil,
			expected:    false,
		},
		{
			name:        "normal interface with no filters",
			nicName:     "eth0",
			includeNics: nil,
			excludeNics: nil,
			expected:    true,
		},
		{
			name:    "interface in include list",
			nicName: "eth0",
			includeNics: map[string]struct{}{
				"eth0": {},
			},
			excludeNics: nil,
			expected:    true,
		},
		{
			name:    "interface not in include list",
			nicName: "wlan0",
			includeNics: map[string]struct{}{
				"eth0": {},
			},
			excludeNics: nil,
			expected:    false,
		},
		{
			name:        "interface in exclude list",
			nicName:     "eth0",
			includeNics: nil,
			excludeNics: map[string]struct{}{
				"eth0": {},
			},
			expected: false,
		},
		{
			name:        "interface not in exclude list",
			nicName:     "wlan0",
			includeNics: nil,
			excludeNics: map[string]struct{}{
				"eth0": {},
			},
			expected: true,
		},
		{
			name:    "loopback in include list should still be excluded",
			nicName: "lo",
			includeNics: map[string]struct{}{
				"lo": {},
			},
			excludeNics: nil,
			expected:    false,
		},
		{
			name:    "wildcard include",
			nicName: "eth0",
			includeNics: map[string]struct{}{
				"eth*": {},
			},
			excludeNics: nil,
			expected:    true,
		},
		{
			name:        "wildcard exclude",
			nicName:     "tun0",
			includeNics: nil,
			excludeNics: map[string]struct{}{
				"tun*": {},
			},
			expected: false,
		},
		{
			name:    "include takes precedence over exclude",
			nicName: "eth0",
			includeNics: map[string]struct{}{
				"eth*": {},
			},
			excludeNics: map[string]struct{}{
				"eth0": {},
			},
			expected: true,
		},
		{
			name:        "tap remains included by default",
			nicName:     "tap0",
			includeNics: nil,
			excludeNics: nil,
			expected:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := shouldInclude(tt.nicName, tt.includeNics, tt.excludeNics)
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestNetworkSpeedFallback(t *testing.T) {
	// 测试回退方法
	includeNics := map[string]struct{}{}
	excludeNics := map[string]struct{}{}

	totalUp, totalDown, upSpeed, downSpeed, err := getNetworkSpeedFallback(includeNics, excludeNics)
	if err != nil {
		t.Fatalf("getNetworkSpeedFallback failed: %v", err)
	}

	t.Logf("TotalUp: %d, TotalDown: %d, UpSpeed: %d/s, DownSpeed: %d/s",
		totalUp, totalDown, upSpeed, downSpeed)
}

func isolateNetworkState(t *testing.T) {
	t.Helper()
	originalFlags := *flags
	originalSaveFilePath := netstatic.SaveFilePath
	temporarySaveFilePath := t.TempDir() + "/net_static.json"

	// Point persistence at the temporary file before stopping any leaked worker.
	netstatic.SaveFilePath = temporarySaveFilePath
	if err := netstatic.Stop(); err != nil {
		t.Fatalf("stop leaked netstatic worker: %v", err)
	}
	if err := netstatic.Clear(); err != nil {
		t.Fatalf("clear netstatic state: %v", err)
	}
	resetNetworkSpeedSample()

	t.Cleanup(func() {
		// Cleanup runs before TempDir cleanup, so Stop can flush safely.
		if err := netstatic.Stop(); err != nil {
			t.Errorf("stop netstatic worker: %v", err)
		}
		_ = netstatic.Clear()
		netstatic.SaveFilePath = originalSaveFilePath
		*flags = originalFlags
		resetNetworkSpeedSample()
	})
}

func resetNetworkSpeedSample() {
	networkSpeedSample.Lock()
	networkSpeedSample.sampledAt = time.Time{}
	networkSpeedSample.filterKey = ""
	networkSpeedSample.counters = nil
	networkSpeedSample.Unlock()
}

func TestNetworkSpeedWithoutMonthRotate(t *testing.T) {
	isolateNetworkState(t)

	flags.MonthRotate = 0
	flags.IncludeNics = ""
	flags.ExcludeNics = ""

	totalUp, totalDown, upSpeed, downSpeed, err := NetworkSpeed()
	if err != nil {
		t.Fatalf("NetworkSpeed failed: %v", err)
	}

	t.Logf("Without MonthRotate - TotalUp: %d, TotalDown: %d, UpSpeed: %d/s, DownSpeed: %d/s",
		totalUp, totalDown, upSpeed, downSpeed)
}

func TestNetworkSpeedWithMonthRotate(t *testing.T) {
	isolateNetworkState(t)

	// 设置测试值 - 启用月重置
	flags.MonthRotate = 1
	flags.IncludeNics = ""
	flags.ExcludeNics = ""

	totalUp, totalDown, upSpeed, downSpeed, err := NetworkSpeed()

	// 如果vnstat不可用，可能会回退到原来的方法，这是正常的
	if err != nil {
		t.Fatalf("NetworkSpeed failed: %v", err)
	}

	t.Logf("With MonthRotate - TotalUp: %d, TotalDown: %d, UpSpeed: %d/s, DownSpeed: %d/s",
		totalUp, totalDown, upSpeed, downSpeed)
}

func TestNetworkSpeedWithNicFilters(t *testing.T) {
	// 保存原始值
	originalMonthRotate := flags.MonthRotate
	originalIncludeNics := flags.IncludeNics
	originalExcludeNics := flags.ExcludeNics

	// 恢复原始值
	defer func() {
		flags.MonthRotate = originalMonthRotate
		flags.IncludeNics = originalIncludeNics
		flags.ExcludeNics = originalExcludeNics
	}()

	// 测试排除回环接口
	flags.MonthRotate = 0
	flags.IncludeNics = ""
	flags.ExcludeNics = "lo,docker0"

	totalUp, totalDown, upSpeed, downSpeed, err := NetworkSpeed()
	if err != nil {
		t.Fatalf("NetworkSpeed with excludeNics failed: %v", err)
	}

	t.Logf("With excludeNics - TotalUp: %d, TotalDown: %d, UpSpeed: %d/s, DownSpeed: %d/s",
		totalUp, totalDown, upSpeed, downSpeed)
}

func TestUpdateNetworkSpeedSampleArithmetic(t *testing.T) {
	resetNetworkSpeedSample()
	t.Cleanup(resetNetworkSpeedSample)
	base := time.Unix(100, 0)
	updateNetworkSpeedSample := func(tx, rx uint64, now time.Time) (uint64, uint64) {
		return updateNetworkSpeedCounters(map[string]networkCounter{"eth0": {Tx: tx, Rx: rx}}, now, "")
	}

	up, down := updateNetworkSpeedSample(1_000, 2_000, base)
	if up != 0 || down != 0 {
		t.Fatalf("first sample = %d/%d, want zero", up, down)
	}

	up, down = updateNetworkSpeedSample(1_150, 2_300, base.Add(1500*time.Millisecond))
	if up != 100 || down != 200 {
		t.Fatalf("elapsed sample = %d/%d, want 100/200", up, down)
	}

	// A counter reset must not become a huge unsigned rate.
	up, down = updateNetworkSpeedSample(50, 75, base.Add(2500*time.Millisecond))
	if up != 0 || down != 0 {
		t.Fatalf("reset sample = %d/%d, want zero", up, down)
	}

	up, down = updateNetworkSpeedSample(150, 275, base.Add(4500*time.Millisecond))
	if up != 50 || down != 100 {
		t.Fatalf("post-reset sample = %d/%d, want 50/100", up, down)
	}
}

func TestUpdateNetworkSpeedSampleResetsWhenNICFilterChanges(t *testing.T) {
	resetNetworkSpeedSample()
	t.Cleanup(resetNetworkSpeedSample)
	base := time.Unix(200, 0)
	updateNetworkSpeedSampleForFilter := func(tx, rx uint64, now time.Time, filterKey string) (uint64, uint64) {
		return updateNetworkSpeedCounters(map[string]networkCounter{"eth0": {Tx: tx, Rx: rx}}, now, filterKey)
	}

	if up, down := updateNetworkSpeedSampleForFilter(100, 200, base, "include:eth*"); up != 0 || down != 0 {
		t.Fatalf("first filtered sample = %d/%d, want zero", up, down)
	}
	if up, down := updateNetworkSpeedSampleForFilter(200, 400, base.Add(time.Second), "include:eth*"); up != 100 || down != 200 {
		t.Fatalf("same filtered sample = %d/%d, want 100/200", up, down)
	}
	if up, down := updateNetworkSpeedSampleForFilter(900, 1_000, base.Add(2*time.Second), "include:wlan*"); up != 0 || down != 0 {
		t.Fatalf("changed filtered sample = %d/%d, want zero", up, down)
	}
}

func TestProcNetConnectionsCountFixture(t *testing.T) {
	root := t.TempDir()
	netDir := filepath.Join(root, "net")
	if err := os.MkdirAll(netDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const tcp = "sl  local_address rem_address st\n 0: 0100007F:0019 00000000:0000 0A\n 1: 0100007F:001A 00000000:0000 01\n"
	const udp = "sl  local_address rem_address st\n 0: 0100007F:0035 00000000:0000 07\n"
	if err := os.WriteFile(filepath.Join(netDir, "tcp"), []byte(tcp), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(netDir, "udp"), []byte(udp), 0o600); err != nil {
		t.Fatal(err)
	}

	tcpCount, udpCount, err := procNetConnectionsCount(root)
	if err != nil {
		t.Fatalf("procNetConnectionsCount() failed: %v", err)
	}
	if tcpCount != 2 || udpCount != 1 {
		t.Fatalf("fixture counts = %d/%d, want 2/1", tcpCount, udpCount)
	}
}

func TestProcNetConnectionsFallback(t *testing.T) {
	fallbackErr := errors.New("fallback unavailable")
	tcpCount, udpCount, err := connectionsCountWithProcFallback(t.TempDir(), func() (int, int, error) {
		return 7, 8, nil
	})
	if err != nil || tcpCount != 7 || udpCount != 8 {
		t.Fatalf("successful fallback = %d/%d, %v; want 7/8 nil", tcpCount, udpCount, err)
	}

	_, _, err = connectionsCountWithProcFallback(t.TempDir(), func() (int, int, error) {
		return 0, 0, fallbackErr
	})
	if err == nil || !errors.Is(err, fallbackErr) || !strings.Contains(err.Error(), "proc net fast path failed") {
		t.Fatalf("combined fallback error = %v", err)
	}
}

func TestProcNetConnectionsFallbacksForEmptyFile(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "net"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "net", "tcp"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	tcpCount, udpCount, err := connectionsCountWithProcFallback(root, func() (int, int, error) {
		return 7, 8, nil
	})
	if err != nil || tcpCount != 7 || udpCount != 8 {
		t.Fatalf("empty proc file fallback = %d/%d, %v; want 7/8 nil", tcpCount, udpCount, err)
	}
}

func TestUpdateNetworkSpeedCountersHandlesNICMembership(t *testing.T) {
	resetNetworkSpeedSample()
	t.Cleanup(resetNetworkSpeedSample)
	base := time.Unix(300, 0)

	first := map[string]networkCounter{
		"eth0":  {Tx: 100, Rx: 200},
		"wlan0": {Tx: 300, Rx: 400},
	}
	if up, down := updateNetworkSpeedCounters(first, base, "default"); up != 0 || down != 0 {
		t.Fatalf("first NIC sample = %d/%d, want zero", up, down)
	}

	second := map[string]networkCounter{
		"eth0":  {Tx: 200, Rx: 300},
		"wlan0": {Tx: 350, Rx: 500},
	}
	if up, down := updateNetworkSpeedCounters(second, base.Add(time.Second), "default"); up != 150 || down != 200 {
		t.Fatalf("steady NIC sample = %d/%d, want 150/200", up, down)
	}

	// eth1 appears with a pre-existing cumulative counter. It must be seeded,
	// not treated as traffic since the previous sample.
	third := map[string]networkCounter{
		"eth0": {Tx: 300, Rx: 400},
		"eth1": {Tx: 1_000_000, Rx: 2_000_000},
	}
	if up, down := updateNetworkSpeedCounters(third, base.Add(2*time.Second), "default"); up != 100 || down != 100 {
		t.Fatalf("NIC addition sample = %d/%d, want 100/100", up, down)
	}

	// eth0 disappears and eth1 advances. The removed NIC contributes nothing,
	// while eth1 uses its own established baseline.
	fourth := map[string]networkCounter{
		"eth1": {Tx: 1_000_100, Rx: 2_000_100},
	}
	if up, down := updateNetworkSpeedCounters(fourth, base.Add(3*time.Second), "default"); up != 100 || down != 100 {
		t.Fatalf("NIC removal sample = %d/%d, want 100/100", up, down)
	}
}
