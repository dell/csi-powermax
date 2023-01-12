{{/*
Return the appropriate sidecar images based on k8s version
*/}}
{{- define "csi-powermax.attacherImage" -}}
  {{- if eq .Capabilities.KubeVersion.Major "1" }}
    {{- if and (ge (trimSuffix "+" .Capabilities.KubeVersion.Minor) "23") (le (trimSuffix "+" .Capabilities.KubeVersion.Minor) "26") -}}
      {{- print "k8s.gcr.io/sig-storage/csi-attacher:v4.0.0" -}}
    {{- end -}}
  {{- end -}}
{{- end -}}

{{- define "csi-powermax.provisionerImage" -}}
  {{- if eq .Capabilities.KubeVersion.Major "1" }}
    {{- if and (ge (trimSuffix "+" .Capabilities.KubeVersion.Minor) "23") (le (trimSuffix "+" .Capabilities.KubeVersion.Minor) "26") -}}
      {{- print "k8s.gcr.io/sig-storage/csi-provisioner:v3.3.0" -}}
    {{- end -}}
  {{- end -}}
{{- end -}}

{{- define "csi-powermax.snapshotterImage" -}}
  {{- if eq .Capabilities.KubeVersion.Major "1" }}
    {{- if and (ge (trimSuffix "+" .Capabilities.KubeVersion.Minor) "23") (le (trimSuffix "+" .Capabilities.KubeVersion.Minor) "26") -}}
      {{- print "k8s.gcr.io/sig-storage/csi-snapshotter:v6.1.0" -}}
    {{- end -}}
  {{- end -}}
{{- end -}}

{{- define "csi-powermax.resizerImage" -}}
  {{- if eq .Capabilities.KubeVersion.Major "1" }}
    {{- if and (ge (trimSuffix "+" .Capabilities.KubeVersion.Minor) "23") (le (trimSuffix "+" .Capabilities.KubeVersion.Minor) "26") -}}
      {{- print "k8s.gcr.io/sig-storage/csi-resizer:v1.6.0" -}}
    {{- end -}}
  {{- end -}}
{{- end -}}

{{- define "csi-powermax.registrarImage" -}}
  {{- if eq .Capabilities.KubeVersion.Major "1" }}
    {{- if and (ge (trimSuffix "+" .Capabilities.KubeVersion.Minor) "23") (le (trimSuffix "+" .Capabilities.KubeVersion.Minor) "26") -}}
      {{- print "k8s.gcr.io/sig-storage/csi-node-driver-registrar:v2.6.0" -}}
    {{- end -}}
  {{- end -}}
{{- end -}}

{{- define "csi-powermax.healthmonitorImage" -}}
  {{- if eq .Capabilities.KubeVersion.Major "1" }}
    {{- if and (ge (trimSuffix "+" .Capabilities.KubeVersion.Minor) "23") (le (trimSuffix "+" .Capabilities.KubeVersion.Minor) "26") -}}
      {{- print "gcr.io/k8s-staging-sig-storage/csi-external-health-monitor-controller:v0.7.0" -}}
    {{- end -}}
  {{- end -}}
{{- end -}}
