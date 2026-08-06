{{/*
Expand the name of the chart.
*/}}
{{- define "ssm-annotation-resolver.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "ssm-annotation-resolver.fullname" -}}
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

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "ssm-annotation-resolver.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "ssm-annotation-resolver.labels" -}}
helm.sh/chart: {{ include "ssm-annotation-resolver.chart" . }}
{{ include "ssm-annotation-resolver.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "ssm-annotation-resolver.selectorLabels" -}}
app.kubernetes.io/name: {{ default (include "ssm-annotation-resolver.name" .) .name }}
app.kubernetes.io/instance: {{ default .Release.Name .instance }}
app.kubernetes.io/component: {{ default "controller-manager" .component }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "ssm-annotation-resolver.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "ssm-annotation-resolver.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}
