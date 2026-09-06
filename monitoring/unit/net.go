package monitoring

import (
	"bufio"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/komari-monitor/komari-agent/monitoring/netstatic"
	"github.com/komari-monitor/komari-agent/utils"
	"github.com/shirou/gopsutil/v4/net"
)

func ConnectionsCount() (tcpCount, udpCount int, err error) {
	if runtime.GOOS == "linux" {
		return connectionsCountWithProcFallback(procRoot(), gopsutilConnectionsCount)
	}

	return gopsutilConnectionsCount()
}

func connectionsCountWithProcFallback(root string, fallback func() (int, int, error)) (tcpCount, udpCount int, err error) {
	var procErr error
	tcpCount, udpCount, procErr = procNetConnectionsCount(root)
	if procErr == nil {
		return tcpCount, udpCount, nil
	}

	tcpCount, udpCount, err = fallback()
	if err != nil && procErr != nil {
		return 0, 0, fmt.Errorf("proc net fast path failed: %w; gopsutil fallback failed: %w", procErr, err)
	}
	return tcpCount, udpCount, err
}

func gopsutilConnectionsCount() (tcpCount, udpCount int, err error) {
	tcps, err := net.Connections("tcp")
	if err != nil {
		return 0, 0, fmt.Errorf("failed to get TCP connections: %w", err)
	}
	udps, err := net.Connections("udp")
	if err != nil {
		return 0, 0, fmt.Errorf("failed to get UDP connections: %w", err)
	}

	return len(tcps), len(udps), nil
}

func procRoot() string {
	if flags.HostProc != "" {
		return flags.HostProc
	}
	return "/proc"
}

func procNetConnectionsCount(root string) (tcpCount, udpCount int, err error) {
	tcpCount, err = countProcNetFiles(root, "tcp", "tcp6")
	if err != nil {
		return 0, 0, err
	}
	udpCount, err = countProcNetFiles(root, "udp", "udp6")
	if err != nil {
		return 0, 0, err
	}
	return tcpCount, udpCount, nil
}

func countProcNetFiles(root string, names ...string) (int, error) {
	total := 0
	readAny := false
	for _, name := range names {
		count, err := countProcNetFile(filepath.Join(root, "net", name))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return 0, err
		}
		total += count
		readAny = true
	}
	if !readAny {
		return 0, fmt.Errorf("no proc net files found under %s", filepath.Join(root, "net"))
	}
	return total, nil
}

