#!/usr/bin/env bash
# Runs every load step for one system on a freshly reset and seeded stack.
#
#   scripts/matrix.sh torment
#   scripts/matrix.sh formance default|minimal
#
# Env: DURATION (default 5m), USERS (default 10000), OUT (default results/<date>). With SUT_SSH (an ssh
# destination such as ubuntu@10.0.0.5) the stack runs on that host from ~/TorBench, without TigerBeetle's
# --development flag and with an 8 GiB TigerBeetle grid cache, and URL must point at it.
set -euo pipefail
cd "$(dirname "$0")/.."

system=$1
features=${2:-default}
duration=${DURATION:-5m}
users=${USERS:-10000}
out=${OUT:-results/$(date +%Y%m%d)}
label=$system
[ "$system" = formance ] && label=formance-$features

mkdir -p "$out" bin
go build -o bin/torbench ./cmd/torbench
tb() { bin/torbench "$1" -system "$system" -features "$features" -users "$users" ${URL:+-url "$URL"} "${@:2}"; }
# a failed correctness check is recorded in the result file, so the matrix carries on
step() { tb run -duration "$duration" "$@" || echo "step failed: $*" >&2; }

if [ -n "${SUT_SSH:-}" ]; then
  ssh "$SUT_SSH" "cd TorBench && TB_DEV_FLAG= TB_CACHE_GRID=8GiB make fresh SYSTEM=$system"
else
  make fresh SYSTEM="$system"
fi
tb seed

# contention levels: shards, zipf
for level in "1 1.1" "16 1.1" "256 0"; do
  read -r shards zipf <<<"$level"
  name=$out/$label-s$shards-z$zipf
  for c in 16 64 256; do
    step -shards "$shards" -zipf "$zipf" -concurrency "$c" -out "$name-c$c.json"
  done
  # open loop at fractions of the best requests/s any closed loop reached
  capacity=$(jq -s 'map(.requests_per_sec) | max' "$name"-c*.json)
  for f in 0.25 0.5 0.75 0.9; do
    rate=$(jq -n "[$capacity * $f, 1] | max | floor")
    step -shards "$shards" -zipf "$zipf" -rate "$rate" -out "$name-r$rate.json"
  done
  step -shards "$shards" -zipf "$zipf" -concurrency 16 -batch 100 -out "$name-b100.json"
done
