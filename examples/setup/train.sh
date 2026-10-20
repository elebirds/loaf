#!/bin/sh
set -eu
test "$(cat scratch/setup-ready)" = ready
mkdir -p outputs
printf 'Experiment: %s\n' "$EXPERIMENT_NAME"
printf '%s\n' "$EXPERIMENT_NAME" >outputs/experiment.txt
