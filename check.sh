#!/bin/bash
# Copyright © 2020 Dell Inc. or its subsidiaries. All Rights Reserved.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#      http://www.apache.org/licenses/LICENSE-2.0
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
#
REVPROXY=false
if [ "$1" = "--all" ]; then
  REVPROXY=true
fi
if [ -f "../vendor" ]; then
    # Tell the applicable Go tools to use the vendor directory, if it exists.
    MOD_FLAGS="-mod=vendor"
fi
FMT_TMPFILE=/tmp/check_fmt
FMT_COUNT_TMPFILE=${FMT_TMPFILE}.count

fmt_count() {
    if [ ! -f $FMT_COUNT_TMPFILE ]; then
        echo "0"
    fi

    head -1 $FMT_COUNT_TMPFILE
}

fmt() {
    if [ "$REVPROXY" = true ] ; then
      gofmt -d ./service ./csireverseproxy ./k8sutils ./pkg | tee $FMT_TMPFILE
    else
      gofmt -d ./service ./k8sutils ./pkg| tee $FMT_TMPFILE
    fi
    cat $FMT_TMPFILE | wc -l > $FMT_COUNT_TMPFILE
    if [ ! `cat $FMT_COUNT_TMPFILE` -eq "0" ]; then
        echo Found `cat $FMT_COUNT_TMPFILE` formatting issue\(s\).
        return 1
    fi
}

echo === Checking format...
fmt
FMT_RETURN_CODE=$?
echo === Finished

echo === Vetting csi-powermax
CGO_ENABLED=0 go vet ${MOD_FLAGS} ./service/... ./k8sutils/... ./pkg/...
VET_RETURN_CODE=$?
echo === Finished

if [ "$REVPROXY" = true ] ; then
  echo === Vetting csireverseproxy
  (cd csireverseproxy && CGO_ENABLED=0 go vet ${MOD_FLAGS} ./...)
  VET_CSIREVPROXY_RETURN_CODE=$?
fi

echo === Linting...
if [ -z ${GOLINT+x} ]; then
  if [ "$REVPROXY" = true ] ; then
    (command -v golint >/dev/null 2>&1 \
        || GO111MODULE=on go install golang.org/x/lint/golint@latest) \
        && golint --set_exit_status ./service/... ./csireverseproxy/... ./pkg/...
  else
    (command -v golint >/dev/null 2>&1 \
        || GO111MODULE=on go install golang.org/x/lint/golint@latest) \
        && golint --set_exit_status ./service/... ./pkg/...
  fi
else
  if [ "$REVPROXY" = true ] ; then
    $GOLINT --set_exit_status ./service/... ./csireverseproxy/...
  else
    $GOLINT --set_exit_status ./service/... ./pkg/...
  fi
fi
LINT_RETURN_CODE=$?
echo === Finished

# Report output.
fail_checks=0
[ "${FMT_RETURN_CODE}" != "0" ] && echo "Formatting checks failed! => Run 'make format'." && fail_checks=1
[ "${VET_RETURN_CODE}" != "0" ] && echo "Vetting checks failed!" && fail_checks=1
if [ "$REVPROXY" = true ] ; then
  [ "${VET_CSIREVPROXY_RETURN_CODE}" != "0" ] && echo "Vetting checks on csireverseproxy failed!" && fail_checks=1
fi
[ "${LINT_RETURN_CODE}" != "0" ] && echo "Linting checks failed!" && fail_checks=1

exit ${fail_checks}
