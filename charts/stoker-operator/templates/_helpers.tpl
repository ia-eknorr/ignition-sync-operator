{{/*
Expand the name of the chart.
*/}}
{{- define "stoker-operator.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "stoker-operator.fullname" -}}
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
{{- define "stoker-operator.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels.
*/}}
{{- define "stoker-operator.labels" -}}
helm.sh/chart: {{ include "stoker-operator.chart" . }}
{{ include "stoker-operator.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels.
*/}}
{{- define "stoker-operator.selectorLabels" -}}
app.kubernetes.io/name: {{ include "stoker-operator.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
control-plane: controller-manager
{{- end }}

{{/*
ServiceAccount name.
*/}}
{{- define "stoker-operator.serviceAccountName" -}}
{{- include "stoker-operator.fullname" . }}-controller-manager
{{- end }}

{{/*
Container image.
*/}}
{{- define "stoker-operator.image" -}}
{{- $tag := default .Chart.AppVersion .Values.image.tag -}}
{{ .Values.image.repository }}:{{ $tag }}
{{- end }}
