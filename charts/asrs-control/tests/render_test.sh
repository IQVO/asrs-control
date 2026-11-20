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
echo "render_test: PASS"
