#!/bin/sh
set -eu
mkdir -p outputs
printf 'CPU example started\n'
printf 'result=42\n' >outputs/result.txt
printf 'CPU example finished: outputs/result.txt\n'
