#!/usr/bin/env bash
# Stops every devnet process started by up.sh and waits for them to exit.
DATA="$(cd "$(dirname "$0")/.." && pwd)/.devnet"
pids=()
for file in "$DATA"/*.pid; do
  [ -e "$file" ] || continue
  pids+=("$(cat "$file")")
  rm -f "$file"
done
[ ${#pids[@]} -eq 0 ] && exit 0
kill "${pids[@]}" 2>/dev/null || true
for _ in $(seq 1 40); do
  kill -0 "${pids[@]}" 2>/dev/null || exit 0
  sleep 0.25
done
kill -9 "${pids[@]}" 2>/dev/null || true
