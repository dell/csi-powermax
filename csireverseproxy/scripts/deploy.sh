#!/usr/bin/env bash
echo "kubectl create configmap revproxy-config --from-file deploy/ -n powermax"
kubectl create configmap powermax-reverseproxy-config --from-file deploy/ -n powermax
echo "kubectl create -f deploy/revproxy.yaml"
kubectl create -f manifests/revproxy.yaml
