{{/*
Expand the name of the chart.
*/}}
{{- define "cert-manager-webhook-porkbun.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "cert-manager-webhook-porkbun.fullname" -}}
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
{{- define "cert-manager-webhook-porkbun.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "cert-manager-webhook-porkbun.labels" -}}
helm.sh/chart: {{ include "cert-manager-webhook-porkbun.chart" . }}
{{ include "cert-manager-webhook-porkbun.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "cert-manager-webhook-porkbun.selectorLabels" -}}
app.kubernetes.io/name: {{ include "cert-manager-webhook-porkbun.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "cert-manager-webhook-porkbun.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "cert-manager-webhook-porkbun.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{- define "cert-manager-webhook-porkbun.selfSignedIssuer" -}}
{{ printf "%s-selfsign" (include "cert-manager-webhook-porkbun.fullname" .) }}
{{- end -}}

{{- define "cert-manager-webhook-porkbun.rootCAIssuer" -}}
{{ printf "%s-ca" (include "cert-manager-webhook-porkbun.fullname" .) }}
{{- end -}}

{{- define "cert-manager-webhook-porkbun.rootCACertificate" -}}
{{ printf "%s-ca" (include "cert-manager-webhook-porkbun.fullname" .) }}
{{- end -}}

{{- define "cert-manager-webhook-porkbun.servingCertificate" -}}
{{ printf "%s-webhook-tls" (include "cert-manager-webhook-porkbun.fullname" .) }}
{{- end -}}

{{/*
Fully qualified image reference. A digest, when set, takes precedence over the
tag so the deployment is pinned to exact bytes.
*/}}
{{- define "cert-manager-webhook-porkbun.image" -}}
{{- $tag := default .Chart.AppVersion .Values.image.tag -}}
{{- if .Values.image.digest -}}
{{- printf "%s@%s" .Values.image.repository .Values.image.digest -}}
{{- else -}}
{{- printf "%s:%s" .Values.image.repository $tag -}}
{{- end -}}
{{- end -}}