func countProcNetFile(path string) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	count := 0
	scanner := bufio.NewScanner(file)
	lineNumber := 0
	for scanner.Scan() {
		line := scanner.Text()
		if lineNumber == 0 {
			fields := strings.Fields(line)
			if len(fields) < 4 || fields[0] != "sl" || fields[1] != "local_address" ||
				(fields[2] != "rem_address" && fields[2] != "remote_address") || fields[3] != "st" {
				return 0, fmt.Errorf("invalid proc net header in %s", path)
			}
			lineNumber++
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		if len(strings.Fields(line)) < 4 {
			return 0, fmt.Errorf("invalid proc net entry in %s", path)
		}
		count++
		lineNumber++
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	if lineNumber == 0 {
		return 0, fmt.Errorf("empty proc net file %s", path)
	}
	return count, nil
}

var (
	// 预定义常见的回环和虚拟接口名称
	loopbackNames = map[string]struct{}{
		"br":      {},
		"cni":     {},
		"docker":  {},
		"podman":  {},
		"flannel": {},
		"lo":      {},
		"veth":    {}, // Docker
		"virbr":   {}, // KVM
		"vmbr":    {}, // Proxmox
	}
)

// VnstatInterface represents a network interface in vnstat output
type VnstatInterface struct {
	Name    string        `json:"name"`
	Alias   string        `json:"alias"`
	Created VnstatDate    `json:"created"`
	Updated VnstatUpdated `json:"updated"`
	Traffic VnstatTraffic `json:"traffic"`
}

// VnstatDate represents date information
type VnstatDate struct {
	Date      VnstatDateInfo `json:"date"`
	Timestamp int64          `json:"timestamp"`
}

// VnstatUpdated represents updated information
type VnstatUpdated struct {
	Date      VnstatDateInfo `json:"date"`
	Time      VnstatTimeInfo `json:"time"`
	Timestamp int64          `json:"timestamp"`
}

// VnstatDateInfo represents date components
type VnstatDateInfo struct {
	Year  int `json:"year"`
	Month int `json:"month"`
	Day   int `json:"day"`
}

// VnstatTimeInfo represents time components
type VnstatTimeInfo struct {
	Hour   int `json:"hour"`
	Minute int `json:"minute"`
}

// VnstatTraffic represents traffic data from vnstat output
type VnstatTraffic struct {
	Total      VnstatTotal        `json:"total"`
	FiveMinute []VnstatTimeEntry  `json:"fiveminute"`
	Hour       []VnstatTimeEntry  `json:"hour"`
	Day        []VnstatTimeEntry  `json:"day"`
	Month      []VnstatMonthEntry `json:"month"`
	Year       []VnstatYearEntry  `json:"year"`
	Top        []VnstatTimeEntry  `json:"top"`
}

// VnstatTotal represents total traffic data
type VnstatTotal struct {
	Rx uint64 `json:"rx"`
	Tx uint64 `json:"tx"`
}

// VnstatTimeEntry represents a time-based traffic entry
type VnstatTimeEntry struct {
	ID        int            `json:"id"`
	Date      VnstatDateInfo `json:"date"`
	Time      VnstatTimeInfo `json:"time"`
	Timestamp int64          `json:"timestamp"`
	Rx        uint64         `json:"rx"`
	Tx        uint64         `json:"tx"`
}

// VnstatMonthEntry represents a monthly traffic entry
type VnstatMonthEntry struct {
	ID        int            `json:"id"`
	Date      VnstatDateInfo `json:"date"`
	Timestamp int64          `json:"timestamp"`
	Rx        uint64         `json:"rx"`
	Tx        uint64         `json:"tx"`
}

// VnstatYearEntry represents a yearly traffic entry
type VnstatYearEntry struct {
	ID        int            `json:"id"`
	Date      VnstatDateInfo `json:"date"`
	Timestamp int64          `json:"timestamp"`
	Rx        uint64         `json:"rx"`
	Tx        uint64         `json:"tx"`
}

// VnstatOutput represents the complete vnstat JSON output
type VnstatOutput struct {
	VnstatVersion string            `json:"vnstatversion"`
	JsonVersion   string            `json:"jsonversion"`
	Interfaces    []VnstatInterface `json:"interfaces"`
}

func NetworkSpeed() (totalUp, totalDown, upSpeed, downSpeed uint64, err error) {
	includeNics := parseNics(flags.IncludeNics)
	excludeNics := parseNics(flags.ExcludeNics)

	// 如果设置了月重置（非0），统计totalUp、totalDown
	if flags.MonthRotate != 0 {
		netstatic.StartOrContinue() // 确保netstatic在运行
		now := uint64(time.Now().Unix())
		resetDay := uint64(utils.GetLastResetDate(flags.MonthRotate, time.Now()).Unix())
		nicStatics, err := netstatic.GetTotalTrafficBetween(resetDay, now)
		if err != nil {
			// 如果netstatic失败，回退到原来的方法，并返回额外的错误信息
			fallbackUp, fallbackDown, fallbackUpSpeed, fallbackDownSpeed, fallbackErr := getNetworkSpeedFallback(includeNics, excludeNics)
			if fallbackErr != nil {
				return fallbackUp, fallbackDown, fallbackUpSpeed, fallbackDownSpeed, fmt.Errorf("failed to call GetTotalTrafficBetween: %v; fallback error: %w", err, fallbackErr)
			}
			return fallbackUp, fallbackDown, fallbackUpSpeed, fallbackDownSpeed, fmt.Errorf("failed to call GetTotalTrafficBetween: %w", err)
		}

		for interfaceName, stats := range nicStatics {
			if shouldInclude(interfaceName, includeNics, excludeNics) {
				totalUp += stats.Tx
				totalDown += stats.Rx
			}
		}

		// 对于实时速度，仍然使用网卡累计计数器差值
		_, _, upSpeed, downSpeed, err = getNetworkSpeedFallback(includeNics, excludeNics)
		if err != nil {
			return totalUp, totalDown, 0, 0, err
		}

		return totalUp, totalDown, upSpeed, downSpeed, nil
	}

	// 如果没有设置月重置，使用原来的方法
	return getNetworkSpeedFallback(includeNics, excludeNics)
}

func getNetworkSpeedFallback(includeNics, excludeNics map[string]struct{}) (totalUp, totalDown, upSpeed, downSpeed uint64, err error) {
	counters, err := collectNetworkCounters(includeNics, excludeNics)
	if err != nil {
		return 0, 0, 0, 0, err
	}

	for _, counter := range counters {
		totalUp += counter.Tx
		totalDown += counter.Rx
	}
	upSpeed, downSpeed = updateNetworkSpeedCounters(counters, time.Now(), networkFilterKey(includeNics, excludeNics))
	return totalUp, totalDown, upSpeed, downSpeed, nil
}

type networkCounter struct {
	Tx uint64
	Rx uint64
}

func collectNetworkCounters(includeNics, excludeNics map[string]struct{}) (map[string]networkCounter, error) {
	ioCounters, err := net.IOCounters(true)
	if err != nil {
		return nil, fmt.Errorf("failed to get network IO counters: %w", err)
	}

	if len(ioCounters) == 0 {
		return nil, fmt.Errorf("no network interfaces found")
	}

	counters := make(map[string]networkCounter)
	for _, interfaceStats := range ioCounters {
		if shouldInclude(interfaceStats.Name, includeNics, excludeNics) {
			counters[interfaceStats.Name] = networkCounter{
				Tx: interfaceStats.BytesSent,
				Rx: interfaceStats.BytesRecv,
			}
		}
	}
	return counters, nil
}

type networkSpeedState struct {
	sync.Mutex
	sampledAt time.Time
	filterKey string
	counters  map[string]networkCounter
}

var networkSpeedSample networkSpeedState

func updateNetworkSpeedCounters(counters map[string]networkCounter, now time.Time, filterKey string) (upSpeed, downSpeed uint64) {
	networkSpeedSample.Lock()
	defer networkSpeedSample.Unlock()

	if networkSpeedSample.sampledAt.IsZero() || networkSpeedSample.filterKey != filterKey || networkSpeedSample.counters == nil {
		networkSpeedSample.counters = maps.Clone(counters)
		networkSpeedSample.sampledAt = now
		networkSpeedSample.filterKey = filterKey
		return 0, 0
	}

	elapsed := now.Sub(networkSpeedSample.sampledAt).Seconds()
	if elapsed <= 0 {
		return 0, 0
	}

	var upDelta, downDelta uint64
	for name, current := range counters {
		previous, ok := networkSpeedSample.counters[name]
		if !ok {
			// A newly discovered interface has no trustworthy previous sample.
			continue
		}
		upDelta += safeCounterDelta(current.Tx, previous.Tx)
		downDelta += safeCounterDelta(current.Rx, previous.Rx)
	}

	networkSpeedSample.counters = maps.Clone(counters)
	networkSpeedSample.sampledAt = now

	return uint64(float64(upDelta) / elapsed), uint64(float64(downDelta) / elapsed)
}

func safeCounterDelta(current, previous uint64) uint64 {
	if current >= previous {
		return current - previous
	}
	return 0
}

func networkFilterKey(includeNics, excludeNics map[string]struct{}) string {
	format := func(prefix string, values map[string]struct{}) string {
		keys := make([]string, 0, len(values))
		for key := range values {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		return prefix + strings.Join(keys, "\x00")
	}
	return format("include:", includeNics) + "\x01" + format("exclude:", excludeNics)
}

func parseNics(nics string) map[string]struct{} {
	if nics == "" {
		return nil
	}
	nicSet := make(map[string]struct{})
	for nic := range strings.SplitSeq(nics, ",") {
		nicSet[strings.TrimSpace(nic)] = struct{}{}
	}
	return nicSet
}

func shouldInclude(nicName string, includeNics, excludeNics map[string]struct{}) bool {
	// 默认排除回环接口
	for loopbackName := range loopbackNames {
		if strings.HasPrefix(nicName, loopbackName) {
			return false
		}
	}

	// 如果定义了白名单，则只包括白名单中的接口
	if len(includeNics) > 0 {
		for pattern := range includeNics {
			if matched, _ := filepath.Match(pattern, nicName); matched {
				return true
			}
		}
		return false
	}

	// 如果定义了黑名单，则排除黑名单中的接口
	for pattern := range excludeNics {
		if matched, _ := filepath.Match(pattern, nicName); matched {
			return false
		}
	}

	return true
}

func InterfaceList() ([]string, error) {
	includeNics := parseNics(flags.IncludeNics)
	excludeNics := parseNics(flags.ExcludeNics)
	interfaces := []string{}

	ioCounters, err := net.IOCounters(true)
	if err != nil {
		return nil, err
	}
	for _, interfaceStats := range ioCounters {
		if shouldInclude(interfaceStats.Name, includeNics, excludeNics) {
			interfaces = append(interfaces, interfaceStats.Name)
		}
	}
	return interfaces, nil
}
