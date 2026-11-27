#!/usr/bin/env python3
"""Assert this chart's Kong configuration contract (fleet decision 2026-10-13).

ALL Kong configuration -- routes AND plugins -- belongs to the application's Helm chart, not warehouse-infra
Terraform. Chart-rendered objects are Helm/ArgoCD reconciled forever; the old Terraform-side objects vanished on a
cluster rebuild. This test is the sensor for that decision. It fails if:
  * the default values (ci/default-values.yaml) render any Kong object or HTTPRoute (standalone installs must be unaffected),
  * any enabled api/reports HTTPRoute renders WITHOUT the konghq.com/plugins annotation
    (MCP + A2A routes are the Agent Router's and must carry NO Kong annotation),
  * the annotation names KongPlugins the chart does not render,
  * any auth/authorization plugin is configured (forbidden fleet-wide),
  * a route is not on its fleet path or does not rewrite as its binary expects: /api/asrs-control -> / (cmd/api, Service port 80),
    /api/mcp/asrs-control -> /mcp (cmd/mcp), /api/a2a/asrs-control -> / (cmd/a2a), each to its own Service and port,
  * the CORS plugin does not allow Idempotency-Key and If-Match, does not expose ETag, Idempotency-Replayed and
    Mcp-Session-Id, or allows origin "*".

The enabled render uses ci/all-components-values.yaml (every route on) with the three plugins switched on.
Run: python3 charts/asrs-control/tests/test_kong_config.py   (needs helm and PyYAML)
"""

from __future__ import annotations

import subprocess
import sys
from pathlib import Path

CHART = Path(__file__).resolve().parents[1]
FORBIDDEN_PLUGINS = {
    "basic-auth", "key-auth", "jwt", "oauth2", "oidc", "acl",
    "hmac-auth", "ldap-auth", "mtls", "session", "request-termination",
}
PLUGINS_ON = [
    "--set", "kongPlugins.cors.enabled=true",
    "--set", "kongPlugins.prometheus.enabled=true",
    "--set", "kongPlugins.opentelemetry.enabled=true",
]
# route name -> (path, rewrite target, backend Service, port): the fleet gateway contract of asrs-control.
EXPECTED_ROUTES = {
    "asrs-control": ("/api/asrs-control", "/", "asrs-control", 80),
    "asrs-control-mcp": ("/api/mcp/asrs-control", "/mcp", "asrs-control-mcp", 8090),
    "asrs-control-a2a": ("/api/a2a/asrs-control", "/", "asrs-control-a2a", 8093),
}


def render(values_file: str, extra: list[str]) -> list[dict]:
    out = subprocess.run(
        ["helm", "template", "asrs-control", str(CHART), "-f", str(CHART / "ci" / values_file), *extra],
        capture_output=True, text=True,
    )
    if out.returncode != 0:
        print(f"FAIL: helm template ({values_file}):\n{out.stderr}", file=sys.stderr)
        raise SystemExit(1)
    try:
        import yaml  # type: ignore
    except ModuleNotFoundError:  # pragma: no cover - environment guard
        print("FAIL: PyYAML not available; a Kong contract test must not pass vacuously", file=sys.stderr)
        raise SystemExit(1)
    return [d for d in yaml.safe_load_all(out.stdout) if d]


def check_defaults() -> list[str]:
    failures: list[str] = []
    docs = render("default-values.yaml", [])
    kong = [d for d in docs if "Kong" in (d.get("kind") or "")]
    if kong:
        failures.append(f"default values render Kong objects {sorted(d['metadata']['name'] for d in kong)}; keep plugin blocks default-off")
    if any(d.get("kind") == "HTTPRoute" for d in docs):
        failures.append("default values render an HTTPRoute; routes must be default-off")
    return failures


def check_annotations(routes: list[dict], plugins: dict[str, dict]) -> list[str]:
    failures: list[str] = []
    for r in routes:
        ann = (r.get("metadata", {}).get("annotations") or {}).get("konghq.com/plugins")
        if r["metadata"]["name"].endswith(("-a2a", "-mcp")):
            # MCP + A2A are exposed by the Agent Router (Envoy Gateway), not Kong
            # (warehouse-infra ADR 0014 / 0015): no Kong annotation, not on the Kong Gateway.
            if any(k.startswith("konghq.com/") for k in (r.get("metadata", {}).get("annotations") or {})):
                failures.append(f"HTTPRoute {r['metadata']['name']} carries a konghq.com/* annotation; "
                                "MCP/A2A routes belong to the Agent Router, not Kong")
            for ref in r.get("spec", {}).get("parentRefs", []):
                if ref.get("name") == "warehouse-gateway" or ref.get("namespace") == "kong":
                    failures.append(f"HTTPRoute {r['metadata']['name']} attaches to the Kong Gateway "
                                    f"({ref.get('name')}/{ref.get('namespace')}); use the agent-router Gateway")
            continue
        if not ann:
            failures.append(f"HTTPRoute {r['metadata']['name']} has no konghq.com/plugins annotation")
            continue
        for name in ann.split(","):
            if name not in plugins:
                failures.append(f"HTTPRoute {r['metadata']['name']} references plugin '{name}' that the chart does not render")
    for name, p in plugins.items():
        if p.get("plugin", "") in FORBIDDEN_PLUGINS:
            failures.append(f"KongPlugin {name} is '{p['plugin']}': authentication/authorization plugins are forbidden at the gateway")
    return failures


