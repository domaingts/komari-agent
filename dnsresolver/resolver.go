package dnsresolver

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	pkg_flags "github.com/komari-monitor/komari-agent/cmd/flags"
)

var flags = pkg_flags.GlobalConfig
var (
	DNSServers = []string{
		"[2606:4700:4700::1111]:53", // Cloudflare IPv6
		"[2606:4700:4700::1001]:53", // Cloudflare IPv6 备用
		"[2001:4860:4860::8888]:53", // Google IPv6
		"[2001:4860:4860::8844]:53", // Google IPv6 备用

		"114.114.114.114:53", // 114DNS，中国大陆
		"1.1.1.1:53",         // Cloudflare IPv4
		"8.8.8.8:53",         // Google IPv4
		"8.8.4.4:53",         // Google IPv4 备用
		"223.5.5.5:53",       // 阿里DNS，中国大陆
		"119.29.29.29:53",    // DNSPod，中国大陆
	}

	// CustomDNSServer 自定义DNS服务器，可以通过命令行参数设置
	CustomDNSServer string

	preferV4Once sync.Once
	hasIPv4      bool

	dnsServerMu  sync.RWMutex
	httpClientMu sync.Mutex
	httpClients  = make(map[httpClientKey]*http.Client)
)

type httpClientKey struct {
	timeout          time.Duration
	ignoreUnsafeCert bool
	preferIPVersion  string
	// The resolver is captured when a transport is built. Include the
	// configured server so a client cannot outlive a DNS configuration change.
	dnsServer string
}

// SetCustomDNSServer sets the DNS server used by newly-created resolvers.
// An empty value restores the system resolver. Existing pooled clients are
// retired because their transport captured the previous resolver.
func SetCustomDNSServer(dnsServer string) {
	normalized := normalizeDNSServer(dnsServer)

	dnsServerMu.Lock()
	previous := CustomDNSServer
	CustomDNSServer = normalized
	dnsServerMu.Unlock()

	if previous != normalized {
		invalidateHTTPClients()
	}
}

// normalizeDNSServer 将输入的 DNS 服务器字符串规范化为 host:port 形式：
// - IPv6 地址自动加方括号并补全端口 :53（若未提供）
// - IPv4/域名未提供端口时补全 :53
func normalizeDNSServer(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}

	// Bracketed IPv6 with no port is common in configuration files.
	if strings.HasPrefix(s, "[") {
		if end := strings.IndexByte(s, ']'); end >= 0 {
			if end == len(s)-1 {
				return net.JoinHostPort(s[1:end], "53")
			}
			if s[end+1] == ':' {
				if end+2 == len(s) {
					return s[:end+1] + ":53"
				}
				return s
			}
		}
	}

	// A parsed IP removes ambiguity between an IPv4 address and host:port,
	// and net.JoinHostPort supplies brackets for IPv6.
	if ip := net.ParseIP(s); ip != nil {
		return net.JoinHostPort(ip.String(), "53")
	}

	// An unbracketed string containing multiple colons is an IPv6 address.
	// IPv6 literals with a port must be supplied in bracketed form.
	if strings.Count(s, ":") >= 2 {
		return net.JoinHostPort(s, "53")
	}

	// Preserve an already supplied host:port pair. A blank port is treated as
	// omitted and receives the default DNS port.
	if strings.Count(s, ":") == 1 {
		if host, port, err := net.SplitHostPort(s); err == nil {
			if port == "" {
				return net.JoinHostPort(host, "53")
			}
			return s
		}
		// SplitHostPort rejects some non-numeric or malformed ports. Keep the
		// existing value rather than silently changing a caller's endpoint.
		return s
	}

	return net.JoinHostPort(s, "53")
}

// getCurrentDNSServer 获取当前要使用的DNS服务器
func getCurrentDNSServer() string {
	dnsServerMu.RLock()
	defer dnsServerMu.RUnlock()
	return CustomDNSServer
}

// GetCustomResolver 返回一个解析器：
// - 若设置了自定义 DNS：使用该服务器（并在失败时尝试内置列表作为兜底）。
// - 若未设置自定义 DNS：返回系统默认解析器（不使用内置列表）。
func GetCustomResolver() *net.Resolver {
	return resolverForDNSServer(getCurrentDNSServer())
}

