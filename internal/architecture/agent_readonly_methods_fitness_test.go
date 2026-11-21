package architecture

import (
	"go/ast"
	"go/types"
	"slices"
	"strconv"
	"strings"
)

// agentWriteVerbs are the method names agent sources may not select on ANY value (fleet audit
// 2026-11-21, ported from material-flow-control 2556afc / amr-fleet-control 52797e0): the write
// methods of the postgres repos (Save, Insert, Update, Lock, Purge, Delete, Append, Enqueue) and
// of the pgx pool and its transactions (Exec, Begin, BeginTx, CopyFrom, SendBatch); the pool's
// raw-SQL entry points (Query, QueryRow, Acquire, AcquireFunc, AcquireAllIdle, Conn, PgConn,
// ExecParams, Hijack), because a raw pool.Query runs ANY SQL; and this module's write use cases
// and unit-of-work accessors on usecases.Service (RequestStore, RequestRetrieve, Cancel,
// ConfirmPickup, HandleAck, HandleExpired, HandleUp, Prepare, Recover, UnitOfWork, AfterCommit):
// cmd/mcp and cmd/a2a hold a full *usecases.Service, so only this list keeps a write call out of
// the composition roots. The only legit pool methods in agent sources are Close and Ping. A verb
// list, not a type check: the sensor parses (fixtures need no module to compile against), so a
// selector is judged by its name.
//
// False-positive allowances, by design: a package-qualified FUNCTION is not a method and is judged
// by the allowlist instead (slices.Insert, slices.Delete stay allowed; usecases/postgres
// identifiers keep their allowlist). Anything else with one of these names is refused even when it
// is not a database write (a sync.Mutex Lock): an agent source has no business with either, and a
// real need adds its exact expression to agentWriteVerbAllowances with the reason.
var agentWriteVerbs = []string{"Save", "Insert", "Exec", "Begin", "BeginTx", "CopyFrom", "SendBatch", "Enqueue",
	"Append", "Lock", "Purge", "Delete", "Update",
	"Query", "QueryRow", "Acquire", "AcquireFunc", "AcquireAllIdle", "Conn", "PgConn", "ExecParams", "Hijack",
	"RequestStore", "RequestRetrieve", "Cancel", "ConfirmPickup", "HandleAck", "HandleExpired", "HandleUp", "Prepare",
	"Recover", "UnitOfWork", "AfterCommit"}

// agentWriteVerbAllowances maps an exact selector expression (types.ExprString) to why it is not a
// write. Empty: no agent source needs one today.
var agentWriteVerbAllowances = map[string]string{}

// agentRawDriverImports are refused outright: the agent pool is built by the postgres adapter
// only, so a raw driver import can only mean a second, unguarded way to the database.
var agentRawDriverImports = []string{"github.com/jackc/pgx", "database/sql"}

// agentWriteMethodViolations refuses a write verb selected on anything but an imported package, and
// a raw driver import. names resolves each local package name of f (agentQualifiers).
func agentWriteMethodViolations(f *ast.File, names map[string]string) []string {
	var out []string
	for _, imp := range f.Imports {
		if path, err := strconv.Unquote(imp.Path.Value); err == nil {
			for _, raw := range agentRawDriverImports {
				if path == raw || strings.HasPrefix(path, raw+"/") {
					out = append(out, "imports "+path+": a raw database driver (the agent pool comes from the postgres adapter only)")
				}
			}
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok || !slices.Contains(agentWriteVerbs, sel.Sel.Name) {
			return true
		}
		if pkg, isIdent := sel.X.(*ast.Ident); isIdent && pkg.Obj == nil && names[pkg.Name] != "" {
			return true // a package-qualified function: the allowlist judges it
		}
		expr := types.ExprString(sel)
		if _, allowed := agentWriteVerbAllowances[expr]; !allowed {
			out = append(out, "selects "+expr+": "+sel.Sel.Name+" is a write method or raw SQL (the surface reads only)")
		}
		return true
	})
	return out
}
