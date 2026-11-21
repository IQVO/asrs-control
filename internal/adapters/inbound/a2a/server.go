// Package a2a is asrs-control's inbound Agent2Agent adapter: a third driving adapter over the
// same application read use cases as REST and MCP (ADR 0005), built on the official A2A Go SDK
// (a2aproject/a2a-go/v2), JSON-RPC 2.0 over HTTP, with the Agent Card at the well-known path.
//
// A2A is the DELEGATION channel between agents: a client sends a goal-shaped skill request and
// this agent answers from its own state through its own use cases. It is not a data-sync channel:
// facts still travel only as CloudEvents on Kafka through the outbox (ADR 0003). The agent is
// READ-ONLY like the MCP server: its two skills read a job's status and an AS/RS's capacity.
// Requesting and cancelling jobs stay on REST. No outbound A2A client lives here, and no auth.
package a2a

import (
	"context"
	"iter"
	"net/http"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/IQVO/asrs-control/internal/domain/capacity"
	"github.com/IQVO/asrs-control/internal/domain/storagejob"
)

// Config is the deployment identity of the agent: the absolute URL clients dial (advertised on
// the card; in-cluster http://<release>-a2a.<ns>.svc.cluster.local:8093/) and the card version.
type Config struct {
	AgentURL string
	Version  string
}

// Reads is the read-only slice of the application service the skills need; *usecases.Service
// implements it. Nothing here can write (ADR 0005); cmd/a2a wires no write seam.
type Reads interface {
	Job(ctx context.Context, asrsID, jobID string) (*storagejob.Job, error)
	Capacity(ctx context.Context, asrsID string) (capacity.Snapshot, error)
}

// Deps is everything the skills need, injected by the composition root.
type Deps struct {
	Reads Reads
}

// Card is the Agent Card: two curated, goal-shaped READ skills (job_status, asrs_capacity), each
// tagged authority:L1 (Agentic Standard v2) like the MCP tools they mirror. No security schemes on
// purpose: unauthenticated by the same fleet decision as REST and MCP.
func Card(cfg Config) *a2a.AgentCard {
	return &a2a.AgentCard{
		Name: "asrs-control",
		Description: "asrs-control agent (read-only): the WCS subsystem controller of the automated storage and retrieval " +
			"systems. It answers where one storage job stands and how much capacity an AS/RS has. It never changes data: " +
			"requesting and cancelling jobs and confirming pickups stay on the REST API.",
		Version:             cfg.Version,
		Provider:            &a2a.AgentProvider{Org: "IQVO", URL: "https://github.com/IQVO/asrs-control"},
		SupportedInterfaces: []*a2a.AgentInterface{a2a.NewAgentInterface(cfg.AgentURL, a2a.TransportProtocolJSONRPC)},
		DefaultInputModes:   []string{contentTypeJSON, "text/plain"},
		DefaultOutputModes:  []string{contentTypeJSON, "text/plain"},
		Capabilities:        a2a.AgentCapabilities{Streaming: false},
		Skills: []a2a.AgentSkill{
			{
				ID:   skillJobStatus,
				Name: "Storage job status",
				Description: "Answer one STORE or RETRIEVE job as asrs-control recorded it: kind, container, port, status (QUEUED, " +
					"DISPATCHED, ACKNOWLEDGED, CANCEL_REQUESTED, AWAITING_ARRIVAL, DONE, FAILED, CANCELLED), the failure reason " +
					"and PLC reason code, accepted and ended times. Arguments: asrsId and storageJobId (both required). Answers a " +
					"404 problem for an unknown AS/RS or job. Persists nothing.",
				Tags:       []string{"asrs", "storage-job", "read", "authority:L1"},
				InputModes: []string{contentTypeJSON},
				Examples:   []string{`{"skill":"job_status","arguments":{"asrsId":"ASRS1","storageJobId":"sj-3f2a"}}`},
			},
			{
				ID:   skillAsrsCapacity,
				Name: "AS/RS capacity",
				Description: "Answer one AS/RS's capacity snapshot (what AsrsCapacityChanged publishes): units total and available, " +
					"positions total and free, open external jobs. Arguments: asrsId (required). Answers a 404 problem for an " +
					"unknown AS/RS. Persists nothing.",
				Tags:       []string{"asrs", "capacity", "read", "authority:L1"},
				InputModes: []string{contentTypeJSON},
				Examples:   []string{`{"skill":"asrs_capacity","arguments":{"asrsId":"ASRS1"}}`},
			},
		},
	}
}

// NewExecutor maps an incoming message to a skill and answers with ONE agent Message (a
// message-only agent): both skills are request/response reads, so there is no Task lifecycle.
// The answer carries the caller's contextId (and taskId) so a delegating saga can group it.
func NewExecutor(deps Deps) a2asrv.AgentExecutor {
	return a2asrv.AgentExecutorFunc(func(ctx context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
		return func(yield func(a2a.Event, error) bool) {
			yield(answer(ctx, deps, ec), nil)
		}
	})
}

// Handler wraps the executor in the JSON-RPC 2.0 HTTP handler, the only transport shipped.
func Handler(executor a2asrv.AgentExecutor) http.Handler {
	return a2asrv.NewJSONRPCHandler(a2asrv.NewHandler(executor))
}
