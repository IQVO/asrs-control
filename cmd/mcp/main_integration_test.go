//go:build integration

package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	inboundmcp "github.com/IQVO/asrs-control/internal/adapters/inbound/mcp"
	"github.com/IQVO/asrs-control/internal/adapters/outbound/memory"
	"github.com/IQVO/asrs-control/internal/adapters/outbound/postgres"
	"github.com/IQVO/asrs-control/internal/application/usecases"
	layoutfile "github.com/IQVO/asrs-control/internal/config"
	"github.com/IQVO/asrs-control/internal/testing/pgtest"
)

const layoutYAML = `asrs:
  - id: ASRS1
    aisles: 2
    levels: 1
    slots: 2
    doubleDeep: true
    ports:
      - {id: IN1, aisle: 1, kind: INPUT}
      - {id: OUT1, aisle: 1, kind: OUTPUT}
      - {id: IN2, aisle: 2, kind: INPUT}
      - {id: OUT2, aisle: 2, kind: OUTPUT}
`

// seedStore accepts one STORE through the write use cases over the same database, the way cmd/api
// would (test-only wiring: the MCP binary has no write seam).
func seedStore(t *testing.T, url, layoutFile, container string) string {
	t.Helper()
	ctx := context.Background()
	layouts, err := layoutfile.LoadLayouts(layoutFile)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := postgres.NewPool(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	svc, err := usecases.New(usecases.Deps{Layouts: layouts, UoW: postgres.NewUnitOfWork(pool), Maps: postgres.NewStorageMapRepo(pool),
		Jobs: postgres.NewStorageJobRepo(pool), Units: postgres.NewUnitRepo(pool), Capacity: postgres.NewCapacityRepo(pool),
		Telegrams: postgres.NewTelegramInbox(pool), Events: memory.NewEvents(memory.NewStore()), Downlink: memory.NewDownlink(),
		Clock: memory.NewClock(time.Now()), IDs: memory.NewIDs("it-"), Velocity: usecases.VelocityPolicy{Window: time.Hour, FastRetrievals: 3}})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	j, err := svc.RequestStore(ctx, usecases.StoreRequest{AsrsID: "ASRS1", ContainerID: container, PortID: "IN1"})
	if err != nil {
		t.Fatal(err)
	}
	return j.ID()
}

func rowCount(t *testing.T, url, table string) int {
	t.Helper()
	pool, err := postgres.NewPool(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM `+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// The composition root end to end: migrations, the Postgres read repositories, the read use cases,
// the MCP adapter and the router, driven by the SDK client over Streamable HTTP. It reads what the
// write side stored and writes nothing itself.
func TestMCPServesPostgresBackedReadsAndWritesNothing(t *testing.T) {
	url := pgtest.URL(t)
	layoutFile := filepath.Join(t.TempDir(), "layout.yaml")
	if err := os.WriteFile(layoutFile, []byte(layoutYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	jobID := seedStore(t, url, layoutFile, "C1")
	jobsBefore := rowCount(t, url, "storage_jobs")

	reads, closeReads, err := buildReads(context.Background(), config{DatabaseURL: url, MigrationsURL: url, LayoutFile: layoutFile})
	if err != nil {
		t.Fatal(err)
	}
	defer closeReads()
	srv := httptest.NewServer(newRouter(inboundmcp.Handler(inboundmcp.NewServer(inboundmcp.Deps{Reads: reads})), quiet(), "asrs-control-mcp"))
	defer srv.Close()
	session, err := sdk.NewClient(&sdk.Implementation{Name: "it", Version: "0"}, nil).
		Connect(context.Background(), &sdk.StreamableClientTransport{Endpoint: srv.URL + "/mcp"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	for tool, want := range map[string]string{
		"get_storage_job":   `{"asrsId":"ASRS1","storageJobId":"` + jobID + `"}`,
		"list_storage_jobs": `{"asrsId":"ASRS1"}`,
		"get_asrs_state":    `{"asrsId":"ASRS1"}`,
	} {
		res, err := session.CallTool(context.Background(), &sdk.CallToolParams{Name: tool, Arguments: json.RawMessage(want)})
		if err != nil || res.IsError {
			t.Fatalf("%s: %v %+v", tool, err, res)
		}
		raw, _ := json.Marshal(res.StructuredContent)
		if tool != "get_asrs_state" && !strings.Contains(string(raw), jobID) {
			t.Errorf("%s does not answer the seeded job %s: %s", tool, jobID, raw)
		}
		if tool == "get_asrs_state" && !strings.Contains(string(raw), `"open_jobs":1`) {
			t.Errorf("get_asrs_state does not count the seeded job: %s", raw)
		}
	}
	if got := rowCount(t, url, "storage_jobs"); got != jobsBefore {
		t.Errorf("the MCP server changed storage_jobs: %d rows, want %d", got, jobsBefore)
	}
	if got := rowCount(t, url, "outbox_events"); got != 0 {
		t.Errorf("outbox_events = %d rows: the seed used an in-memory sink and the MCP server must append nothing", got)
	}
}
