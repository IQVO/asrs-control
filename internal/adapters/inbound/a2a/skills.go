package a2a

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/IQVO/asrs-control/internal/application/usecases"
)

// tracerName is the instrumentation scope of the skill spans ("a2a.skill <id>").
const tracerName = "github.com/IQVO/asrs-control/internal/adapters/inbound/a2a"

// The skill ids on the Agent Card.
const (
	skillJobStatus    = "job_status"
	skillAsrsCapacity = "asrs_capacity"
)

// contentTypeJSON is the input mode of every skill.
const contentTypeJSON = "application/json"

// skillRequest is the wire contract of every call: the first message part carries the skill id and
// its arguments, as a data part or a text part holding the same JSON. Strict on purpose.
type skillRequest struct {
	Skill     string          `json:"skill"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

type jobStatusArgs struct {
	AsrsID       string `json:"asrsId"`
	StorageJobID string `json:"storageJobId"`
}

type asrsCapacityArgs struct {
	AsrsID string `json:"asrsId"`
}

// answer maps one message to a skill and returns the reply (the whole executor body).
func answer(ctx context.Context, deps Deps, ec *a2asrv.ExecutorContext) a2a.Event {
	req, p := parseSkillRequest(ec)
	if p != nil {
		return reply(ec, envelope{Problem: p}, p.summary())
	}
	ctx, span := otel.Tracer(tracerName).Start(ctx, "a2a.skill "+req.Skill)
	defer span.End()
	span.SetAttributes(attribute.String("a2a.skill", req.Skill))
	dto, p := run(ctx, deps, req)
	if p != nil {
		span.SetStatus(codes.Error, p.Detail)
		return reply(ec, envelope{Skill: req.Skill, Problem: p}, p.summary())
	}
	return reply(ec, envelope{Skill: req.Skill, Data: &dto}, dto.summary)
}

func run(ctx context.Context, deps Deps, req skillRequest) (skillDTO, *problem) {
	switch req.Skill {
	case skillJobStatus:
		return deps.jobStatus(ctx, req.Arguments)
	case skillAsrsCapacity:
		return deps.asrsCapacity(ctx, req.Arguments)
	default:
		return skillDTO{}, problemUnknownSkill(req.Skill)
	}
}

func (d Deps) jobStatus(ctx context.Context, raw json.RawMessage) (skillDTO, *problem) {
	var args jobStatusArgs
	if err := decodeStrict(raw, &args); err != nil {
		return skillDTO{}, problemInvalidArgument(fmt.Sprintf("arguments: %v", err))
	}
	asrsID, jobID := strings.TrimSpace(args.AsrsID), strings.TrimSpace(args.StorageJobID)
	if asrsID == "" || jobID == "" {
		return skillDTO{}, problemInvalidArgument("asrsId and storageJobId are required")
	}
	j, err := d.Reads.Job(ctx, asrsID, jobID)
	if err != nil {
		return skillDTO{}, problemFromUseCase(ctx, err)
	}
	dto := toJobDTO(j)
	return skillDTO{Job: &dto, summary: summarizeJob(dto)}, nil
}

func (d Deps) asrsCapacity(ctx context.Context, raw json.RawMessage) (skillDTO, *problem) {
	var args asrsCapacityArgs
	if err := decodeStrict(raw, &args); err != nil {
		return skillDTO{}, problemInvalidArgument(fmt.Sprintf("arguments: %v", err))
	}
	asrsID := strings.TrimSpace(args.AsrsID)
	if asrsID == "" {
		return skillDTO{}, problemInvalidArgument("asrsId is required")
	}
	c, err := d.Reads.Capacity(ctx, asrsID)
	if err != nil {
		return skillDTO{}, problemFromUseCase(ctx, err)
	}
	dto := toCapacityDTO(c)
	return skillDTO{Capacity: &dto, summary: summarizeCapacity(dto)}, nil
}

// reply is the agent Message of one run: a data part with the envelope and a text part with the
// one-line summary, bound to the caller's context (and task).
func reply(ec *a2asrv.ExecutorContext, env envelope, summary string) a2a.Event {
	return a2a.NewMessageForTask(a2a.MessageRoleAgent, ec, a2a.NewDataPart(env), a2a.NewTextPart(summary))
}

// parseSkillRequest reads the request off the first part. A malformed delegation is answered as a
// problem in the envelope, never raised as a transport error.
func parseSkillRequest(ec *a2asrv.ExecutorContext) (skillRequest, *problem) {
	msg := ec.Message
	if msg == nil || len(msg.Parts) == 0 {
		return skillRequest{}, problemMalformedRequest("the message carries no parts")
	}
	raw, err := partJSON(msg.Parts[0])
	if err != nil {
		return skillRequest{}, problemMalformedRequest(fmt.Sprintf("the first message part is not a skill request: %v", err))
	}
	var req skillRequest
	if err := decodeStrict(raw, &req); err != nil {
		return skillRequest{}, problemMalformedRequest(fmt.Sprintf("the skill request is not valid: %v", err))
	}
	if req.Skill == "" {
		return skillRequest{}, problemMalformedRequest(`the skill request carries no "skill" field`)
	}
	return req, nil
}

func partJSON(part *a2a.Part) ([]byte, error) {
	switch content := part.Content.(type) {
	case a2a.Data:
		return json.Marshal(content.Value)
	case a2a.Text:
		return []byte(string(content)), nil
	default:
		return nil, errors.New("expected a data part (or a text part holding the skill JSON)")
	}
}

// decodeStrict decodes one JSON document, refusing unknown fields and trailing data; absent
// arguments decode as {} so the skill's own required checks speak.
func decodeStrict(raw []byte, dst any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = []byte("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("unexpected data after the JSON document")
	}
	return nil
}

// problemFromUseCase maps a read error with the REST adapter's classification and slugs: an
// unknown AS/RS or job is 404, anything else 500 with an adapter-owned detail (never the cause).
func problemFromUseCase(ctx context.Context, err error) *problem {
	switch {
	case errors.Is(err, usecases.ErrUnknownAsrs):
		return &problem{Type: "asrs-not-found", Title: "AS/RS not found", Status: 404, Detail: "no AS/RS with that asrsId is configured"}
	case errors.Is(err, usecases.ErrJobNotFound):
		return &problem{Type: "storage-job-not-found", Title: "Storage job not found", Status: 404,
			Detail: "no STORE or RETRIEVE job with that storageJobId exists in that AS/RS"}
	default:
		slog.ErrorContext(ctx, "a2a skill failed", "error", logReplacer.Replace(err.Error()))
		return &problem{Type: "internal-error", Title: "Internal server error", Status: 500,
			Detail: "the skill failed; see the server logs (trace id in the response metadata)"}
	}
}
