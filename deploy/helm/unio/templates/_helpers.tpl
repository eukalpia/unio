{{/*
Chart name. It is at most 50 characters, so that the pod names, which add an
ordinal, stay within the 63-character DNS label limit.
*/}}
{{- define "unio.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 50 | trimSuffix "-" -}}
{{- end -}}

{{/*
Full name of the release. It names the StatefulSet, the services, the pods
and the Secret. The pod names are node identities, so the length is bounded.
*/}}
{{- define "unio.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 50 | trimSuffix "-" -}}
{{- else -}}
{{- $name := include "unio.name" . -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 50 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 50 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
Labels that select the pods of this release. Services and PVCs use them too.
*/}}
{{- define "unio.selectorLabels" -}}
app.kubernetes.io/name: {{ include "unio.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/*
Labels of every object of this release.
*/}}
{{- define "unio.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{ include "unio.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{/*
Container image, with the chart appVersion as the default tag.
*/}}
{{- define "unio.image" -}}
{{- printf "%s:%s" .Values.image.repository (default .Chart.AppVersion .Values.image.tag) -}}
{{- end -}}

{{/*
Name of the headless service that gives each pod its internode DNS name.
*/}}
{{- define "unio.headlessService" -}}
{{- printf "%s-hl" (include "unio.fullname" .) -}}
{{- end -}}

{{/*
Name of the Secret that holds the cluster secret, the root credentials and
the metrics token. The chart creates it unless auth.existingSecret is set.
*/}}
{{- define "unio.credentialsSecret" -}}
{{- if .Values.auth.existingSecret -}}
{{- .Values.auth.existingSecret -}}
{{- else -}}
{{- printf "%s-credentials" (include "unio.fullname" .) -}}
{{- end -}}
{{- end -}}

{{/*
Drive endpoints of the whole cluster, in the ellipsis form that the node
expands. There is one host per pod ordinal and one path per drive.
*/}}
{{- define "unio.endpoints" -}}
{{- $fn := include "unio.fullname" . -}}
{{- $hl := include "unio.headlessService" . -}}
{{- printf "https://%s-{0...%d}.%s.%s.svc.%s:9100/data{1...%d}" $fn (sub (int .Values.replicas) 1) $hl .Release.Namespace .Values.clusterDomain (int .Values.drivesPerNode) -}}
{{- end -}}

{{/*
Value of one credential in the chart-managed Secret. An existing value is kept
as it is, so that an upgrade never rotates it. A missing value gets a random
string of the length given in .length. The output is base64, as in Secret data.
The random source is cryptographic (Sprig randAlphaNum).
*/}}
{{- define "unio.credentialValue" -}}
{{- $v := "" -}}
{{- if hasKey .old .key -}}
{{- $v = index .old .key -}}
{{- end -}}
{{- if $v -}}
{{- $v -}}
{{- else -}}
{{- randAlphaNum (int .length) | b64enc -}}
{{- end -}}
{{- end -}}

{{/*
Checks that fail the install before any object is rendered. Each check states
the setting to change. The helper renders nothing.
*/}}
{{- define "unio.validate" -}}
{{- $fn := include "unio.fullname" . -}}
{{- if not (regexMatch "^[a-z]([-a-z0-9]*[a-z0-9])?$" $fn) -}}
{{- fail (printf "the release name %q gives the name %q, which is not a valid pod name: use lowercase letters, digits and hyphens, starting with a letter" .Release.Name $fn) -}}
{{- end -}}
{{- $total := mul (int .Values.replicas) (int .Values.drivesPerNode) -}}
{{- if gt $total 4096 -}}
{{- fail (printf "replicas (%d) times drivesPerNode (%d) is %d drives; the limit is 4096" (int .Values.replicas) (int .Values.drivesPerNode) $total) -}}
{{- end -}}
{{- if gt $total 1 -}}
{{- $ok := false -}}
{{- range $i := until 13 -}}
{{- if eq (mod $total (add $i 4)) 0 -}}
{{- $ok = true -}}
{{- end -}}
{{- end -}}
{{- if not $ok -}}
{{- fail (printf "%d drives (replicas %d times drivesPerNode %d) cannot form erasure sets: the total must be 1 or have a divisor from 4 to 16" $total (int .Values.replicas) (int .Values.drivesPerNode)) -}}
{{- end -}}
{{- end -}}
{{- $set := int .Values.config.erasure.setDriveCount -}}
{{- if gt $set 0 -}}
{{- if not (or (and (eq $total 1) (eq $set 1)) (and (ge $set 4) (le $set 16) (eq (mod $total $set) 0))) -}}
{{- fail (printf "config.erasure.setDriveCount %d does not divide the %d drives, or is outside 4 to 16" $set $total) -}}
{{- end -}}
{{- end -}}
{{- if and .Values.metrics.serviceMonitor.enabled (not .Values.metrics.token.enabled) -}}
{{- fail "metrics.serviceMonitor.enabled needs metrics.token.enabled: the metrics endpoint accepts scrapes only with its bearer token" -}}
{{- end -}}
{{- if and .Values.ingress.s3.enabled (not .Values.ingress.s3.hosts) -}}
{{- fail "ingress.s3.enabled needs at least one host in ingress.s3.hosts" -}}
{{- end -}}
{{- if and .Values.ingress.console.enabled (not .Values.ingress.console.hosts) -}}
{{- fail "ingress.console.enabled needs at least one host in ingress.console.hosts" -}}
{{- end -}}
{{- end -}}
