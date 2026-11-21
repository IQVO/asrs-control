package architecture

// Agent surface fitness (ADR 0005): the MCP server and the A2A agent are READ-ONLY adapters.
//
//   - TestA2AAdapterDependencyRule: the A2A adapter depends only on the application and domain
//     layers, and nothing in internal/ depends on it (the MCP twin is TestMCPAdapterDependencyRule).
//   - TestAgentSurfacesAreReadOnly: an ALLOWLIST sensor over each adapter and its cmd/ composition
//     root. The adapter's `Reads` seam holds exactly the listed read methods; the only
//     usecases/ports/postgres identifiers the sources may reference are the read ones (a qualifier
//     is resolved through the file's imports to its import path, so an alias cannot launder a write
//     seam, and a dot or aliased import of those three packages is itself refused); and no
//     write-side adapter (Kafka, outbox, events, the MQTT link, the telegram codec, the memory
//     doubles), no sibling inbound adapter and no outbound A2A client is imported. Allowlist, not
//     denylist: a new write use case is refused without having to be named here first.
//   - TestAgentReadOnlySensorBites proves each rule fires on code fixtures, so the sensor cannot
//     pass vacuously.
//   - agent_readonly_methods_fitness_test.go: no write METHOD (agentWriteVerbs) is selected on any
//     value, and no raw driver (pgx, database/sql) is imported. The hardening (fleet audit
//     2026-11-21, ported from material-flow-control 2556afc / amr-fleet-control 52797e0) is bitten
//     by agent_readonly_hardening_test.go.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	archgo "github.com/arch-go/arch-go/api"
	"github.com/arch-go/arch-go/api/configuration"
)

func TestA2AAdapterDependencyRule(t *testing.T) {
	moduleInfo := configuration.Load(modulePath)
	t.Run("a2a adapter depends only on application and domain", func(t *testing.T) {
		assertArchGoPasses(t, archgo.CheckArchitecture(moduleInfo, configuration.Config{DependenciesRules: []*configuration.DependenciesRule{{
			Package: "**.internal.adapters.inbound.a2a.**",
			ShouldOnlyDependsOn: &configuration.Dependencies{Internal: []string{"**.internal.adapters.inbound.a2a.**",
				"**.internal.application.**", "**.internal.domain.**"}},
		}}}))
	})
	t.Run("nothing else depends on the a2a adapter", func(t *testing.T) {
		assertArchGoPasses(t, archgo.CheckArchitecture(moduleInfo, configuration.Config{DependenciesRules: []*configuration.DependenciesRule{{
			Package:            "**.internal.**",
			ShouldNotDependsOn: &configuration.Dependencies{Internal: []string{"**.internal.adapters.inbound.a2a.**"}},
		}}}))
	})
}

// agentSurface is one read-only agent adapter and its composition root.
type agentSurface struct {
	name, adapterDir, cmdDir string
	reads                    []string // the exact method set of the adapter's Reads seam, sorted
	forbidSibling            string   // the other agent adapter, never imported
}

var agentSurfaces = []agentSurface{
	{"mcp", "../adapters/inbound/mcp", "../../cmd/mcp", []string{"Asrs", "AsrsIDs", "Capacity", "Job", "ListJobs"}, "/internal/adapters/inbound/a2a"},
	{"a2a", "../adapters/inbound/a2a", "../../cmd/a2a", []string{"Capacity", "Job"}, "/internal/adapters/inbound/mcp"},
}

// agentAllowedSelectors maps a package qualifier to the identifiers agent sources may reference
// through it. Anything else (a write use case, the unit of work, the outbox) is a violation.
var agentAllowedSelectors = map[string][]string{
	"ports": {},
	"usecases": {"New", "Deps", "Service", "AsrsView", "ListJobsRequest", "JobPage", "ErrUnknownAsrs", "ErrJobNotFound",
		"ErrInvalidRequest"},
	"postgres": {"MigrationsURL", "RunMigrations", "NewPool", "NewStorageMapRepo", "NewStorageJobRepo", "NewUnitRepo"},
}

