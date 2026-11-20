// Package mcp is asrs-control's inbound Model Context Protocol adapter: a second driving adapter
// over the SAME application read use cases the REST adapter serves (ADR 0005), built on the
// official MCP Go SDK and served over Streamable HTTP only, stateless.
//
// The surface is READ-ONLY: every tool reads this context's own state (the AS/RS, its capacity,
// its storage jobs). Requesting, cancelling and confirming pickups stay on REST, where the
// Idempotency-Key, the aisle unit of work and the outbox live; governance_test.go fails the build
// on a write-verb tool name. The package depends on the application and domain layers only (never
// an outbound adapter, the REST adapter or cmd), and nothing but cmd/mcp imports it
// (TestMCPAdapterDependencyRule). There is no auth of any kind (fleet decision 2026-09-11): the
// in-cluster ClusterIP boundary is the access control. No rack position is exposed (D11).
package mcp

import (
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// serverName and serverVersion identify this server in the MCP handshake.
const (
	serverName    = "asrs-control-mcp"
	serverVersion = "1.0.0"
)

// instructions is what an MCP client is told about this server.
const instructions = "asrs-control (read-only): the WCS subsystem controller of the automated storage and retrieval " +
	"systems (AS/RS). list_asrs names the configured AS/RS; get_asrs_state reads one AS/RS's aisles (shuttle-and-lift " +
	"unit state, busy, open jobs, free positions), its ports (and the container presented at each output port) and the " +
	"recommended input port; get_asrs_capacity reads its capacity snapshot. get_storage_job reads one STORE or RETRIEVE " +
	"job (status QUEUED, DISPATCHED, ACKNOWLEDGED, CANCEL_REQUESTED, AWAITING_ARRIVAL, DONE, FAILED or CANCELLED, and the " +
	"failure reason); list_storage_jobs pages an AS/RS's jobs newest first, optionally by status. The storage job resource " +
	"(warehouse://asrs-control/asrs/{asrsId}/storage-jobs/{storageJobId}) loads one job without a tool call, and the " +
	"review_storage_job prompt hands you the operator SOP. Nothing here changes data: requesting, cancelling a job and " +
	"confirming a pickup are REST operations of asrs-control, called by material-flow-control."

// NewServer builds the MCP server with every tool, resource and prompt registered.
func NewServer(deps Deps) *mcp.Server {
	server := mcp.NewServer(
		&mcp.Implementation{Name: serverName, Version: serverVersion},
		&mcp.ServerOptions{Instructions: instructions},
	)
	deps.registerTools(server)
	deps.registerResources(server)
	deps.registerPrompts(server)
	return server
}

// Handler returns the Streamable HTTP handler. Stateless (Agentic Standard v2, AS-4): no
// Mcp-Session-Id is issued, so the 2026-07-28 revision's per-request handshake works and legacy
// initialize clients keep working; nothing relies on cross-call session state.
func Handler(server *mcp.Server) http.Handler {
	return mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Stateless: true},
	)
}