func resolverForDNSServer(dnsServer string) *net.Resolver {
	// 未设置自定义 DNS，直接使用系统默认解析器
	if dnsServer == "" {
		return net.DefaultResolver
	}

	// Capture the configured server. A transport may remain pooled after the
	// setting changes, so its resolver must not silently start using a new one.
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{Timeout: 10 * time.Second}
			dnsNetwork := network
			if dnsNetwork == "" {
				dnsNetwork = "udp"
			}

			// 优先使用自定义 DNS 服务器
			if conn, err := d.DialContext(ctx, dnsNetwork, dnsServer); err == nil {
				return conn, nil
			}
			log.Printf("Custom DNS server %s is unreachable, trying fallback servers", dnsServer)
			// 如果自定义DNS不可用，则尝试内置列表作为兜底
			for _, server := range DNSServers {
				if server == dnsServer {
					continue
				}
				if conn, err := d.DialContext(ctx, dnsNetwork, server); err == nil {
					return conn, nil
				}
			}

			return nil, fmt.Errorf("no available DNS server")
		},
	}
}

// buildTransport 构建带有自定义解析/拨号策略的 HTTP 传输层，可注入 TLS 配置
func buildTransport(timeout time.Duration, tlsConfig *tls.Config) *http.Transport {
	return buildTransportWithPreference(timeout, tlsConfig, "")
}

func buildTransportWithPreference(timeout time.Duration, tlsConfig *tls.Config, preferIPVersion string) *http.Transport {
	return buildTransportWithResolver(timeout, tlsConfig, preferIPVersion, GetCustomResolver())
}

func buildTransportWithResolver(timeout time.Duration, tlsConfig *tls.Config, preferIPVersion string, customResolver *net.Resolver) *http.Transport {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := customResolver.LookupHost(ctx, host)
			if err != nil {
				return nil, err
			}
			sortIPsByPreference(ips, preferIPVersion)
			return dialResolvedIPs(ctx, network, port, ips, timeout)
		},
		MaxIdleConns:          32,
		MaxIdleConnsPerHost:   4,
		MaxConnsPerHost:       8,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		TLSClientConfig:       tlsConfig,
		ForceAttemptHTTP2:     true,
	}
}

// GetHTTPClient returns a pooled HTTP client using the configured resolver.
func GetHTTPClient(timeout time.Duration) *http.Client {
	return getHTTPClient(timeout, "")
}

// GetHTTPClientWithPreference returns a pooled client that uses the configured
// resolver and orders addresses according to the requested IP family.
// "4" and "6" pin the preferred family; an empty value uses automatic order.
func GetHTTPClientWithPreference(timeout time.Duration, preferIPVersion string) *http.Client {
	return getHTTPClient(timeout, normalizeIPVersionPreference(preferIPVersion))
}

func getHTTPClient(timeout time.Duration, preferIPVersion string) *http.Client {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	preferIPVersion = normalizeIPVersionPreference(preferIPVersion)

	httpClientMu.Lock()
	defer httpClientMu.Unlock()
	dnsServer := getCurrentDNSServer()
	key := httpClientKey{
		timeout:          timeout,
		ignoreUnsafeCert: flags.IgnoreUnsafeCert,
		preferIPVersion:  preferIPVersion,
		dnsServer:        dnsServer,
	}
	if client := httpClients[key]; client != nil {
		return client
	}
	client := &http.Client{
		Transport: buildTransportWithResolver(timeout, &tls.Config{
			InsecureSkipVerify: flags.IgnoreUnsafeCert,
		}, preferIPVersion, resolverForDNSServer(dnsServer)),
		Timeout: timeout,
	}
	httpClients[key] = client
	return client
}

func invalidateHTTPClients() {
	httpClientMu.Lock()
	clients := httpClients
	httpClients = make(map[httpClientKey]*http.Client)
	httpClientMu.Unlock()

	for _, client := range clients {
		if transport, ok := client.Transport.(interface{ CloseIdleConnections() }); ok {
			transport.CloseIdleConnections()
		}
	}
}

