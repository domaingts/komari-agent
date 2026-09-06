package cmd

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	pkg_flags "github.com/komari-monitor/komari-agent/cmd/flags"
	"github.com/spf13/pflag"
)

func resetConfigForTest(t *testing.T) {
	t.Helper()
	original := *flags
	t.Cleanup(func() { *flags = original })
	*flags = pkg_flags.Config{
		Interval: 1, MaxRetries: 3, ReconnectInterval: 5,
		InfoReportInterval: 5, ProtocolVersion: 2,
	}
	typ := reflect.TypeOf(*flags)
	for i := 0; i < typ.NumField(); i++ {
		if key := typ.Field(i).Tag.Get("env"); key != "" {
			t.Setenv(key, "")
		}
	}
	RootCmd.PersistentFlags().VisitAll(func(f *pflag.Flag) {
		changed := f.Changed
		t.Cleanup(func() { f.Changed = changed })
		f.Changed = false
	})
}

func TestConfigPrecedence(t *testing.T) {
	resetConfigForTest(t)
	if err := RootCmd.PersistentFlags().Parse([]string{
		"--endpoint=https://cli.invalid", "--interval=2", "--protocol-version=1",
		"--prefer-ip-version=4", "--ignore-unsafe-cert", "--disable-compression",
	}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_ENDPOINT", "https://env.invalid")
	t.Setenv("AGENT_INTERVAL", "4")
	t.Setenv("AGENT_PROTOCOL_VERSION", "2")
	t.Setenv("AGENT_PREFER_IP_VERSION", "6")
	t.Setenv("AGENT_IGNORE_UNSAFE_CERT", "false")
	t.Setenv("AGENT_DISABLE_COMPRESSION", "0")
	t.Setenv("HOST_PROC", "/host/proc")
	if err := loadConfig(); err != nil {
		t.Fatal(err)
	}
	if flags.Endpoint != "https://env.invalid" || flags.Interval != 4 || flags.ProtocolVersion != 2 || flags.PreferIPVersion != "6" || flags.HostProc != "/host/proc" {
		t.Fatalf("environment did not override CLI configuration: %+v", *flags)
	}
	if !flags.IgnoreUnsafeCert || !flags.DisableCompression {
		t.Fatal("false environment values must retain the existing boolean behavior")
	}

	path := filepath.Join(t.TempDir(), "config.json")
	data := []byte(`{"endpoint":"https://json.invalid","interval":1.5,"protocol_version":1,"prefer_ip_version":"4","ignore_unsafe_cert":false,"disable_compression":false,"cf_access_client_id":"json-id","cf_access_client_secret":"json-secret"}`)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_CONFIG_FILE", path)
	t.Setenv("AGENT_CF_ACCESS_CLIENT_ID", "env-id")
	t.Setenv("AGENT_CF_ACCESS_CLIENT_SECRET", "env-secret")
	if err := loadConfig(); err != nil {
		t.Fatal(err)
	}
	if flags.Endpoint != "https://json.invalid" || flags.Interval != 1.5 || flags.ProtocolVersion != 1 || flags.PreferIPVersion != "4" || flags.IgnoreUnsafeCert || flags.DisableCompression {
		t.Fatalf("JSON did not override environment configuration: %+v", *flags)
	}
	if flags.CFAccessClientID != "json-id" || flags.CFAccessClientSecret != "json-secret" {
		t.Fatal("Cloudflare configuration was not preserved")
	}
	if err := validateConfig(); err != nil {
		t.Fatal(err)
	}
}

func TestRemovedOptionsRemainInert(t *testing.T) {
	resetConfigForTest(t)
	removed := []string{"disable-auto-update", "disable-web-ssh", "show-warning", "memory-mode-available", "autoUpdate"}
	for _, name := range removed {
		if RootCmd.PersistentFlags().Lookup(name) != nil {
			t.Errorf("removed flag --%s has been restored", name)
		}
	}
	if !RootCmd.PersistentFlags().ParseErrorsWhitelist.UnknownFlags {
		t.Fatal("unknown-flag compatibility must be retained")
	}
	if err := RootCmd.PersistentFlags().Parse([]string{"--disable-auto-update=true", "--disable-web-ssh=false", "--show-warning", "--memory-mode-available", "--autoUpdate=true", "--token=kept"}); err != nil {
		t.Fatal(err)
	}
	if flags.Token != "kept" || flags.MemoryIncludeCache || flags.MemoryReportRawUsed {
		t.Fatal("removed flags affected retained options")
	}
	before := *flags
	if err := json.Unmarshal([]byte(`{"disable_auto_update":false,"disable_web_ssh":false,"show_warning":true,"memory_mode_available":true}`), flags); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_DISABLE_AUTO_UPDATE", "false")
	t.Setenv("AGENT_DISABLE_WEB_SSH", "false")
	t.Setenv("AGENT_SHOW_WARNING", "true")
	t.Setenv("AGENT_MEMORY_MODE_AVAILABLE", "true")
	loadFromEnv()
	if *flags != before {
		t.Fatal("removed JSON/environment options changed configuration")
	}
	for _, field := range []string{"DisableAutoUpdate", "DisableWebSsh", "ShowWarning", "MemoryModeAvailable"} {
		if _, ok := reflect.TypeOf(*flags).FieldByName(field); ok {
			t.Errorf("removed configuration field %s has been restored", field)
		}
	}
}

func TestReportingFlagDefaults(t *testing.T) {
	for name, want := range map[string]string{
		"interval": "1", "protocol-version": "2", "disable-compression": "false", "prefer-ip-version": "",
		"cf-access-client-id": "", "cf-access-client-secret": "", "month-rotate": "0",
	} {
		flag := RootCmd.PersistentFlags().Lookup(name)
		if flag == nil || flag.DefValue != want {
			t.Errorf("flag %s: got %v, want default %q", name, flag, want)
		}
	}
}

func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name string
		set  func()
		want string
	}{
		{"protocol", func() { flags.ProtocolVersion = 3 }, "protocol-version"},
		{"preference", func() { flags.PreferIPVersion = "ipv6" }, "prefer-ip-version"},
		{"zero interval", func() { flags.Interval = 0 }, "interval"},
		{"negative interval", func() { flags.Interval = -1 }, "interval"},
		{"NaN interval", func() { flags.Interval = math.NaN() }, "interval"},
		{"infinite interval", func() { flags.Interval = math.Inf(1) }, "interval"},
		{"overflow interval", func() { flags.Interval = 1e20 }, "interval"},
		{"info interval", func() { flags.InfoReportInterval = 0 }, "info-report-interval"},
		{"reconnect interval", func() { flags.ReconnectInterval = -1 }, "reconnect-interval"},
		{"retries", func() { flags.MaxRetries = -1 }, "max-retries"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetConfigForTest(t)
			tt.set()
			if err := validateConfig(); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got error %v, want %s", err, tt.want)
			}
		})
	}
	t.Run("zero protocol selects v2", func(t *testing.T) {
		resetConfigForTest(t)
		flags.ProtocolVersion = 0
		flags.Interval = 0.5
		if err := validateConfig(); err != nil || flags.ProtocolVersion != 2 {
			t.Fatalf("zero protocol should select v2: protocol=%d, err=%v", flags.ProtocolVersion, err)
		}
	})
}

