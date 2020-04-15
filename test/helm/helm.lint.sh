#!/bin/sh
FILES="10block 10vols 1bigvol 1vol 2block 2vols 7vols block xfspre"

for i in $FILES;
do
helm lint -n test $i
done