// GetNetDialer 返回一个使用自定义DNS解析器的网络拨号器
func GetNetDialer(timeout time.Duration) *net.Dialer {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	return &net.Dialer{
		Timeout:   timeout,
		KeepAlive: 30 * time.Second,
		Resolver:  GetCustomResolver(),
	}
}

// GetDialContext 返回一个自定义 DialContext：
// - 使用自定义解析器解析主机名
// - 根据本机网络自动选择 IPv4 或 IPv6 优先
// - 逐个 IP 进行连接尝试，直到成功或全部失败
func GetDialContext(timeout time.Duration) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return GetDialContextWithPreference(timeout, "")
}

// GetDialContextWithPreference returns a dial function with an optional IP
// family preference. "4" and "6" pin the preferred family; an empty value
// uses automatic order.
func GetDialContextWithPreference(timeout time.Duration, preferIPVersion string) func(ctx context.Context, network, addr string) (net.Conn, error) {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}

	resolver := GetCustomResolver()
	preferIPVersion = normalizeIPVersionPreference(preferIPVersion)

	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}

		// 为解析设置一个带超时的子 context，避免整体拨号过快超时
		lookupCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		ips, err := resolver.LookupHost(lookupCtx, host)
		if err != nil {
			return nil, err
		}

		sortIPsByPreference(ips, preferIPVersion)

		// 逐个 IP 尝试连接
		return dialResolvedIPs(ctx, network, port, ips, timeout)
	}
}

func dialResolvedIPs(ctx context.Context, network, port string, ips []string, timeout time.Duration) (net.Conn, error) {
	for _, ip := range ips {
		d := &net.Dialer{
			Timeout:   timeout,
			KeepAlive: 30 * time.Second,
			DualStack: true,
		}
		c, err := d.DialContext(ctx, network, net.JoinHostPort(ip, port))
		if err == nil {
			return c, nil
		}
	}
	return nil, fmt.Errorf("failed to dial to any of the resolved IPs")
}

func normalizeIPVersionPreference(preferIPVersion string) string {
	if preferIPVersion == "4" || preferIPVersion == "6" {
		return preferIPVersion
	}
	return ""
}

func sortIPsByPreference(ips []string, preferIPVersion string) {
	preferIPVersion = normalizeIPVersionPreference(preferIPVersion)
	if preferIPVersion == "" {
		// Select automatically based on whether the host has IPv4 connectivity.
		if preferIPv4First() {
			preferIPVersion = "4"
		} else {
			preferIPVersion = "6"
		}
	}

	preferred := make([]string, 0, len(ips))
	others := make([]string, 0, len(ips))
	for _, ip := range ips {
		parsedIP := net.ParseIP(ip)
		isIPv4 := parsedIP != nil && parsedIP.To4() != nil
		isIPv6 := parsedIP != nil && parsedIP.To4() == nil
		if (preferIPVersion == "4" && isIPv4) || (preferIPVersion == "6" && isIPv6) {
			preferred = append(preferred, ip)
		} else {
			others = append(others, ip)
		}
	}
	n := copy(ips, preferred)
	copy(ips[n:], others)
}

// preferIPv4First 检测本机是否存在可用的 IPv4 地址，若没有则在连接尝试中优先 IPv6
func preferIPv4First() bool {
	preferV4Once.Do(func() {
		ifaces, _ := net.Interfaces()
		for _, iface := range ifaces {
			if (iface.Flags&net.FlagUp) == 0 || (iface.Flags&net.FlagLoopback) != 0 {
				continue
			}
			addrs, _ := iface.Addrs()
			for _, a := range addrs {
				var ip net.IP
				switch v := a.(type) {
				case *net.IPNet:
					ip = v.IP
				case *net.IPAddr:
					ip = v.IP
				}
				if ip == nil || ip.IsLoopback() {
					continue
				}
				if ip.To4() != nil {
					hasIPv4 = true
					return
				}
			}
		}
		hasIPv4 = false
	})
	return hasIPv4
}
