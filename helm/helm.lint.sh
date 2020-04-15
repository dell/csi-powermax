#!/bin/sh
for version in csi-powermax/k8s*values.yaml; 
do echo $version; 
helm lint --values myvalues.yaml --values $version -n powermax ./csi-powermax
done
