// Package v1 contains the reporting protocol used by legacy panels.
package v1

// ReportPayload is the raw JSON payload produced by the monitoring package.
// Keeping it raw lets the agent preserve field names and integer precision while
// wrapping the same report in the v2 JSON-RPC envelope.
type ReportPayload = []byte
