{{/*
Chart name.
*/}}
{{- define "anomaly-detection.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Fullname: release-name + chart name.
*/}}
{{- define "anomaly-detection.fullname" -}}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- printf "%s" $name | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels applied to all resources.
*/}}
{{- define "anomaly-detection.labels" -}}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: staffops-anomaly-detection
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version | replace "+" "_" }}
{{- end }}

{{/*
Pod labels — mandatory app labels + runtime Environment.
Corp cost tags (CostCenter/CostProject/CostScope) are NOT set here: this repo is
org-neutral. They are injected at deploy time by the corp overlay/ApplicationSet.
*/}}
{{- define "anomaly-detection.podLabels" -}}
app.kubernetes.io/name: {{ .component }}
app.kubernetes.io/version: {{ .tag | default $.Chart.AppVersion | quote }}
Environment: {{ .global.environment | quote }}
{{- end }}

{{/*
Security context — hardened for Kyverno (PH.1).
runAsNonRoot, readOnlyRootFilesystem, no privilege escalation, drop ALL caps.
*/}}
{{- define "anomaly-detection.securityContext" -}}
runAsNonRoot: true
runAsUser: 65534
readOnlyRootFilesystem: true
allowPrivilegeEscalation: false
capabilities:
  drop:
    - ALL
{{- end }}

{{/*
Pod security context (pod-level).
*/}}
{{- define "anomaly-detection.podSecurityContext" -}}
runAsNonRoot: true
runAsUser: 65534
fsGroup: 65534
{{- end }}

{{/*
Lifecycle preStop + terminationGracePeriodSeconds (PH.7).
*/}}
{{- define "anomaly-detection.lifecycle" -}}
lifecycle:
  preStop:
    exec:
      command: ["sh", "-c", "sleep 5"]
{{- end }}
