#!/bin/bash

# Copyright © 2020-2025 Dell Inc. or its subsidiaries. All Rights Reserved.
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
