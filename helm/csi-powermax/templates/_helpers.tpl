{{- define "csi-powermax.isStorageCapacitySupported" -}}
{{- if eq .Values.storageCapacity.enabled true -}}
  {{- if and (eq .Capabilities.KubeVersion.Major "1") (ge (trimSuffix "+" .Capabilities.KubeVersion.Minor) "25") -}}
      {{- true -}}
  {{- end -}}
{{- end -}}
{{- end -}}
