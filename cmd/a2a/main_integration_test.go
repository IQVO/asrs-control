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

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"

	inbounda2a "github.com/IQVO/asrs-control/internal/adapters/inbound/a2a"
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
// the A2A adapter and the router, driven by the SDK client built from the served card. It reads
// what the write side stored and writes nothing itself.
func TestA2AServesPostgresBackedSkillsAndWritesNothing(t *testing.T) {
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
	srv := httptest.NewServer(newAgent(inbounda2a.Deps{Reads: reads}, testAgentURL, "it", quiet(), "asrs-control-a2a"))
	defer srv.Close()
	client, err := a2aclient.NewFromCard(context.Background(), inbounda2a.Card(inbounda2a.Config{AgentURL: srv.URL + "/a2a", Version: "it"}))
	if err != nil {
		t.Fatal(err)
	}
	for skill, args := range map[string]map[string]any{
		"job_status":    {"asrsId": "ASRS1", "storageJobId": jobID},
		"asrs_capacity": {"asrsId": "ASRS1"},
	} {
		part := a2a.NewDataPart(map[string]any{"skill": skill, "arguments": args})
		resp, err := client.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, part)})
		if err != nil {
			t.Fatalf("%s: %v", skill, err)
		}
		msg, ok := resp.(*a2a.Message)
		if !ok || len(msg.Parts) != 2 {
			t.Fatalf("%s: reply %T", skill, resp)
		}
		raw, _ := json.Marshal(msg.Parts[0].Content.(a2a.Data).Value)
		if strings.Contains(string(raw), `"problem"`) {
			t.Fatalf("%s answered a problem: %s", skill, raw)
		}
		if skill == "job_status" && !strings.Contains(string(raw), jobID) {
			t.Errorf("job_status does not answer the seeded job: %s", raw)
		}
		if skill == "asrs_capacity" && !strings.Contains(string(raw), `"openJobs":1`) {
			t.Errorf("asrs_capacity does not count the seeded job: %s", raw)
		}
	}
	if got := rowCount(t, url, "storage_jobs"); got != jobsBefore {
		t.Errorf("the A2A agent changed storage_jobs: %d rows, want %d", got, jobsBefore)
	}
	if got := rowCount(t, url, "outbox_events"); got != 0 {
		t.Errorf("outbox_events = %d rows: the A2A agent must append nothing", got)
	}
}
