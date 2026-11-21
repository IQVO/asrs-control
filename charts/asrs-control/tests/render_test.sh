#!/usr/bin/env bash
# Chart render assertions. Run: bash charts/asrs-control/tests/render_test.sh (needs helm)
set -euo pipefail
chart_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fail() { echo "FAIL: $*" >&2; exit 1; }
vals=(--set database.url=postgres://x --set kafka.brokers=k:9092)
render() { helm template asrs-control "$chart_dir" "${vals[@]}" "$@"; }

deployment="$(render --show-only templates/deployment.yaml)"
configmap="$(render --show-only templates/configmap.yaml)"
cm_name="$(awk '/^  name:/ {print $2; exit}' <<<"$configmap")"
grep -A3 'envFrom:' <<<"$deployment" | grep -q "name: $cm_name" || fail "Deployment does not mount the ConfigMap via envFrom"
for v in ADDR LOG_LEVEL KAFKA_BROKERS MQTT_URL MQTT_CLIENT_ID ASRS_LAYOUT_FILE OUTBOX_INTERVAL_MS OUTBOX_BATCH OUTBOX_MAX_ATTEMPTS VELOCITY_WINDOW_HOURS VELOCITY_FAST_RETRIEVALS; do
  grep -q "^  $v: " <<<"$configmap" || fail "ConfigMap does not carry $v"
done
grep -q '^  layout.yaml: ' <<<"$configmap" || fail "ConfigMap does not carry layout.yaml"
grep -q 'id: ASRS1' <<<"$configmap" || fail "layout.yaml lost the default AS/RS"
grep -q 'DATABASE_URL' <<<"$configmap" && fail "the ConfigMap must not carry the DSN"
grep -A4 'name: DATABASE_URL$' <<<"$deployment" | grep -q secretKeyRef || fail "DATABASE_URL must come from a Secret"
grep -q 'replicas: 1$' <<<"$deployment" || fail "the Deployment must run exactly one replica"
grep -q 'type: Recreate' <<<"$deployment" || fail "the rollout must be Recreate (never two schedulers)"
grep -q 'readOnlyRootFilesystem: true' <<<"$deployment" || fail "root filesystem must be read-only"
grep -q 'runAsNonRoot: true' <<<"$deployment" || fail "the pod must run as non-root"
grep -q 'terminationGracePeriodSeconds: 30' <<<"$deployment" || fail "grace period must default to 30"
grep -q 'path: /readyz' <<<"$deployment" || fail "readiness probe must hit /readyz"
grep -q 'mountPath: /etc/asrs-control' <<<"$deployment" || fail "layout.yaml is not mounted at the layout file's directory"
grep -q '_MODE' <<<"$deployment$configmap" && fail "no *_MODE toggle may exist"
helm template asrs-control "$chart_dir" --set kafka.brokers=k:9092 >/dev/null 2>&1 && fail "renders without a database"
helm template asrs-control "$chart_dir" --set database.url=postgres://x >/dev/null 2>&1 && fail "renders without Kafka"
render --set replicaCount=2 >/dev/null 2>&1 && fail "renders two replicas"
render --set terminationGracePeriodSeconds=5 >/dev/null 2>&1 && fail "renders a grace period below the shutdown bound"
other="$(render --show-only templates/configmap.yaml --set mqtt.url=tcp://x:1 --set config.velocityFastRetrievals=5)"
grep -q 'MQTT_URL: "tcp://x:1"' <<<"$other" || fail "mqtt.url does not reach MQTT_URL"
grep -q 'VELOCITY_FAST_RETRIEVALS: "5"' <<<"$other" || fail "config.velocityFastRetrievals does not reach its variable"

# --- chart vs binary (helm-chart-env-contract) -------------------------------------------------
# Every env var cmd/api (and the telemetry adapter it wires) reads is rendered when every optional
# value is set: as an explicit Deployment env entry or as a key of the ConfigMap it mounts via
# envFrom. The names come from the getenv-style literals (getenv/Getenv/env and config.go's `or`
# and `atLeast` helpers), so a new variable fails here until the chart renders it.
repo_root="$(cd "$chart_dir/../.." && pwd)"
scan_go=()
for f in "$repo_root"/cmd/api/*.go "$repo_root"/internal/adapters/outbound/telemetry/*.go; do
  [[ "$f" == *_test.go ]] || scan_go+=("$f")
done
# shellcheck disable=SC2207
api_vars=($(grep -ohE '(^|[^A-Za-z])(Getenv|getenv|env|or|atLeast)\("[A-Z][A-Z0-9_]*"' "${scan_go[@]}" | sed -E 's/.*\("//; s/"$//' | sort -u))
(( ${#api_vars[@]} >= 15 )) || fail "found only ${#api_vars[@]} env names in cmd/api and the telemetry adapter; the scan is looking in the wrong place"
everything=(--set otel.enabled=true --set otel.endpoint=otel-collector:4317 --set otel.serviceName=asrs-control-x
  --set environment=staging --set serviceVersion=v9.9.9 --set database.migrationsExistingSecretKey=MIGRATIONS_DATABASE_URL)
full_deployment="$(render "${everything[@]}" --show-only templates/deployment.yaml)"
full_configmap="$(render "${everything[@]}" --show-only templates/configmap.yaml)"
for var in "${api_vars[@]}"; do
  grep -q "name: $var\$" <<<"$full_deployment" || grep -q "^  $var: " <<<"$full_configmap" \
    || fail "the api Deployment does not render $var, which cmd/api reads"
done
api_known=" ${api_vars[*]} "
while read -r key; do
  [[ "$api_known" == *" $key "* ]] || fail "ConfigMap key $key is read by no binary (dead key)"
done < <(awk '/^data:/ {d=1; next} d && /^  [A-Z]/ {sub(/:.*/, ""); sub(/^  /, ""); print}' <<<"$full_configmap")

