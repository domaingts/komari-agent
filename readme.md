# komari-agent

`komari-agent` is a Go monitoring agent for Komari. It collects host metrics and basic machine information, then reports them to a Komari server over HTTP and WebSocket.

This reporting-only fork incorporates upstream **1.2.60** while retaining its custom behavior. Remote command execution, remote terminals, server-directed ping tasks, and self-updating are not included. WebSocket heartbeat pings are still used to maintain reporting connections. Cloudflare Access support, the one-second default interval, and the fork's existing default NIC exclusions are retained.

## What it reports

- CPU usage and CPU model
- RAM and swap usage
- Load average
- Disk usage
- Network throughput and total traffic
- TCP/UDP connection counts
- Uptime and process count
- OS, kernel, virtualization, IP addresses
- Optional detailed GPU information

## Build

Go 1.27 or newer is required. Release artifacts remain Linux/amd64 v1, v2, and v3 tarballs containing the `agent` binary; the upstream baseline is independent of the fork's release version.

```bash
go build ./...
```

## Run

Minimal example:

```bash
go run . --endpoint https://example.com --token YOUR_TOKEN
```

Common example:

```bash
go run . \
  --endpoint https://example.com \
  --token YOUR_TOKEN \
  --interval 2 \
  --info-report-interval 10 \
  --gpu
```

## Configuration methods

The agent supports three configuration sources:

1. command-line flags
2. environment variables
3. JSON config file

Precedence is **defaults → parsed command-line flags → environment variables → JSON config**. Environment variables can override explicitly supplied flags, and a JSON config file can then override both. For compatibility, boolean environment values only set a value to true (`true` or `1`); use JSON to explicitly override a true value with false. Unknown legacy flags remain tolerated, but removed remote-control/update options have no effect.

## Important flags

- `--endpoint`, `-e`: Komari server endpoint
- `--token`, `-t`: API token
- `--config`: path to a JSON config file
- `--interval`, `-i`: metric reporting interval in seconds (default 1; effective minimum 1 second)
- `--protocol-version`: reporting protocol, `1` or `2` (default 2)
- `--disable-compression`: disable v2 gzip and WebSocket permessage-deflate compression
- `--prefer-ip-version`: prefer `4` or `6` for panel connections, retaining the other family as fallback
- `--info-report-interval`: basic info upload interval in minutes
- `--max-retries`, `-r`: WebSocket reconnect retry limit
- `--reconnect-interval`, `-c`: reconnect interval in seconds
- `--ignore-unsafe-cert`, `-u`: skip TLS certificate verification
- `--custom-dns`: use a custom DNS server for agent HTTP/WebSocket traffic
- `--gpu`: enable detailed GPU reporting
- `--auto-discovery`: register with the server using an auto-discovery key
- `--include-nics`: comma-separated interface allowlist, supporting glob patterns such as `eth*`
- `--exclude-nics`: comma-separated interface denylist, supporting glob patterns such as `veth*`
- `--include-mountpoint`: semicolon-separated mountpoint allowlist
- `--month-rotate`: reset day for monthly traffic accounting
- `--memory-include-cache`: report memory usage including cache/buffer
- `--memory-exclude-bcf`: use the raw memory calculation mode
- `--cf-access-client-id`: Cloudflare Access client ID
- `--cf-access-client-secret`: Cloudflare Access client secret
- `--custom-ipv4`: override detected IPv4 address
- `--custom-ipv6`: override detected IPv6 address
- `--get-ip-addr-from-nic`: derive IP addresses from interfaces

## Environment variables

The same settings can be supplied through environment variables.

Common examples:

```bash
export AGENT_ENDPOINT=https://example.com
export AGENT_TOKEN=YOUR_TOKEN
export AGENT_INTERVAL=2
export AGENT_INFO_REPORT_INTERVAL=10
export AGENT_ENABLE_GPU=true

go run .
```

