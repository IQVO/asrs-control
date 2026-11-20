package mcp

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// reviewJobSOP is the operator standard operating procedure the review_storage_job prompt hands
// the model (Agentic Standard v2, AS-3): how to read the results, when to escalate and what done
// means. It names tools but never calls them, and every tool it names is read-only (ADR 0005).
const reviewJobSOP = `You are reviewing one storage job of an automated storage and retrieval system (AS/RS) controlled by asrs-control. Use only the MCP tools of asrs-control; never assume state, read it.

Procedure:
1. Call get_storage_job(asrsId = ASRS, storageJobId = JOB). It is asrs-control's own record: kind (STORE or RETRIEVE), container, port, status, the failure reason and the PLC's reason code, accepted_at and ended_at.
2. Call get_asrs_state(asrsId = ASRS) for the aisle context: the shuttle-and-lift unit state (IDLE, EXECUTE, HELD), whether it is busy, the open jobs ahead of this one and the free positions.
3. For history, call list_storage_jobs(asrsId = ASRS, status = FAILED): a run of failures with the same reason points at equipment, not at one job.

Interpretation:
- QUEUED waits for its aisle (one job per aisle unit at a time); DISPATCHED was sent to the PLC; ACKNOWLEDGED was accepted by the PLC; AWAITING_ARRIVAL is a retrieve on its way to the output port; DONE, FAILED and CANCELLED are final.
- A STORE is DONE at the PLC's JOB_DONE; a RETRIEVE is DONE when the container arrives at the port (its pickup is then confirmed by material-flow-control).
- reason EQUIPMENT_UNREACHABLE means the PLC never acknowledged the command after the retransmits; EQUIPMENT_FAULT opens an exception case; STORAGE_MAP_MISMATCH means the PLC disagreed with the storage map; CANCELLED was requested by material-flow-control.
- A HELD unit fails its queued jobs EQUIPMENT_FAULT and accepts no new job until it is released.

Escalate to the WCS operator when: a job has been ACKNOWLEDGED for much longer than its aisle's other jobs took (the PLC may have lost its JOB_DONE; protocol v1 has no job-state query), a unit stays HELD, or failures with the same reason repeat on one aisle. Requesting, cancelling a job and confirming a pickup are REST operations of asrs-control called by material-flow-control: nothing in this server can perform them, so do not promise them.

Done means: you have reported the job's kind, status and reason as recorded, its aisle's state, and named the escalation when the evidence warrants it.`

// registerPrompts adds the workflow prompt (read-only by construction: a prompt returns text).
func (d Deps) registerPrompts(server *mcp.Server) {
	server.AddPrompt(&mcp.Prompt{
		Name:        "review_storage_job",
		Description: "Standard operating procedure for reviewing one storage job: read the job and its aisle, interpret status and reason, and know when to escalate to the WCS operator.",
		Arguments: []*mcp.PromptArgument{
			{Name: "asrsId", Description: "the AS/RS id, e.g. ASRS1", Required: true},
			{Name: "storageJobId", Description: "the storage job id to review, e.g. sj-3f2a", Required: true},
		},
	}, func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		asrs := orPlaceholder(req.Params.Arguments["asrsId"], "<asrsId>")
		job := orPlaceholder(req.Params.Arguments["storageJobId"], "<storageJobId>")
		text := strings.NewReplacer("ASRS", asrs, "JOB", job).Replace(reviewJobSOP)
		return &mcp.GetPromptResult{
			Description: "How to review a storage job: read it and its aisle, interpret status and reason, escalate stuck or repeated failures.",
			Messages:    []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: text}}},
		}, nil
	})
}

func orPlaceholder(v, placeholder string) string {
	if v = strings.TrimSpace(v); v == "" {
		return placeholder
	}
	return v
}
