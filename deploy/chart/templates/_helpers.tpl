{{/*
Expand the name of the chart.
*/}}
{{- define "radosgw-exporter.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "radosgw-exporter.fullname" -}}
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

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "radosgw-exporter.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Common labels
*/}}
{{- define "radosgw-exporter.labels" -}}
helm.sh/chart: {{ include "radosgw-exporter.chart" . }}
{{ include "radosgw-exporter.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{/*
Selector labels
*/}}
{{- define "radosgw-exporter.selectorLabels" -}}
app.kubernetes.io/name: {{ include "radosgw-exporter.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/*
Create the name of the service account to use
*/}}
{{- define "radosgw-exporter.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "radosgw-exporter.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{/*
Get the secret name to use for credentials.
*/}}
{{- define "radosgw-exporter.secretName" -}}
{{- if .Values.exporter.credentials.existingSecret.name -}}
{{- .Values.exporter.credentials.existingSecret.name -}}
{{- else if .Values.exporter.credentials.secretName -}}
{{- .Values.exporter.credentials.secretName -}}
{{- else -}}
{{- printf "%s-radosgw-credentials" .Release.Name -}}
{{- end -}}
{{- end -}}

{{/*
Get the access key key name.
*/}}
{{- define "radosgw-exporter.accessKeyKey" -}}
{{- if .Values.exporter.credentials.existingSecret.name -}}
{{- .Values.exporter.credentials.existingSecret.accessKeyKey | default "accessKey" -}}
{{- else -}}
accessKey
{{- end -}}
{{- end -}}

{{/*
Get the secret key key name.
*/}}
{{- define "radosgw-exporter.secretKeyKey" -}}
{{- if .Values.exporter.credentials.existingSecret.name -}}
{{- .Values.exporter.credentials.existingSecret.secretKeyKey | default "secretKey" -}}
{{- else -}}
secretKey
{{- end -}}
{{- end -}}

{{/*
Check if we should create a secret
*/}}
{{- define "radosgw-exporter.createSecret" -}}
{{- if and .Values.exporter.credentials.createSecret (not .Values.exporter.credentials.existingSecret.name) -}}
true
{{- else -}}
false
{{- end -}}
{{- end -}}

{{/*
Validate credentials configuration
*/}}
{{- define "radosgw-exporter.validateCredentials" -}}
{{- if not .Values.exporter.credentials.existingSecret.name -}}
  {{- if .Values.exporter.credentials.createSecret -}}
    {{- if and (not .Values.exporter.credentials.accessKey) (not .Values.exporter.credentials.secretKey) -}}
    {{/* Allow empty credentials for initial deployment - user can update secret later */}}
    {{- else if or (not .Values.exporter.credentials.accessKey) (not .Values.exporter.credentials.secretKey) -}}
      {{- fail "Both exporter.credentials.accessKey and exporter.credentials.secretKey must be specified when creating a secret" -}}
    {{- end -}}
  {{- else -}}
    {{- fail "Either specify exporter.credentials.existingSecret.name or set exporter.credentials.createSecret to true" -}}
  {{- end -}}
{{- end -}}
{{- end -}}