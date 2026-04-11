# komari-agent

`komari-agent` is a Go monitoring agent for Komari. It collects host metrics and basic machine information, then reports them to a Komari server over HTTP and WebSocket.

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

Environment variables are loaded first, and a JSON config file can then override them when `--config` is provided.

## Important flags

- `--endpoint`, `-e`: Komari server endpoint
- `--token`, `-t`: API token
- `--config`: path to a JSON config file
- `--interval`, `-i`: metric reporting interval in seconds
- `--info-report-interval`: basic info upload interval in minutes
- `--max-retries`, `-r`: WebSocket reconnect retry limit
- `--reconnect-interval`, `-c`: reconnect interval in seconds
- `--ignore-unsafe-cert`, `-u`: skip TLS certificate verification
- `--custom-dns`: use a custom DNS server for agent HTTP/WebSocket traffic
- `--gpu`: enable detailed GPU reporting
- `--auto-discovery`: register with the server using an auto-discovery key
- `--include-nics`: comma-separated interface allowlist
- `--exclude-nics`: comma-separated interface denylist
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