# --- observability (ADR 0004) ------------------------------------------------------------------
env_value() { grep -A1 "name: $2\$" <<<"$1" | grep -m1 'value:' | sed -E 's/.*value: "?//; s/"$//'; }
[[ "$(env_value "$deployment" OTEL_EXPORTER_OTLP_ENDPOINT)" == "otel-collector.observability.svc.cluster.local:4317" ]] \
  || fail "OTEL_EXPORTER_OTLP_ENDPOINT must default to the fleet Collector"
[[ "$(env_value "$deployment" OTEL_SERVICE_NAME)" == "asrs-control" ]] || fail "OTEL_SERVICE_NAME must default to asrs-control"
[[ "$(env_value "$deployment" ENVIRONMENT)" == "production" ]] || fail "ENVIRONMENT must come from environment"
[[ "$(env_value "$deployment" SERVICE_VERSION)" == "local" ]] || fail "SERVICE_VERSION must default to image.tag"
[[ "$(env_value "$full_deployment" SERVICE_VERSION)" == "v9.9.9" ]] || fail "serviceVersion must override SERVICE_VERSION"
[[ "$(env_value "$full_deployment" OTEL_SERVICE_NAME)" == "asrs-control-x" ]] || fail "OTEL_SERVICE_NAME must follow otel.serviceName"
grep -q 'OTEL_EXPORTER_OTLP_ENDPOINT: "otel-collector:4317"' <<<"$full_configmap" || fail "the ConfigMap does not mirror otel.endpoint"
off="$(render --set otel.enabled=false)"
grep -q 'OTEL_EXPORTER_OTLP_ENDPOINT\|OTEL_SERVICE_NAME' <<<"$off" && fail "otel.enabled=false must render no OTLP endpoint (telemetry off)"
for name in OTEL_EXPORTER_OTLP_ENDPOINT OTEL_SERVICE_NAME ENVIRONMENT SERVICE_VERSION; do
  render --set "extraEnv[0].name=$name" --set "extraEnv[0].value=x" >/dev/null 2>&1 && fail "extraEnv may set $name, which the chart renders"
done

