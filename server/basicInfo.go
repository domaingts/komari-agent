package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/komari-monitor/komari-agent/dnsresolver"
	monitoring "github.com/komari-monitor/komari-agent/monitoring/unit"
	"github.com/komari-monitor/komari-agent/protocol/transport"
	v2 "github.com/komari-monitor/komari-agent/protocol/v2"
	"github.com/komari-monitor/komari-agent/update"
	"github.com/komari-monitor/komari-agent/utils"

	pkg_flags "github.com/komari-monitor/komari-agent/cmd/flags"
)

var flags = pkg_flags.GlobalConfig

func DoUploadBasicInfoWorks() {
	ticker := time.NewTicker(time.Duration(flags.InfoReportInterval) * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		if err := uploadBasicInfo(); err != nil {
			log.Println("Error uploading basic info:", err)
		}
	}
}

func UpdateBasicInfo() {
	if err := uploadBasicInfo(); err != nil {
		log.Println("Error uploading basic info:", err)
	} else {
		log.Println("Basic info uploaded successfully")
	}
}

func uploadBasicInfo() error {
	cpu := monitoring.CpuStaticInfo()
	osname := monitoring.OSName()
	kernelVersion := monitoring.KernelVersion()
	ipv4, ipv6, _ := monitoring.GetIPAddress()

	data := map[string]any{
		"cpu_name":           cpu.CPUName,
		"cpu_cores":          cpu.CPUCores,
		"cpu_physical_cores": cpu.CPUPhysicalCores,
		"arch":               cpu.CPUArchitecture,
		"os":                 osname,
		"kernel_version":     kernelVersion,
		"ipv4":               ipv4,
		"ipv6":               ipv6,
		"mem_total":          monitoring.Ram().Total,
		"swap_total":         monitoring.Swap().Total,
		"disk_total":         monitoring.Disk().Total,
		"gpu_name":           monitoring.GpuName(),
		"virtualization":     monitoring.Virtualized(),
		"version":            update.CurrentVersion,
	}

	// First send the complete shape. The retry retains compatibility with old
	// panels which reject kernel_version and cpu_physical_cores. It only runs
	// when the panel actually refused the payload: retrying a DNS/dial/timeout
	// failure cannot succeed for a compatibility reason, and would let a
	// transient outage replace the metadata with the reduced legacy shape.
	err := tryUploadData(data)
	if err == nil || !panelRejectedPayload(err) {
		return err
	}
	delete(data, "kernel_version")
	delete(data, "cpu_physical_cores")
	return tryUploadData(data)
}

// panelRejectedPayload reports whether the panel answered and refused the
// request, as opposed to the request never reaching it.
func panelRejectedPayload(err error) bool {
	var statusErr *httpStatusError
	if errors.As(err, &statusErr) {
		// An old panel meeting an unknown field answers with a client error;
		// 5xx means the panel is failing rather than refusing the shape.
		return statusErr.StatusCode >= 400 && statusErr.StatusCode < 500
	}
	// A v2 panel refuses the shape through a JSON-RPC error response.
	var protocolErr *v2ProtocolError
	return errors.As(err, &protocolErr)
}

func tryUploadData(data map[string]any) error {
	protocolVersion := uploadProtocolVersion()
	if protocolVersion >= 2 {
		err := tryUploadDataWithProtocol(data, 2)
		if shouldFallbackToV1(2, err) {
			log.Printf("v2 basic info failed %d consecutive protocol attempts, falling back to v1", v2ProtocolFallbackThreshold)
			setConnectionProtocolVersion(1)
			return tryUploadDataWithProtocol(data, 1)
		}
		return err
	}
	return tryUploadDataWithProtocol(data, 1)
}

func tryUploadDataWithProtocol(data map[string]any, protocolVersion int) error {
	endpoint := buildPanelEndpoint("/api/clients/uploadBasicInfo?token=" + flags.Token)
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if protocolVersion >= 2 {
		endpoint = buildPanelEndpoint("/api/clients/v2/rpc?token=" + flags.Token)
		payload = v2.BuildBasicInfoPayload(data)
	}

	body := payload
	compressed := false
	if protocolVersion >= 2 && !flags.DisableCompression {
		body, err = transport.GzipBytes(payload)
		if err != nil {
			return err
		}
		compressed = true
	}

	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if compressed {
		req.Header.Set("Content-Encoding", "gzip")
	}
	utils.SetCloudflareAccessHeaders(req.Header, flags.CFAccessClientID, flags.CFAccessClientSecret)

	client := dnsresolver.GetHTTPClientWithPreference(30*time.Second, flags.PreferIPVersion)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return &httpStatusError{StatusCode: resp.StatusCode, Status: resp.Status, Body: string(respBody)}
	}
	if protocolVersion >= 2 && len(bytes.TrimSpace(respBody)) > 0 {
		if _, err := parseV2Response(respBody); err != nil {
			return err
		}
	}
	if protocolVersion >= 2 {
		resetV2ProtocolFailures(2)
	}
	return nil
}