// agentForbiddenImports are import-path fragments no agent source may use, and why.
var agentForbiddenImports = map[string]string{
	"a2a-go/v2/a2aclient":                "an outbound A2A client (the agent only answers)",
	"/internal/adapters/outbound/kafka":  "Kafka: a read-only surface publishes no fact",
	"/internal/adapters/outbound/outbox": "the outbox relay: a read-only surface never publishes",
	"/internal/adapters/outbound/events": "the event sink: a read-only surface never appends an event",
	"/internal/adapters/outbound/memory": "the in-memory test doubles (real Postgres only at runtime)",
	"/internal/adapters/mqtt":            "the PLC link: only cmd/api talks to the PLC",
	"/internal/adapters/telegram":        "the telegram codec: only cmd/api talks to the PLC",
	"/internal/adapters/inbound/http":    "the REST adapter (the inbound adapters are siblings)",
}

func agentSourceViolations(s agentSurface, filename string, src any) ([]string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), filename, src, 0)
	if err != nil {
		return nil, err
	}
	out := agentImportViolations(s, f)
	out = append(out, agentSelectorViolations(f)...)
	names, _ := agentQualifiers(f)
	out = append(out, agentWriteMethodViolations(f, names)...)
	return append(out, agentReadsViolations(s, f)...), nil
}

func agentImportViolations(s agentSurface, f *ast.File) []string {
	var out []string
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		for fragment, why := range agentForbiddenImports {
			if strings.Contains(path, fragment) {
				out = append(out, "imports "+path+": "+why)
			}
		}
		if strings.Contains(path, s.forbidSibling) {
			out = append(out, "imports "+path+": the agent adapters are siblings")
		}
	}
	return out
}

// agentGuardedPackages maps the import-path suffix of each guarded package to its allowlist key:
// the allowlist applies by IMPORT PATH, whatever local name a file binds the package to.
var agentGuardedPackages = map[string]string{
	"/internal/application/usecases":       "usecases",
	"/internal/application/ports":          "ports",
	"/internal/adapters/outbound/postgres": "postgres",
}

// guardedKey is the allowlist key of an import path, "" when the path is not a guarded package.
func guardedKey(path string) string {
	for suffix, key := range agentGuardedPackages {
		if strings.HasSuffix(path, suffix) {
			return key
		}
	}
	return ""
}

// agentQualifiers resolves every local package name of f to its import path (the alias, or the
// last path element), and refuses a dot or aliased import of a guarded package: a dot import
// hides every selector the allowlist reads, and an alias only obscures which package a reader sees.
func agentQualifiers(f *ast.File) (map[string]string, []string) {
	names, out := map[string]string{}, []string(nil)
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		local := path[strings.LastIndex(path, "/")+1:]
		if imp.Name != nil {
			local = imp.Name.Name
		}
		if key := guardedKey(path); key != "" && imp.Name != nil && local != "_" {
			if local == "." {
				out = append(out, "dot-imports "+path+": a guarded package must be referenced as "+key+".<Name>")
				continue
			}
			if local != key {
				out = append(out, "imports "+path+" as "+local+": a guarded package must keep its name "+key)
			}
		}
		names[local] = path
	}
	return names, out
}

func agentSelectorViolations(f *ast.File) []string {
	names, out := agentQualifiers(f)
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Obj != nil { // not a qualifier, or a variable declared in this file
			return true
		}
		key := pkg.Name // a qualifier no import binds (fixtures): judged by its name
		if path, imported := names[pkg.Name]; imported {
			key = guardedKey(path)
		}
		if allowed, guarded := agentAllowedSelectors[key]; guarded && !slices.Contains(allowed, sel.Sel.Name) {
			out = append(out, "references "+pkg.Name+"."+sel.Sel.Name+" ("+key+"), which is not on the read-only allowlist")
		}
		return true
	})
	return out
}

