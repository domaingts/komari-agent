package cmd

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"os/signal"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"time"

	pkg_flags "github.com/komari-monitor/komari-agent/cmd/flags"
	"github.com/komari-monitor/komari-agent/dnsresolver"
	"github.com/komari-monitor/komari-agent/monitoring/netstatic"
	monitoring "github.com/komari-monitor/komari-agent/monitoring/unit"
	"github.com/komari-monitor/komari-agent/server"
	"github.com/komari-monitor/komari-agent/update"
	"github.com/spf13/cobra"
)

var flags = pkg_flags.GlobalConfig

var RootCmd = &cobra.Command{
	Use:   "komari-agent",
	Short: "komari agent",
	Long:  `komari agent`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := loadConfig(); err != nil {
			return err
		}
		if err := validateConfig(); err != nil {
			return err
		}

		stopCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		go func() {
			<-stopCtx.Done()
			log.Printf("shutting down gracefully...")
			netstatic.Stop()
			os.Exit(0)
		}()

		if flags.MonthRotate != 0 {
			err := netstatic.StartOrContinue()
			if err != nil {
				log.Println("Failed to start netstatic monitoring:", err)
			}
			nics, err := monitoring.InterfaceList()
			if err != nil {
				log.Println("Failed to get interface list for netstatic:", err)
			}
			err = netstatic.SetNewConfig(netstatic.NetStaticConfig{
				Nics: nics,
			})
			if err != nil {
				log.Println("Failed to set netstatic config:", err)
			}
		}

		log.Println("Komari Agent", update.CurrentVersion)
		log.Println("Github Repo:", update.Repo)

		if flags.CustomDNS != "" {
			dnsresolver.SetCustomDNSServer(flags.CustomDNS)
			log.Printf("Using custom DNS server: %s", flags.CustomDNS)
		} else {
			log.Printf("Using system default DNS resolver")
		}

		if flags.AutoDiscoveryKey != "" {
			err := handleAutoDiscovery()
			if err != nil {
				return fmt.Errorf("auto-discovery failed: %w", err)
			}
		}

		diskList, err := monitoring.DiskList()
		if err != nil {
			log.Println("Failed to get disk list:", err)
		}
		log.Println("Monitoring Mountpoints:", diskList)
		interfaceList, err := monitoring.InterfaceList()
		if err != nil {
			log.Println("Failed to get interface list:", err)
		}
		log.Println("Monitoring Interfaces:", interfaceList)

		if flags.IgnoreUnsafeCert {
			http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
		}
		go server.DoUploadBasicInfoWorks()
		for {
			server.UpdateBasicInfo()
			server.EstablishWebSocketConnection()
		}
	},
}

func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Println(err)
		os.Exit(1)
	}
}

func init() {
	RootCmd.PersistentFlags().StringVarP(&flags.Token, "token", "t", "", "API token")
	RootCmd.PersistentFlags().StringVarP(&flags.Endpoint, "endpoint", "e", "", "API endpoint")
	RootCmd.PersistentFlags().StringVar(&flags.AutoDiscoveryKey, "auto-discovery", "", "Auto discovery key for the agent")
	RootCmd.PersistentFlags().Float64VarP(&flags.Interval, "interval", "i", 1.0, "Interval in seconds")
	RootCmd.PersistentFlags().BoolVarP(&flags.IgnoreUnsafeCert, "ignore-unsafe-cert", "u", false, "Ignore unsafe certificate errors")
	RootCmd.PersistentFlags().IntVarP(&flags.MaxRetries, "max-retries", "r", 3, "Maximum number of retries")
	RootCmd.PersistentFlags().IntVarP(&flags.ReconnectInterval, "reconnect-interval", "c", 5, "Reconnect interval in seconds")
	RootCmd.PersistentFlags().IntVar(&flags.InfoReportInterval, "info-report-interval", 5, "Interval in minutes for reporting basic info")
	RootCmd.PersistentFlags().StringVar(&flags.IncludeNics, "include-nics", "", "Comma-separated list of network interfaces to include")
	RootCmd.PersistentFlags().StringVar(&flags.ExcludeNics, "exclude-nics", "", "Comma-separated list of network interfaces to exclude")
	RootCmd.PersistentFlags().StringVar(&flags.IncludeMountpoints, "include-mountpoint", "", "Semicolon-separated list of mount points to include for disk statistics")
	RootCmd.PersistentFlags().IntVar(&flags.MonthRotate, "month-rotate", 0, "Month reset for network statistics (0 to disable)")
	RootCmd.PersistentFlags().StringVar(&flags.CFAccessClientID, "cf-access-client-id", "", "Cloudflare Access Client ID")
	RootCmd.PersistentFlags().StringVar(&flags.CFAccessClientSecret, "cf-access-client-secret", "", "Cloudflare Access Client Secret")
	RootCmd.PersistentFlags().BoolVar(&flags.MemoryIncludeCache, "memory-include-cache", false, "Include cache/buffer in memory usage")
	RootCmd.PersistentFlags().BoolVar(&flags.MemoryReportRawUsed, "memory-exclude-bcf", false, "Use \"raminfo.Used = v.Total - v.Free - v.Buffers - v.Cached\" calculation for memory usage")
	RootCmd.PersistentFlags().StringVar(&flags.CustomDNS, "custom-dns", "", "Custom DNS server to use (e.g. 8.8.8.8, 114.114.114.114). By default, the program uses the system DNS resolver.")
	RootCmd.PersistentFlags().BoolVar(&flags.EnableGPU, "gpu", false, "Enable detailed GPU monitoring (usage, memory, multi-GPU support)")
	RootCmd.PersistentFlags().StringVar(&flags.CustomIpv4, "custom-ipv4", "", "Custom IPv4 address to use")
	RootCmd.PersistentFlags().StringVar(&flags.CustomIpv6, "custom-ipv6", "", "Custom IPv6 address to use")
	RootCmd.PersistentFlags().BoolVar(&flags.GetIpAddrFromNic, "get-ip-addr-from-nic", false, "Get IP address from network interface")
	RootCmd.PersistentFlags().StringVar(&flags.ConfigFile, "config", "", "Path to the configuration file")
	RootCmd.PersistentFlags().IntVar(&flags.ProtocolVersion, "protocol-version", 2, "Report protocol version (1 or 2)")
	RootCmd.PersistentFlags().BoolVar(&flags.DisableCompression, "disable-compression", false, "Disable v2 gzip/permessage-deflate compression")
	RootCmd.PersistentFlags().StringVar(&flags.PreferIPVersion, "prefer-ip-version", "", "Prefer IP version for dashboard connections: 4 or 6")
	RootCmd.PersistentFlags().ParseErrorsWhitelist.UnknownFlags = true
}

