package mcp_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	inboundmcp "github.com/IQVO/asrs-control/internal/adapters/inbound/mcp"
	"github.com/IQVO/asrs-control/internal/adapters/outbound/memory"
	"github.com/IQVO/asrs-control/internal/application/usecases"
	"github.com/IQVO/asrs-control/internal/domain/layout"
)

var t0 = time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)

// harness is a connected in-memory MCP client over the real server, wired to the application
// service over in-memory repositories the way cmd/mcp wires it over Postgres. The write use
// cases are used ONLY here, to seed jobs the way the REST adapter would: the MCP surface has none.
type harness struct {
	t       *testing.T
	session *sdk.ClientSession
	svc     *usecases.Service
	down    *memory.Downlink
}

func asrsLayout() layout.Layout {
	return layout.Layout{ID: "ASRS1", Aisles: 2, Levels: 1, Slots: 2, DoubleDeep: true, Cost: layout.Cost{PerLevel: 1, PerSlot: 1},
		Ports: []layout.Port{{ID: "IN1", Aisle: 1, Kind: layout.PortInput}, {ID: "OUT1", Aisle: 1, Kind: layout.PortOutput},
			{ID: "IN2", Aisle: 2, Kind: layout.PortInput}, {ID: "OUT2", Aisle: 2, Kind: layout.PortOutput}}}
}

func newService(t *testing.T) (*usecases.Service, *memory.Downlink) {
	t.Helper()
	st := memory.NewStore()
	down := memory.NewDownlink()
	svc, err := usecases.New(usecases.Deps{Layouts: []layout.Layout{asrsLayout()}, UoW: memory.NewUnitOfWork(st),
		Maps: memory.NewStorageMaps(st), Jobs: memory.NewStorageJobs(st), Units: memory.NewUnits(st), Capacity: memory.NewCapacities(st),
		Telegrams: memory.NewTelegrams(st), Events: memory.NewEvents(st), Downlink: down, Clock: memory.NewClock(t0),
		IDs: memory.NewIDs("id-"), Velocity: usecases.VelocityPolicy{Window: time.Hour, FastRetrievals: 3}})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	return svc, down
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	svc, down := newService(t)
	return &harness{t: t, session: connect(t, inboundmcp.Deps{Reads: svc}), svc: svc, down: down}
}

func connect(t *testing.T, deps inboundmcp.Deps) *sdk.ClientSession {
	t.Helper()
	ctx := context.Background()
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := inboundmcp.NewServer(deps).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	session, err := sdk.NewClient(&sdk.Implementation{Name: "test-client", Version: "0"}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// store requests a STORE the way the REST adapter would and returns the job id.
func (h *harness) store(container, port string) string {
	h.t.Helper()
	j, err := h.svc.RequestStore(context.Background(), usecases.StoreRequest{AsrsID: "ASRS1", ContainerID: container, PortID: port})
	if err != nil {
		h.t.Fatalf("RequestStore(%s): %v", container, err)
	}
	return j.ID()
}

func (h *harness) call(name, args string) *sdk.CallToolResult {
	h.t.Helper()
	res, err := h.session.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: json.RawMessage(args)})
	if err != nil {
		h.t.Fatalf("call %s: %v", name, err)
	}
	return res
}

// structured decodes a successful result's structured content into out.
func (h *harness) structured(res *sdk.CallToolResult, out any) {
	h.t.Helper()
	if res.IsError {
		raw, _ := json.Marshal(res.Content)
		h.t.Fatalf("tool errored: %s", raw)
	}
	raw, _ := json.Marshal(res.StructuredContent)
	if err := json.Unmarshal(raw, out); err != nil {
		h.t.Fatalf("decode %s: %v", raw, err)
	}
}

// errorText is the text of an isError result ("" when the call succeeded).
func errorText(res *sdk.CallToolResult) string {
	if !res.IsError {
		return ""
	}
	raw, _ := json.Marshal(res.Content)
	return string(raw)
}
