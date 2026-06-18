{{/* Chart base name (overridable). */}}
{{- define "promptcloak.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Fullname: release-scoped base for all resource names. If the release name
already contains the chart name (e.g. `helm install promptcloak ...`), it is
used as-is to avoid `promptcloak-promptcloak`.
*/}}
{{- define "promptcloak.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/* Common labels applied to every resource. */}}
{{- define "promptcloak.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: promptcloak
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
{{- end -}}

{{/* The ext_proc container image reference. */}}
{{- define "promptcloak.image" -}}
{{- printf "%s:%s" .Values.image.repository (.Values.image.tag | default .Chart.AppVersion) -}}
{{- end -}}

{{/* Component resource names. */}}
{{- define "promptcloak.extprocName" -}}{{ include "promptcloak.fullname" . }}-extproc{{- end -}}
{{- define "promptcloak.valkeyName" -}}{{ include "promptcloak.fullname" . }}-valkey{{- end -}}
{{- define "promptcloak.presidioName" -}}{{ include "promptcloak.fullname" . }}-presidio-analyzer{{- end -}}
{{- define "promptcloak.mockLlmName" -}}{{ include "promptcloak.fullname" . }}-mock-llm{{- end -}}
{{- define "promptcloak.gatewayName" -}}{{ include "promptcloak.fullname" . }}-gateway{{- end -}}

{{/* Resolved vault address (VALKEY_ADDR). Empty -> in-memory vault. */}}
{{- define "promptcloak.vaultAddr" -}}
{{- if .Values.valkey.enabled -}}
{{- printf "%s.%s.svc:6379" (include "promptcloak.valkeyName" .) .Release.Namespace -}}
{{- else -}}
{{- .Values.vault.addr -}}
{{- end -}}
{{- end -}}

{{/* Resolved Presidio analyzer URL. */}}
{{- define "promptcloak.presidioURL" -}}
{{- if .Values.presidio.url -}}
{{- .Values.presidio.url -}}
{{- else if .Values.presidio.enabled -}}
{{- printf "http://%s.%s.svc:3000" (include "promptcloak.presidioName" .) .Release.Namespace -}}
{{- end -}}
{{- end -}}