func loadConfig() error {
	loadFromEnv()
	if flags.ConfigFile == "" {
		return nil
	}
	data, err := os.ReadFile(flags.ConfigFile)
	if err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}
	if err := json.Unmarshal(data, flags); err != nil {
		return fmt.Errorf("failed to parse config file: %w", err)
	}
	return nil
}

func validateConfig() error {
	if flags.ProtocolVersion == 0 {
		flags.ProtocolVersion = 2
	}
	if flags.ProtocolVersion != 1 && flags.ProtocolVersion != 2 {
		return fmt.Errorf("invalid --protocol-version value %d: expected 1 or 2", flags.ProtocolVersion)
	}
	if flags.PreferIPVersion != "" && flags.PreferIPVersion != "4" && flags.PreferIPVersion != "6" {
		return fmt.Errorf("invalid --prefer-ip-version value %q: expected 4 or 6", flags.PreferIPVersion)
	}
	if math.IsNaN(flags.Interval) || math.IsInf(flags.Interval, 0) || flags.Interval <= 0 || flags.Interval >= float64(math.MaxInt64)/float64(time.Second) {
		return fmt.Errorf("invalid --interval value: expected a positive, finite duration")
	}
	if flags.InfoReportInterval <= 0 || int64(flags.InfoReportInterval) > math.MaxInt64/int64(time.Minute) {
		return fmt.Errorf("invalid --info-report-interval value: expected a positive duration in minutes")
	}
	if flags.ReconnectInterval < 0 || int64(flags.ReconnectInterval) > math.MaxInt64/int64(time.Second) {
		return fmt.Errorf("invalid --reconnect-interval value: expected a nonnegative duration in seconds")
	}
	if flags.MaxRetries < 0 {
		return fmt.Errorf("invalid --max-retries value: expected a nonnegative integer")
	}
	return nil
}

func loadFromEnv() {
	val := reflect.ValueOf(flags).Elem()
	typ := val.Type()

	for i := 0; i < val.NumField(); i++ {
		field := val.Field(i)
		fieldType := typ.Field(i)

		envTag := fieldType.Tag.Get("env")
		if envTag == "" {
			continue
		}

		envValue := os.Getenv(envTag)
		if envValue == "" {
			continue
		}

		switch field.Kind() {
		case reflect.String:
			field.SetString(envValue)
		case reflect.Bool:
			if strings.ToLower(envValue) == "true" || envValue == "1" {
				field.SetBool(true)
			}
		case reflect.Int:
			if intVal, err := strconv.Atoi(envValue); err == nil {
				field.SetInt(int64(intVal))
			}
		case reflect.Float64:
			if floatVal, err := strconv.ParseFloat(envValue, 64); err == nil {
				field.SetFloat(floatVal)
			}
		}
	}
}
