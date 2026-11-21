{{- define "asrs-control.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "asrs-control.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/* The read-only agent components (ADR 0005): <fullname>-mcp and <fullname>-a2a. */}}
{{- define "asrs-control.mcpFullname" -}}
{{- printf "%s-mcp" (include "asrs-control.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "asrs-control.a2aFullname" -}}
{{- printf "%s-a2a" (include "asrs-control.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
The KongPlugins this chart renders, comma-joined for the konghq.com/plugins annotation of every HTTPRoute; empty
while every kongPlugins block is off, and the routes then carry no annotation.
*/}}
{{- define "asrs-control.kongPluginNames" -}}
{{- $names := list -}}
{{- range $name, $cfg := .Values.kongPlugins -}}
{{- if $cfg.enabled -}}
{{- $names = append $names (printf "%s-%s" (include "asrs-control.fullname" $) $name) -}}
{{- end -}}
{{- end -}}
{{- join "," $names -}}
{{- end -}}

{{- define "asrs-control.selectorLabels" -}}
app.kubernetes.io/name: {{ include "asrs-control.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "asrs-control.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{ include "asrs-control.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "asrs-control.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "asrs-control.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{- define "asrs-control.image" -}}
{{- printf "%s:%s" .Values.image.repository (.Values.image.tag | default .Chart.AppVersion) }}
{{- end }}

{{/* service.version: serviceVersion, else the image tag, else appVersion. */}}
{{- define "asrs-control.serviceVersion" -}}
{{- .Values.serviceVersion | default .Values.image.tag | default .Chart.AppVersion }}
{{- end }}

{{- define "asrs-control.databaseSecretName" -}}
{{- default (printf "%s-db" (include "asrs-control.fullname" .)) .Values.database.existingSecret }}
{{- end }}

{{/* Mandatory values: the service runs on real Postgres, Kafka and MQTT only, as one replica. */}}
{{- define "asrs-control.require" -}}
{{- if and (not .Values.database.url) (not .Values.database.existingSecret) }}
{{- fail "database.url or database.existingSecret is required: asrs-control runs on Postgres only" }}
{{- end }}
{{- if not .Values.kafka.brokers }}
{{- fail "kafka.brokers is required: the outbox relay publishes to Kafka" }}
{{- end }}
{{- if not .Values.mqtt.url }}
{{- fail "mqtt.url is required: the PLC link speaks MQTT" }}
{{- end }}
{{- if ne (int .Values.replicaCount) 1 }}
{{- fail "replicaCount must be 1: asrs-control is the single scheduler of each AS/RS (ADR 0002)" }}
{{- end }}
{{- range concat .Values.extraEnv .Values.mcp.extraEnv .Values.a2a.extraEnv }}
{{- if has .name (list "ENVIRONMENT" "SERVICE_VERSION" "OTEL_EXPORTER_OTLP_ENDPOINT" "OTEL_SERVICE_NAME") }}
{{- fail (printf "extraEnv (api, mcp or a2a) must not set %s: the chart renders it from otel.*, environment or serviceVersion" .name) }}
{{- end }}
{{- end }}
{{- if lt (int .Values.terminationGracePeriodSeconds) 15 }}
{{- fail "terminationGracePeriodSeconds must be at least 15 (10 s HTTP drain plus the relay and MQTT shutdown)" }}
{{- end }}
{{- end }}
