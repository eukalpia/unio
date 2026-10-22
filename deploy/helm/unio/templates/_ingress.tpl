{{/*
Ingress for one endpoint of the node, in networking.k8s.io/v1. The arguments
are a dict: root (the chart context), suffix (appended to the release name),
path (the URL path to route) and ing (the values of the Ingress). Every route
goes to the S3 service, which serves the S3 API and the console on one port.
*/}}
{{- define "unio.ingress" -}}
{{- $root := .root -}}
{{- $ing := .ing -}}
{{- $path := .path -}}
{{- $name := printf "%s%s" (include "unio.fullname" $root) .suffix -}}
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: {{ $name }}
  labels:
    {{- include "unio.labels" $root | nindent 4 }}
  {{- with $ing.annotations }}
  annotations:
    {{- toYaml . | nindent 4 }}
  {{- end }}
spec:
  {{- with $ing.className }}
  ingressClassName: {{ . }}
  {{- end }}
  rules:
    {{- range $host := $ing.hosts }}
    - host: {{ $host | quote }}
      http:
        paths:
          - path: {{ $path }}
            pathType: Prefix
            backend:
              service:
                name: {{ include "unio.fullname" $root }}
                port:
                  name: s3
    {{- end }}
  {{- with $ing.tls }}
  tls:
    {{- toYaml . | nindent 4 }}
  {{- end }}
{{- end -}}
