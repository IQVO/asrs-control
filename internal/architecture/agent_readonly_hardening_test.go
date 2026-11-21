package architecture

import "testing"

// Hardened agent read-only sensor (fleet audit 2026-11-21, defect 2), ported from
// material-flow-control 2556afc and amr-fleet-control 52797e0
// (internal/architecture/agent_readonly*_fitness_test.go). Every fixture below passed the 007bf7b
// origin sensor of this repo:
//
//	G1 a qualifier was matched by its LOCAL name, so an alias laundered a write seam;
//	G2 a dot or aliased import of a guarded package was not refused;
//	G3 a write METHOD on a value an allowlisted constructor returned (or on the Service) got through;
//	G4 the pgx pool's raw-SQL methods (Query, QueryRow, Exec, Begin, Acquire ...) got through;
//	G5 a raw driver import (pgx, database/sql) was not refused.

// agentWriteVerbsWant pins agentWriteVerbs from the test side: the repo and pool write methods,
// the pool's raw-SQL entry points, and this module's write use cases on usecases.Service.
var agentWriteVerbsWant = []string{"Save", "Insert", "Exec", "Begin", "BeginTx", "CopyFrom", "SendBatch", "Enqueue",
	"Append", "Lock", "Purge", "Delete", "Update",
	"Query", "QueryRow", "Acquire", "AcquireFunc", "AcquireAllIdle", "Conn", "PgConn", "ExecParams", "Hijack",
	"RequestStore", "RequestRetrieve", "Cancel", "ConfirmPickup", "HandleAck", "HandleExpired", "HandleUp", "Prepare",
	"Recover", "UnitOfWork", "AfterCommit"}

func TestAgentReadOnlySensorResolvesQualifiersByImportPath(t *testing.T) {
	a2aSurface, mcpSurface := agentSurfaces[1], agentSurfaces[0]
	uc := "\"" + modulePath + "/internal/application/usecases\""
	pg := "\"" + modulePath + "/internal/adapters/outbound/postgres\""
	bad := map[string]string{
		// G1: the allowlist resolves a qualifier through the file's imports.
		"an aliased write use case": "package x\nimport u " + uc + "\nfunc f(s *u.Service) { _, _ = s.RequestStore(ctx, u.StoreRequest{}) }\n",
		"an aliased write type":     "package x\nimport u " + uc + "\nvar _ = u.StoreRequest{}\n",
		"an aliased unit of work":   "package x\nimport p " + pg + "\nvar _ = p.NewUnitOfWork\n",
		"an aliased outbox repo":    "package x\nimport p " + pg + "\nvar _ = p.NewOutboxRepo\n",
		"an aliased port":           "package x\nimport pt \"" + modulePath + "/internal/application/ports\"\nvar _ = pt.UnitOfWork\n",
		// G2: a dot or aliased import of a guarded package is itself refused.
		"an aliased guarded import": "package x\nimport u " + uc + "\nvar _ = u.New\n",
		"a dot import":              "package x\nimport . " + uc + "\nvar _ = StoreRequest{}\n",
		"a dot import of postgres":  "package x\nimport . " + pg + "\nvar _ = NewUnitOfWork\n",
	}
	for name, src := range bad {
		if got, err := agentSourceViolations(a2aSurface, "fixture.go", src); err != nil || len(got) == 0 {
			t.Errorf("the sensor does not flag %s (violations=%v, err=%v)", name, got, err)
		}
	}
	// cmd/mcp's own aliases name packages the allowlist does not guard, and a guarded local NAME
	// bound to another import path is not that package.
	wiring := "package x\nimport inboundmcp \"" + modulePath + "/internal/adapters/inbound/mcp\"\n" +
		"import layoutconfig \"" + modulePath + "/internal/config\"\nimport usecases \"example.com/elsewhere/usecases\"\n" +
		"var _ = inboundmcp.NewServer\nvar _ = layoutconfig.LoadLayouts\nvar _ = usecases.RequestStore\n"
	if got, err := agentSourceViolations(mcpSurface, "fixture.go", wiring); err != nil || len(got) != 0 {
		t.Errorf("false positive on cmd/mcp's aliased wiring: %v (err=%v)", got, err)
	}
}