func agentReadsViolations(s agentSurface, f *ast.File) []string {
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		spec, ok := n.(*ast.TypeSpec)
		if !ok || spec.Name.Name != "Reads" {
			return true
		}
		iface, ok := spec.Type.(*ast.InterfaceType)
		if !ok {
			return true
		}
		var got []string
		for _, m := range iface.Methods.List {
			for _, name := range m.Names {
				got = append(got, name.Name)
			}
		}
		sort.Strings(got)
		if strings.Join(got, ",") != strings.Join(s.reads, ",") {
			out = append(out, "Reads declares "+strings.Join(got, ",")+", want exactly "+strings.Join(s.reads, ","))
		}
		return false
	})
	return out
}

func TestAgentSurfacesAreReadOnly(t *testing.T) {
	for _, s := range agentSurfaces {
		for _, dir := range []string{s.adapterDir, s.cmdDir} {
			files := goFilesUnder(t, dir, false)
			if len(files) == 0 {
				t.Fatalf("no %s sources under %s; the sensor is looking in the wrong place", s.name, dir)
			}
			t.Logf("checking %d %s sources under %s", len(files), s.name, dir)
			for _, path := range files {
				for _, v := range agentFileViolations(t, s, path) {
					t.Errorf("%s %s (ADR 0005: the %s surface is read-only)", path, v, s.name)
				}
			}
		}
	}
}

func agentFileViolations(t *testing.T, s agentSurface, path string) []string {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	violations, err := agentSourceViolations(s, path, src)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return violations
}

func TestAgentReadOnlySensorBites(t *testing.T) {
	a2aSurface, mcpSurface := agentSurfaces[1], agentSurfaces[0]
	bad := map[string]string{
		"a write use case":        "package x\nvar _ = usecases.RequestStore\n",
		"the unit of work":        "package x\nvar _ = postgres.NewUnitOfWork\n",
		"the outbox repo":         "package x\nvar _ = postgres.NewOutboxRepo\n",
		"a port":                  "package x\nvar _ = ports.UnitOfWork\n",
		"a Kafka import":          "package x\nimport _ \"github.com/IQVO/asrs-control/internal/adapters/outbound/kafka\"\n",
		"the MQTT link":           "package x\nimport _ \"github.com/IQVO/asrs-control/internal/adapters/mqtt\"\n",
		"an A2A client":           "package x\nimport _ \"github.com/a2aproject/a2a-go/v2/a2aclient\"\n",
		"a memory double":         "package x\nimport _ \"github.com/IQVO/asrs-control/internal/adapters/outbound/memory\"\n",
		"the sibling MCP adapter": "package x\nimport _ \"github.com/IQVO/asrs-control/internal/adapters/inbound/mcp\"\n",
		"a write method on Reads": "package x\ntype Reads interface{ Capacity(); Job(); Cancel() }\n",
		"a missing read method":   "package x\ntype Reads interface{ Job() }\n",
	}
	for name, src := range bad {
		if got, err := agentSourceViolations(a2aSurface, "fixture.go", src); err != nil || len(got) == 0 {
			t.Errorf("the sensor does not flag %s (violations=%v, err=%v)", name, got, err)
		}
	}
	good := "package x\n// usecases.RequestStore and postgres.NewUnitOfWork are only named in this comment\n" +
		"import _ \"github.com/a2aproject/a2a-go/v2/a2asrv\"\n" +
		"var _ = usecases.New\nvar _ = postgres.NewStorageJobRepo\ntype Reads interface{ Job(); Capacity() }\n"
	if got, err := agentSourceViolations(a2aSurface, "fixture.go", good); err != nil || len(got) != 0 {
		t.Errorf("false positive on the read wiring: %v (err=%v)", got, err)
	}
	if got, _ := agentSourceViolations(mcpSurface, "fixture.go", "package x\nimport _ \"github.com/IQVO/asrs-control/internal/adapters/inbound/a2a\"\n"); len(got) == 0 {
		t.Error("the sensor does not flag the MCP sources importing the A2A adapter")
	}
}