Useful environment variables include:

- `AGENT_ENDPOINT`
- `AGENT_TOKEN`
- `AGENT_AUTO_DISCOVERY_KEY`
- `AGENT_INTERVAL`
- `AGENT_IGNORE_UNSAFE_CERT`
- `AGENT_MAX_RETRIES`
- `AGENT_RECONNECT_INTERVAL`
- `AGENT_INFO_REPORT_INTERVAL`
- `AGENT_INCLUDE_NICS`
- `AGENT_EXCLUDE_NICS`
- `AGENT_INCLUDE_MOUNTPOINTS`
- `AGENT_MONTH_ROTATE`
- `AGENT_CF_ACCESS_CLIENT_ID`
- `AGENT_CF_ACCESS_CLIENT_SECRET`
- `AGENT_MEMORY_INCLUDE_CACHE`
- `AGENT_MEMORY_REPORT_RAW_USED`
- `AGENT_CUSTOM_DNS`
- `AGENT_ENABLE_GPU`
- `AGENT_CUSTOM_IPV4`
- `AGENT_CUSTOM_IPV6`
- `AGENT_GET_IP_ADDR_FROM_NIC`
- `AGENT_CONFIG_FILE`
- `AGENT_PROTOCOL_VERSION`
- `AGENT_DISABLE_COMPRESSION`
- `AGENT_PREFER_IP_VERSION`
- `HOST_PROC`: alternate host `/proc` mount, also configurable as JSON `host_proc`

See `cmd/flags/flag.go` for the complete mapping.

## JSON config file

You can provide a JSON config file with `--config`.

Example:

```json
{
  "endpoint": "https://example.com",
  "token": "YOUR_TOKEN",
  "interval": 2,
  "info_report_interval": 10,
  "enable_gpu": true,
  "custom_dns": "1.1.1.1",
  "include_nics": "eth0,enp1s0",
  "include_mountpoints": "/;/data"
}
```

Run with:

```bash
go run . --config /path/to/config.json
```

## Reporting protocol and compatibility

By default the agent uses JSON-RPC v2 at `/api/clients/v2/rpc` for `agent.report` and `agent.basicInfo`. It negotiates WebSocket compression and uses gzip for v2 HTTP requests unless `--disable-compression` is set. If the v2 WebSocket is unavailable, reporting can continue over HTTP POST while reconnection is attempted.

Three consecutive classified protocol failures trigger fallback to the legacy v1 endpoints. Ordinary network errors do not count as protocol failures, and a successful protocol exchange clears the failure count. Use `--protocol-version 1` to explicitly connect using the legacy protocol. Both Cloudflare Access headers are sent on panel requests when both credentials are configured, including registration, v1/v2 handshakes, and HTTP fallback.

CPU and network usage sampling is nonblocking. Network rates reflect elapsed time between samples; the initial rate sample is zero. Basic information includes both logical and physical CPU core counts. NIC globs retain existing precedence: default exclusions first, then explicit inclusion before explicit exclusion. Unlike upstream 1.2.60, this fork does not add `tap`, `fwbr`, and `fwpr` to its default exclusions; configure them explicitly if needed.

## Auto-discovery

If your Komari deployment supports auto-discovery, you can register the agent with a discovery key instead of providing a fixed token.

```bash
go run . --endpoint https://example.com --auto-discovery YOUR_KEY
```

The returned identity is cached in `auto-discovery.json` next to the agent executable.

## Helper commands

### List monitored disks

```bash
go run . list-disk
```

This prints all detected partitions and the mountpoints the agent would monitor.

### Check memory accounting

```bash
go run . check-mem
```

This compares the agent's memory calculation with several system views to help debug memory-reporting differences.

## Notes

- Version metadata is embedded at release time through GoReleaser.
- When `--custom-dns` is not set, the agent uses the system DNS resolver.
- GPU reporting is optional and depends on the host platform and available GPU tooling.
