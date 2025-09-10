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

#!/usr/bin/env sh

config="$1"
template="$2"
destination="$3"

cp "$template" "$destination"

while IFS= read -r line || [ -n "$line" ]; do
    # Skip lines that begin with a # or are empty
    [[ $line == \#* || -z $line ]] && continue

    setting="$( echo "$line" | cut -d '=' -f 1 )"
    value="$( echo "$line" | cut -d '=' -f 2- )"

    sed -i -e "s;${setting};${value};g" "$destination"
done < "$config"