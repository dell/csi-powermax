#!/usr/bin/env sh

config="$1"
template="$2"
destination="$3"

cp "$template" "$destination"

while read line; do
    setting="$( echo "$line" | cut -d '=' -f 1 )"
    value="$( echo "$line" | cut -d '=' -f 2- )"

    sed -i -e "s;${setting};${value};g" "$destination"
done < "$config"