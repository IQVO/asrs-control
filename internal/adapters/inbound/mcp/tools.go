package mcp

import (
	"context"
	"strconv"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/IQVO/asrs-control/internal/application/usecases"
	"github.com/IQVO/asrs-control/internal/domain/capacity"
	"github.com/IQVO/asrs-control/internal/domain/storagejob"
)

// Reads is the read-only slice of the application service: the reads the REST adapter serves
// (GET /asrs/{asrsId}, GET /asrs/{asrsId}/storage-jobs/{storageJobId}) plus the list, the AS/RS
// ids and the capacity snapshot. *usecases.Service implements it; the adapter holds nothing that
// can write (ADR 0005) and cmd/mcp wires no write seam at all.
type Reads interface {
	AsrsIDs() []string
	Asrs(ctx context.Context, asrsID string) (usecases.AsrsView, error)
	Capacity(ctx context.Context, asrsID string) (capacity.Snapshot, error)
	Job(ctx context.Context, asrsID, jobID string) (*storagejob.Job, error)
	ListJobs(ctx context.Context, r usecases.ListJobsRequest) (usecases.JobPage, error)
}

// Deps is everything the MCP surface needs, injected by the composition root.
type Deps struct {
	Reads Reads
}

type noInput struct{}

type asrsInput struct {
	AsrsID string `json:"asrsId" jsonschema:"the AS/RS id of the layout, e.g. ASRS1 (list_asrs names them)"`
}

type jobInput struct {
	AsrsID       string `json:"asrsId" jsonschema:"the AS/RS id, e.g. ASRS1"`
	StorageJobID string `json:"storageJobId" jsonschema:"the storage job id returned when the job was requested, e.g. sj-3f2a"`
}

type listJobsInput struct {
	AsrsID string `json:"asrsId" jsonschema:"the AS/RS id, e.g. ASRS1"`
	Status string `json:"status,omitempty" jsonschema:"optional status filter: QUEUED, DISPATCHED, ACKNOWLEDGED, CANCEL_REQUESTED, AWAITING_ARRIVAL, DONE, FAILED or CANCELLED; omitted means every status"`
	Cursor string `json:"cursor,omitempty" jsonschema:"optional keyset cursor: the previous page's nextCursor; omitted means the newest page"`
	Limit  int    `json:"limit,omitempty" jsonschema:"optional page size 1..100; omitted means 20"`
}

// registerTools adds the five read-only tools. Each is annotated read-only, idempotent and
// closed-world (the literal is inlined per tool so the fleet's static authority sensor can prove
// ReadOnlyHint) and declares authority L1 (Agentic Standard v2). TestToolSurface pins the set.
func (d Deps) registerTools(server *mcp.Server) {
	closedWorld := false
	addTool(server, &mcp.Tool{Name: "list_asrs",
		Description: "List the AS/RS this asrs-control instance controls (the ids of its layout file). Read-only.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Meta:        authorityMeta(authorityL1)}, d.listAsrs)
	addTool(server, &mcp.Tool{Name: "get_asrs_state",
		Description: "Read one AS/RS as material-flow-control sees it: per aisle the shuttle-and-lift unit (IDLE, EXECUTE or HELD), " +
			"whether it is busy, open external jobs and free positions; every port with the container presented at an output " +
			"port; the recommended input port (the least-loaded available aisle with room; null when none); and the capacity. " +
			"No rack position is exposed. Read-only; an unknown id fails with asrs-not-found.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Meta:        authorityMeta(authorityL1)}, d.getAsrsState)
	addTool(server, &mcp.Tool{Name: "get_asrs_capacity",
		Description: "Read one AS/RS's capacity snapshot (what AsrsCapacityChanged publishes): units total and available, " +
			"positions total and free, open external jobs. Read-only; an unknown id fails with asrs-not-found.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Meta:        authorityMeta(authorityL1)}, d.getAsrsCapacity)
	addTool(server, &mcp.Tool{Name: "get_storage_job",
		Description: "Read one STORE or RETRIEVE job: container, port, reference, status (QUEUED, DISPATCHED, ACKNOWLEDGED, " +
			"CANCEL_REQUESTED, AWAITING_ARRIVAL, DONE, FAILED, CANCELLED), the failure reason and the PLC's reason code, " +
			"accepted_at and ended_at. Read-only; an unknown id fails with storage-job-not-found.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Meta:        authorityMeta(authorityL1)}, d.getStorageJob)
	addTool(server, &mcp.Tool{Name: "list_storage_jobs",
		Description: "List an AS/RS's STORE and RETRIEVE jobs newest first, optionally of one status, keyset-paged (a page's " +
			"nextCursor feeds the next call's cursor; empty when there is no further page). Items have the get_storage_job " +
			"shape. Internal reshuffles are never listed. Read-only.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Meta:        authorityMeta(authorityL1)}, d.listStorageJobs)
}

func (d Deps) listAsrs(_ context.Context, _ noInput) (asrsList, error) {
	return asrsList{AsrsIDs: d.Reads.AsrsIDs()}, nil
}

func (d Deps) getAsrsState(ctx context.Context, in asrsInput) (asrsPayload, error) {
	v, err := d.Reads.Asrs(ctx, in.AsrsID)
	if err != nil {
		return asrsPayload{}, mapError(ctx, err)
	}
	return toAsrs(v), nil
}

func (d Deps) getAsrsCapacity(ctx context.Context, in asrsInput) (capacityPayload, error) {
	c, err := d.Reads.Capacity(ctx, in.AsrsID)
	if err != nil {
		return capacityPayload{}, mapError(ctx, err)
	}
	return toCapacity(c), nil
}

func (d Deps) getStorageJob(ctx context.Context, in jobInput) (jobPayload, error) {
	j, err := d.Reads.Job(ctx, in.AsrsID, in.StorageJobID)
	if err != nil {
		return jobPayload{}, mapError(ctx, err)
	}
	return toJob(j), nil
}

func (d Deps) listStorageJobs(ctx context.Context, in listJobsInput) (jobPagePayload, error) {
	before, err := parseCursor(in.Cursor)
	if err != nil {
		return jobPagePayload{}, err
	}
	page, err := d.Reads.ListJobs(ctx, usecases.ListJobsRequest{AsrsID: in.AsrsID, Status: storagejob.Status(in.Status),
		BeforeSeq: before, Limit: in.Limit})
	if err != nil {
		return jobPagePayload{}, mapError(ctx, err)
	}
	out := jobPagePayload{Items: make([]jobPayload, 0, len(page.Jobs))}
	for _, j := range page.Jobs {
		out.Items = append(out.Items, toJob(j))
	}
	if page.NextBeforeSeq > 0 {
		out.NextCursor = strconv.FormatInt(page.NextBeforeSeq, 10)
	}
	return out, nil
}

// parseCursor decodes the keyset cursor (a page's nextCursor: the decimal sequence number of its
// last job); "" is the newest page. A malformed cursor is invalid-parameter, as on REST.
func parseCursor(raw string) (int64, error) {
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		return 0, toolError("invalid-parameter", "cursor is malformed")
	}
	return n, nil
}

func addTool[In, Out any](server *mcp.Server, tool *mcp.Tool, handle func(context.Context, In) (Out, error)) {
	mcp.AddTool(server, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		out, err := handle(ctx, in)
		if err != nil {
			var zero Out
			return nil, zero, err
		}
		return nil, out, nil
	})
}
