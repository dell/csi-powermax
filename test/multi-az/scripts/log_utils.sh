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
# Copyright © 2025 Dell Inc. or its subsidiaries. All Rights Reserved.
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

SCREEN_W=${SCREEN_W:-120}
NL=$'\n'

print_box()
{
    local SIDE_W=1
    local SIDE_CH='|'
    local PAD_W=2
    local PAD_CH=' '
    local HEADER_CH='-'

    fp_print_multiline "$1"
}

print_msg()
{
    local SIDE_W=3
    local SIDE_CH='-'
    local PAD_W=2
    local PAD_CH=' '
    local HEADER_CH=''

    fp_print_multiline "$1"
}

print_err()
{
    local SIDE_W=1
    local SIDE_CH='#'
    local PAD_W=2
    local PAD_CH=' '
    local HEADER_CH='#'

    fp_print_multiline "$1" >&2
}

print_on_err()
{
    local RC=$?
    [ $RC -ne 0 ] && print_err "$1 (rc=$RC)"
    return $RC
}

exit_on_err()
{
    print_on_err "$1" || exit $?
}


fp_gen_string()
{
    printf -- "$1%.0s" $(seq 1 $2)
}

fp_print_line()
{
    local MSG_LINE="$1"
    local MSG_W=$(echo -n "$MSG_LINE" | wc -m)

    if [ "$2" == "c" ]; then
        local LPAD_W=$(( ($SCREEN_W-$MSG_W-$SIDE_W*2)/2 ))
        local RPAD_W=$(( $SCREEN_W-$MSG_W-$SIDE_W*2-$LPAD_W ))
    else
        local LPAD_W=$PAD_W
        local RPAD_W=$(( $SCREEN_W-$MSG_W-$SIDE_W*2-$PAD_W ))
    fi

    local SIDE=$(fp_gen_string "$SIDE_CH" $SIDE_W)
    local LPAD=$(fp_gen_string "$PAD_CH" $LPAD_W)
    local RPAD=$(fp_gen_string "$PAD_CH" $RPAD_W)

    echo "${SIDE}${LPAD}${MSG_LINE}${RPAD}${SIDE}"
}

fp_print_multiline()
{
    local MSG="$1"
    local MAX_MSG_W=$(( $SCREEN_W-($SIDE_W+$PAD_W)*2 ))
    local HEADER=""

    [ -n "$HEADER_CH" ] && HEADER=$(fp_gen_string "$HEADER_CH" $SCREEN_W)

    [ -n "$HEADER" ] && echo "$HEADER"

    local MSG_LINES=$(echo "$MSG" | fold -s -w$MAX_MSG_W)
    echo "$MSG_LINES" | while IFS= read ML; do
        fp_print_line "$ML"
    done

    [ -n "$HEADER" ] && echo "$HEADER" || true
}

: '
print_box "\
First line
Final line"

print_box "First line
Final line"

print_box "First line${NL}Final line"

print_msg "Below is a long message:${NL}Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the License. You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0 Unless required by applicable law ..."
'