func TestAgentReadOnlySensorRefusesWriteMethodsAndRawDrivers(t *testing.T) {
	a2aSurface := agentSurfaces[1]
	pg := "import \"" + modulePath + "/internal/adapters/outbound/postgres\"\n"
	bad := map[string]string{
		// G3: a write method on a value an allowlisted constructor returned.
		"a write method on a constructed repo": "package x\n" + pg + "func f() { _ = postgres.NewStorageJobRepo(nil).Insert(ctx, nil) }\n",
		"a write method on a repo variable":    "package x\n" + pg + "func f() { r := postgres.NewStorageMapRepo(nil); _ = r.Save(ctx, id, nil) }\n",
		"the aisle row lock":                   "package x\n" + pg + "func f() { _, _ = postgres.NewStorageMapRepo(nil).Lock(ctx, l, 1) }\n",
		"a write method on the units repo":     "package x\n" + pg + "func f() { _ = postgres.NewUnitRepo(nil).Save(ctx, nil) }\n",
		"a method value of a write":            "package x\n" + pg + "func f() { save := postgres.NewStorageJobRepo(nil).Update; _ = save }\n",
		"a write method through a field":       "package x\nfunc (d deps) f() { _, _ = d.pool.Begin(ctx) }\n",
		"a method expression of a write":       "package x\n" + pg + "var _ = (*postgres.StorageJobRepo).Insert\n",
		"a write use case on the service":      "package x\nfunc f(s svc) { _, _ = s.RequestStore(ctx, req) }\n",
		"a cancellation on the service":        "package x\nfunc f(s svc) { _, _ = s.Cancel(ctx, a, j) }\n",
		"a PLC telegram on the service":        "package x\nfunc f(s svc) { _ = s.HandleAck(ctx, a, tg) }\n",
		// G4: raw SQL through the pool.
		"Exec on the pool":              "package x\n" + pg + "func f() { p, _ := postgres.NewPool(ctx, dsn); _, _ = p.Exec(ctx, \"DELETE FROM storage_jobs\") }\n",
		"raw SQL through pool.Query":    "package x\n" + pg + "func f() { p, _ := postgres.NewPool(ctx, dsn); _, _ = p.Query(ctx, \"DELETE FROM storage_jobs\") }\n",
		"raw SQL through pool.QueryRow": "package x\n" + pg + "func f() { p, _ := postgres.NewPool(ctx, dsn); _ = p.QueryRow(ctx, \"UPDATE storage_jobs SET status = 'x' RETURNING 1\") }\n",
		"a pooled connection":           "package x\n" + pg + "func f() { p, _ := postgres.NewPool(ctx, dsn); c, _ := p.Acquire(ctx); _ = c.Conn().PgConn().ExecParams(ctx, \"DELETE FROM storage_jobs\", nil, nil, nil, nil) }\n",
		// G5: a raw driver import.
		"a pgx import":          "package x\nimport _ \"github.com/jackc/pgx/v5/pgxpool\"\n",
		"a pgx root import":     "package x\nimport _ \"github.com/jackc/pgx/v5\"\n",
		"a database/sql import": "package x\nimport _ \"database/sql\"\n",
	}
	for _, verb := range agentWriteVerbsWant {
		bad["the write verb "+verb] = "package x\nfunc f(v repo) { _ = v." + verb + " }\n"
	}
	for name, src := range bad {
		if got, err := agentSourceViolations(a2aSurface, "fixture.go", src); err != nil || len(got) == 0 {
			t.Errorf("the sensor does not flag %s (violations=%v, err=%v)", name, got, err)
		}
	}
	// The read wiring of cmd/a2a, package-qualified slice helpers and non-write methods stay allowed.
	good := "package x\nimport \"slices\"\n" + pg +
		"func f() { pool, _ := postgres.NewPool(ctx, dsn); defer pool.Close(); _ = pool.Ping(ctx)\n" +
		"svc, _ := usecases.New(usecases.Deps{Maps: postgres.NewStorageMapRepo(pool), Jobs: postgres.NewStorageJobRepo(pool), Units: postgres.NewUnitRepo(pool)})\n" +
		"_, _ = svc.Job(ctx, a, j); _, _ = svc.Capacity(ctx, a); s := slices.Insert(xs, 0, 1); _ = slices.Delete(s, 0, 1) }\n"
	if got, err := agentSourceViolations(a2aSurface, "fixture.go", good); err != nil || len(got) != 0 {
		t.Errorf("false positive on the read wiring: %v (err=%v)", got, err)
	}
}
