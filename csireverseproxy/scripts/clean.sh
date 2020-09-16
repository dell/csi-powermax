#!/usr/bin/env bash
kubectl delete configmap powermax-reverseproxy-config -n powermax
kubectl delete -f manifests/revproxy.yaml
