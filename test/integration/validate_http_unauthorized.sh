#!/bin/bash
source ../../env.sh
rm -rf unix_sock
nonhttp=$(echo $X_CSI_POWERMAX_ENDPOINT | sed 's/https:/http:/')
echo "testing http validation with URL: " $nonhttp
export X_CSI_POWERMAX_ENDPOINT=$nonhttp

../../csi-powermax 2>stderr 
grep "Unauthorized" stderr
rc=$?
echo rc $rc
if [ $rc -ne 0 ]; then echo "failed..."; else echo "passed"; fi
exit $rc