# --- read-only agent components (ADR 0005) ----------------------------------------------------
# Off by default; enabled, each renders a Deployment (command /app/<binary>) and a Service, and
# every env var its binary reads (the same literal scan as cmd/api) is an explicit env entry.
default_all="$(render)"
grep -q 'component: mcp\|component: a2a' <<<"$default_all" && fail "mcp and a2a must be off by default"
agents="$(render --set mcp.enabled=true --set a2a.enabled=true --set database.migrationsExistingSecretKey=MIGRATIONS_DATABASE_URL)"
for comp in mcp a2a; do
  dep="$(render --set mcp.enabled=true --set a2a.enabled=true --set database.migrationsExistingSecretKey=MIGRATIONS_DATABASE_URL \
    --show-only "templates/$comp-deployment.yaml")"
  grep -q "command: \[\"/app/$comp\"\]" <<<"$dep" || fail "the $comp Deployment does not run /app/$comp"
  grep -q 'envFrom' <<<"$dep" && fail "the $comp Deployment must not mount the api ConfigMap as env"
  grep -q 'path: /healthz' <<<"$dep" || fail "the $comp probes must hit /healthz"
  grep -q 'mountPath: /etc/asrs-control' <<<"$dep" || fail "the $comp Deployment does not mount layout.yaml"
  # shellcheck disable=SC2207
  comp_vars=($(grep -ohE '(^|[^A-Za-z])(Getenv|getenv|env|or)\("[A-Z][A-Z0-9_]*"' $(ls "$repo_root/cmd/$comp"/*.go | grep -v _test.go) \
    "$repo_root"/internal/adapters/outbound/telemetry/telemetry.go | sed -E 's/.*\("//; s/"$//' | sort -u))
  (( ${#comp_vars[@]} >= 8 )) || fail "found only ${#comp_vars[@]} env names in cmd/$comp; the scan is looking in the wrong place"
  for var in "${comp_vars[@]}"; do
    grep -q "name: $var\$" <<<"$dep" || fail "the $comp Deployment does not render $var, which cmd/$comp reads"
  done
  for var in $(grep -ohE '"[A-Z][A-Z0-9_]{2,} is required' "$repo_root/cmd/$comp"/main.go | tr -d '"' | sed 's/ is required//' | sort -u); do
    grep -q "name: $var\$" <<<"$dep" || fail "the $comp Deployment does not render $var, which cmd/$comp requires"
  done
  [[ "$(env_value "$dep" OTEL_SERVICE_NAME)" == "asrs-control-$comp" ]] || fail "OTEL_SERVICE_NAME of $comp must be asrs-control-$comp"
  grep -q "name: asrs-control-$comp\$" <<<"$agents" || fail "no asrs-control-$comp Service/Deployment name"
done
[[ "$(env_value "$agents" MCP_ADDR)" == ":8090" ]] || fail "MCP_ADDR must default to :8090"
[[ "$(env_value "$agents" A2A_ADDR)" == ":8093" ]] || fail "A2A_ADDR must default to :8093"
agent_url="$(env_value "$agents" A2A_AGENT_URL || true)"
[[ "$agent_url" =~ ^http://asrs-control-a2a\.[a-z0-9-]+\.svc\.cluster\.local:8093/$ ]] || fail "A2A_AGENT_URL must default to the a2a Service DNS name, got '$agent_url'"
custom="$(render --set a2a.enabled=true --set a2a.agentUrl=http://a2a.example.invalid/ --show-only templates/a2a-deployment.yaml)"
[[ "$(env_value "$custom" A2A_AGENT_URL)" == "http://a2a.example.invalid/" ]] || fail "a2a.agentUrl must override A2A_AGENT_URL"
render --set mcp.enabled=true --set "mcp.extraEnv[0].name=OTEL_SERVICE_NAME" --set "mcp.extraEnv[0].value=x" >/dev/null 2>&1 \
  && fail "mcp.extraEnv may set OTEL_SERVICE_NAME, which the chart renders"
echo "render_test: PASS"
