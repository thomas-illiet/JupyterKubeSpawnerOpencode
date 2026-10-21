{{- define "jko.name" -}}jupyter-kubeopencode{{- end -}}
{{- define "jko.labels" -}}
app.kubernetes.io/name: {{ include "jko.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

