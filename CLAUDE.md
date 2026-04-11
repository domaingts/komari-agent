# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Common commands

- Build all packages:
  - `go build ./...`
- Run all tests:
  - `go test ./...`
- Run a single package's tests:
  - `go test ./server`
  - `go test ./monitoring/unit`
- Run a single test by name:
  - `go test ./monitoring/unit -run TestNetworkSpeedWithNicFilters`
  - `go test ./update -run TestRepoDefault`
- Show CLI surface:
  - `go run . --help`
  - `go run . list-disk`
  - `go run . check-mem`
- Run the agent locally:
  - `go run . --endpoint https://example.com --token YOUR_TOKEN`
- Tidy module metadata after dependency changes:
  - `go mod tidy`

## Repository-specific notes

- There is no dedicated lint configuration in this repo. The routine validation path is `go test ./...`, `go build ./...`, and checking `go run . --help` when flags or commands change.
- Release builds inject `update.CurrentVersion` via GoReleaser (`.goreleaser.yaml`), so keep `update/update.go` small and stable.
- `install.sh` currently appears to assume release artifact names that may not match `.goreleaser.yaml`; treat it carefully if touching release/install behavior.

## High-level architecture

### Startup and configuration flow

- `main.go` just calls `cmd.Execute()`.
- `cmd/root.go` is the real entrypoint. It defines the Cobra root command, binds flags, loads env vars via reflection from `cmd/flags/flag.go`, optionally overlays a JSON config file, and then starts the long-running agent loop.
- Configuration precedence is:
  1. Cobra defaults
  2. environment variables via struct tags in `cmd/flags/flag.go`
  3. JSON config passed with `--config`
- `cmd/autodiscovery.go` is a side-path in startup: if `--auto-discovery` is set, it registers with the server and persists the returned token in `auto-discovery.json` next to the executable.

### Main runtime behavior

Once startup is complete in `cmd/root.go`, the agent does three things:

1. optionally starts net traffic accounting state in `monitoring/netstatic` when `--month-rotate` is enabled
2. periodically uploads basic host metadata through `server/basicInfo.go`
3. continuously reconnects and streams metric snapshots over WebSocket through `server/websocket.go`

That means changes to runtime behavior usually cross `cmd/root.go`, `server/basicInfo.go`, `server/websocket.go`, and `monitoring/monitoring.go`.

### Monitoring pipeline

- `monitoring/monitoring.go` assembles the outbound metrics payload.
- Most raw metric collection lives in `monitoring/unit/`.
- The `unit` package is intentionally OS-specific in places, with separate files for Linux, Darwin, Windows, and FreeBSD behavior.
- Some collectors are not “cheap”: CPU usage and network speed sampling intentionally wait about a second to compute rates, so avoid calling them repeatedly in hot code paths outside the normal reporting loop.

Key metric areas:
- CPU, memory, swap, load, uptime, process count
- disk filtering and aggregation
- NIC filtering and traffic counting
- optional GPU detail collection when `--gpu` is enabled

### Network and transport behavior

- `dnsresolver/resolver.go` is central to outbound networking. It provides the custom DNS server option, fallback resolver behavior, IPv4/IPv6 ordering, and the dialers/transports used by both HTTP and WebSocket clients.
- `server/basicInfo.go` uses the custom HTTP client from `dnsresolver` and uploads machine metadata to `/api/clients/uploadBasicInfo`.
- `server/websocket.go` uses a websocket dialer built on `dnsresolver.GetDialContext`, keeps a persistent connection to `/api/clients/report`, sends metric payloads on a ticker, and sends heartbeat pings every 30 seconds.
- Cloudflare Access headers are threaded through both HTTP and WebSocket request setup, so if auth-related connectivity changes are needed, inspect both server files and the shared resolver logic.

### Versioning and release assumptions

- `update/update.go` only holds build-time metadata now (`CurrentVersion`, `Repo`); there is no self-update flow in the current codebase.
- `.goreleaser.yaml` sets `update.CurrentVersion` with ldflags during release builds.
- `.github/workflows/release.yml` runs GoReleaser on published releases.

## Practical guidance for edits

- If you change flags, update both `cmd/root.go` and the config tag definitions in `cmd/flags/flag.go`, then verify with `go run . --help`.
- If you change reporting payload shape, inspect both:
  - `monitoring/monitoring.go` for periodic metrics
  - `server/basicInfo.go` for machine metadata
- If you change network behavior, review `dnsresolver/resolver.go` before adding ad hoc HTTP or WebSocket dialing logic elsewhere.
- If you change auto-discovery, remember it persists state beside the executable rather than in the working directory.