func TestIPPreferenceValidationSources(t *testing.T) {
	for _, source := range []string{"CLI", "environment", "JSON"} {
		t.Run(source, func(t *testing.T) {
			resetConfigForTest(t)
			switch source {
			case "CLI":
				if err := RootCmd.PersistentFlags().Parse([]string{"--prefer-ip-version=5"}); err != nil {
					t.Fatal(err)
				}
			case "environment":
				t.Setenv("AGENT_PREFER_IP_VERSION", "5")
			case "JSON":
				flags.ConfigFile = filepath.Join(t.TempDir(), "config.json")
				if err := os.WriteFile(flags.ConfigFile, []byte(`{"prefer_ip_version":"5"}`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := loadConfig(); err != nil {
				t.Fatal(err)
			}
			if err := validateConfig(); err == nil || !strings.Contains(err.Error(), "prefer-ip-version") {
				t.Fatalf("invalid preference from %s was accepted: %v", source, err)
			}
		})
	}
}

func TestLoadConfigErrors(t *testing.T) {
	resetConfigForTest(t)
	flags.ConfigFile = filepath.Join(t.TempDir(), "missing.json")
	if err := loadConfig(); err == nil || !strings.Contains(err.Error(), "read config") {
		t.Fatalf("missing configuration: %v", err)
	}
	if err := os.WriteFile(flags.ConfigFile, []byte(`{`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := loadConfig(); err == nil || !strings.Contains(err.Error(), "parse config") {
		t.Fatalf("invalid configuration JSON: %v", err)
	}
}
