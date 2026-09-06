package dnsresolver

import (
	"context"
	"net"
	"net/http"
	"reflect"
	"testing"
	"time"

	pkg_flags "github.com/komari-monitor/komari-agent/cmd/flags"
)

func TestNormalizeDNSServer(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", ""},
		{" 1.2.3.4 ", "1.2.3.4:53"},
		{"dns.example", "dns.example:53"},
		{"dns.example:5353", "dns.example:5353"},
		{"[::1]", "[::1]:53"},
		{"[::1]:5353", "[::1]:5353"},
		{"2001:db8::1", "[2001:db8::1]:53"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := normalizeDNSServer(tt.input); got != tt.want {
				t.Fatalf("normalizeDNSServer(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestCustomResolverHonorsTCPNetwork(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("TCP loopback listener unavailable: %v", err)
	}
	defer listener.Close()

	oldDNS := getCurrentDNSServer()
	SetCustomDNSServer(listener.Addr().String())
	t.Cleanup(func() { SetCustomDNSServer(oldDNS) })

	accepted := make(chan net.Conn, 1)
	acceptErr := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			acceptErr <- err
			return
		}
		accepted <- conn
	}()

	conn, err := GetCustomResolver().Dial(context.Background(), "tcp", "ignored")
	if err != nil {
		t.Fatalf("custom resolver TCP dial: %v", err)
	}
	conn.Close()
	select {
	case acceptedConn := <-accepted:
		acceptedConn.Close()
	case err := <-acceptErr:
		t.Fatalf("accept custom resolver connection: %v", err)
	case <-time.After(time.Second):
		t.Fatal("custom resolver did not use TCP")
	}
}

func TestSortIPsByPreferencePartitionsFamilies(t *testing.T) {
	original := []string{"2001:db8::1", "192.0.2.1", "not-an-ip", "198.51.100.1", "2001:db8::2"}

	v4 := append([]string(nil), original...)
	sortIPsByPreference(v4, "4")
	wantV4 := []string{"192.0.2.1", "198.51.100.1", "2001:db8::1", "not-an-ip", "2001:db8::2"}
	if !reflect.DeepEqual(v4, wantV4) {
		t.Fatalf("IPv4 preference reordered %v, want %v", v4, wantV4)
	}

	v6 := append([]string(nil), original...)
	sortIPsByPreference(v6, "6")
	wantV6 := []string{"2001:db8::1", "2001:db8::2", "192.0.2.1", "not-an-ip", "198.51.100.1"}
	if !reflect.DeepEqual(v6, wantV6) {
		t.Fatalf("IPv6 preference reordered %v, want %v", v6, wantV6)
	}
}

func TestDialResolvedIPsFallsBackToNextAddress(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("loopback listener unavailable: %v", err)
	}
	defer listener.Close()

	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	conn, err := dialResolvedIPs(context.Background(), "tcp4", port, []string{"127.0.0.2", "127.0.0.1"}, time.Second)
	if err != nil {
		t.Fatalf("dialResolvedIPs() = %v", err)
	}
	conn.Close()

	accepted := make(chan struct{})
	go func() {
		if acceptedConn, acceptErr := listener.Accept(); acceptErr == nil {
			acceptedConn.Close()
			close(accepted)
		}
	}()
	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Fatal("listener did not receive fallback connection")
	}
}

func TestHTTPClientPoolPartitionsSettings(t *testing.T) {
	oldDNS := getCurrentDNSServer()
	oldUnsafe := pkg_flags.GlobalConfig.IgnoreUnsafeCert
	t.Cleanup(func() {
		pkg_flags.GlobalConfig.IgnoreUnsafeCert = oldUnsafe
		SetCustomDNSServer(oldDNS)
	})

	SetCustomDNSServer("")
	pkg_flags.GlobalConfig.IgnoreUnsafeCert = false

	defaultClient := GetHTTPClient(30 * time.Second)
	if got := GetHTTPClient(30 * time.Second); got != defaultClient {
		t.Fatal("identical HTTP client settings did not reuse pooled client")
	}
	if got := GetHTTPClientWithPreference(30*time.Second, "invalid"); got != defaultClient {
		t.Fatal("invalid IP preference did not normalize to automatic mode")
	}

	if got := GetHTTPClient(31 * time.Second); got == defaultClient {
		t.Fatal("different timeout reused pooled client")
	}
	if got := GetHTTPClientWithPreference(30*time.Second, "4"); got == defaultClient {
		t.Fatal("IPv4 preference was not represented in pool key")
	}
	if got := GetHTTPClientWithPreference(30*time.Second, "6"); got == defaultClient {
		t.Fatal("IPv6 preference was not represented in pool key")
	}

	pkg_flags.GlobalConfig.IgnoreUnsafeCert = true
	unsafeClient := GetHTTPClient(30 * time.Second)
	if unsafeClient == defaultClient {
		t.Fatal("TLS verification setting was not represented in pool key")
	}
	transport, ok := unsafeClient.Transport.(*http.Transport)
	if !ok || transport.TLSClientConfig == nil || !transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("pooled TLS configuration did not preserve IgnoreUnsafeCert")
	}

	SetCustomDNSServer("127.0.0.1")
	customClient := GetHTTPClient(30 * time.Second)
	if customClient == unsafeClient {
		t.Fatal("custom DNS setting reused a client with a captured system resolver")
	}
	if got := GetHTTPClient(30 * time.Second); got != customClient {
		t.Fatal("identical custom DNS setting did not reuse pooled client")
	}
	SetCustomDNSServer("127.0.0.2")
	if got := GetHTTPClient(30 * time.Second); got == customClient {
		t.Fatal("changed custom DNS setting reused stale pooled client")
	}
}
