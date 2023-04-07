{{/*
Return the appropriate sidecar images based on k8s version
*/}}
{{- define "csi-powermax.attacherImage" -}}
  {{- if eq .Capabilities.KubeVersion.Major "1" }}
    {{- if and (ge (trimSuffix "+" .Capabilities.KubeVersion.Minor) "23") (le (trimSuffix "+" .Capabilities.KubeVersion.Minor) "26") -}}
      {{- print "registry.k8s.io/sig-storage/csi-attacher:v4.2.0" -}}
    {{- end -}}
  {{- end -}}
{{- end -}}

{{- define "csi-powermax.provisionerImage" -}}
  {{- if eq .Capabilities.KubeVersion.Major "1" }}
    {{- if and (ge (trimSuffix "+" .Capabilities.KubeVersion.Minor) "23") (le (trimSuffix "+" .Capabilities.KubeVersion.Minor) "26") -}}
      {{- print "registry.k8s.io/sig-storage/csi-provisioner:v3.4.0" -}}
    {{- end -}}
  {{- end -}}
{{- end -}}

{{- define "csi-powermax.snapshotterImage" -}}
  {{- if eq .Capabilities.KubeVersion.Major "1" }}
    {{- if and (ge (trimSuffix "+" .Capabilities.KubeVersion.Minor) "23") (le (trimSuffix "+" .Capabilities.KubeVersion.Minor) "26") -}}
      {{- print "registry.k8s.io/sig-storage/csi-snapshotter:v6.2.1" -}}
    {{- end -}}
  {{- end -}}
{{- end -}}

{{- define "csi-powermax.resizerImage" -}}
  {{- if eq .Capabilities.KubeVersion.Major "1" }}
    {{- if and (ge (trimSuffix "+" .Capabilities.KubeVersion.Minor) "23") (le (trimSuffix "+" .Capabilities.KubeVersion.Minor) "26") -}}
      {{- print "registry.k8s.io/sig-storage/csi-resizer:v1.7.0" -}}
    {{- end -}}
  {{- end -}}
{{- end -}}

{{- define "csi-powermax.registrarImage" -}}
  {{- if eq .Capabilities.KubeVersion.Major "1" }}
    {{- if and (ge (trimSuffix "+" .Capabilities.KubeVersion.Minor) "23") (le (trimSuffix "+" .Capabilities.KubeVersion.Minor) "26") -}}
      {{- print "registry.k8s.io/sig-storage/csi-node-driver-registrar:v2.6.3" -}}
    {{- end -}}
  {{- end -}}
{{- end -}}

{{- define "csi-powermax.healthmonitorImage" -}}
  {{- if eq .Capabilities.KubeVersion.Major "1" }}
    {{- if and (ge (trimSuffix "+" .Capabilities.KubeVersion.Minor) "23") (le (trimSuffix "+" .Capabilities.KubeVersion.Minor) "26") -}}
      {{- print "gcr.io/k8s-staging-sig-storage/csi-external-health-monitor-controller:v0.8.0" -}}
    {{- end -}}
  {{- end -}}
{{- end -}}