def check_routes(routes: list[dict]) -> list[str]:
    failures: list[str] = []
    by_name = {r["metadata"]["name"]: r for r in routes}
    if set(by_name) != set(EXPECTED_ROUTES):
        failures.append(f"HTTPRoutes {sorted(by_name)}, want {sorted(EXPECTED_ROUTES)}")
    for name, (path, target, service, port) in EXPECTED_ROUTES.items():
        r = by_name.get(name)
        if r is None:
            continue
        rules = r.get("spec", {}).get("rules", [])
        if len(rules) != 1:
            failures.append(f"HTTPRoute {name} has {len(rules)} rules, want 1")
            continue
        rule = rules[0]
        got_path = rule["matches"][0]["path"]
        if got_path.get("value") != path or got_path.get("type") != "PathPrefix":
            failures.append(f"HTTPRoute {name} matches {got_path}, want PathPrefix {path}")
        rewrites = [f["urlRewrite"]["path"].get("replacePrefixMatch") for f in rule.get("filters", []) if f.get("type") == "URLRewrite"]
        if rewrites != [target]:
            failures.append(f"HTTPRoute {name} rewrites to {rewrites}, want [{target!r}]")
        backends = [(b["name"], b["port"]) for b in rule.get("backendRefs", [])]
        if backends != [(service, port)]:
            failures.append(f"HTTPRoute {name} sends to {backends}, want [({service!r}, {port})]")
        if not r["spec"].get("parentRefs") or not all(p.get("name") for p in r["spec"]["parentRefs"]):
            failures.append(f"HTTPRoute {name} has no named Gateway parentRef")
    return failures


def check_cors(plugins: dict[str, dict]) -> list[str]:
    failures: list[str] = []
    cors = [p for p in plugins.values() if p.get("plugin") == "cors"]
    for p in cors:
        cfg = p.get("config", {})
        for header in ("Idempotency-Key", "If-Match"):
            if header not in cfg.get("headers", []):
                failures.append(f"cors plugin does not allow the {header} request header")
        for header in ("ETag", "Idempotency-Replayed", "Mcp-Session-Id"):
            if header not in cfg.get("exposed_headers", []):
                failures.append(f"cors plugin does not expose the {header} response header")
        if "*" in cfg.get("origins", []):
            failures.append("cors plugin allows origin '*'; use the exact origin")
    if not cors:
        failures.append("the enabled render contains no cors KongPlugin")
    return failures


def main() -> int:
    failures = check_defaults()
    docs = render("all-components-values.yaml", PLUGINS_ON)
    plugins = {d["metadata"]["name"]: d for d in docs if d.get("kind") == "KongPlugin"}
    routes = [d for d in docs if d.get("kind") == "HTTPRoute"]
    if not routes:
        failures.append("the all-components render has no HTTPRoute at all")
    if sorted(p.get("plugin") for p in plugins.values()) != ["cors", "opentelemetry", "prometheus"]:
        failures.append(f"the enabled render has KongPlugins {sorted(plugins)}, want the cors, prometheus and opentelemetry ones")
    failures += check_annotations(routes, plugins) + check_routes(routes) + check_cors(plugins)
    # Routes on, plugins off: no annotation may name a plugin that is not rendered.
    bare = render("all-components-values.yaml", [])
    for r in (d for d in bare if d.get("kind") == "HTTPRoute"):
        if (r.get("metadata", {}).get("annotations") or {}).get("konghq.com/plugins"):
            failures.append(f"HTTPRoute {r['metadata']['name']} names plugins while every kongPlugins block is off")
    if failures:
        print("FAIL: asrs-control chart Kong-config contract")
        for f in failures:
            print(f"  - {f}")
        return 1
    print(f"PASS: {len(routes)} HTTPRoutes checked (api/reports Kong-annotated, mcp/a2a on the Agent Router), {len(plugins)} KongPlugins, no auth plugin, defaults are a no-op")
    return 0


if __name__ == "__main__":
    sys.exit(main())
