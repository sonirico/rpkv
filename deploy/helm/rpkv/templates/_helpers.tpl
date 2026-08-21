{{/*
Expand the name of the chart, truncated to fit Kubernetes name limits.
*/}}
{{- define "rpkv.fullname" -}}
{{- if contains .Chart.Name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name .Chart.Name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}

{{/*
Common labels.
*/}}
{{- define "rpkv.labels" -}}
app.kubernetes.io/name: {{ .Chart.Name }}
app.kubernetes.io/instance: {{ .Release.Name }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels.
*/}}
{{- define "rpkv.selectorLabels" -}}
app.kubernetes.io/name: {{ .Chart.Name }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Comma-separated shard addresses, one per shards.replicas: each StatefulSet
pod's stable DNS name against the shard headless service.
*/}}
{{- define "rpkv.shardsList" -}}
{{- $fullname := include "rpkv.fullname" . -}}
{{- $port := .Values.service.port -}}
{{- $shards := list -}}
{{- range $i := until (int .Values.shards.replicas) }}
{{- $shards = append $shards (printf "%s-shard-%d.%s-shard:%v" $fullname $i $fullname $port) }}
{{- end }}
{{- join "," $shards -}}
{{- end }}
