#!/usr/bin/env bash
# Starts a Safe rollup devnet: anvil as L1, the Safe rollup contracts, an offchain DA server, op-geth,
# op-node and op-batcher. The batcher posts only keccak commitments to L1. There is no proposer and no
# proof system.
set -euo pipefail

SR="$(cd "$(dirname "$0")/.." && pwd)"
DATA="$SR/.devnet"

"$SR/devnet/down.sh" >/dev/null 2>&1 || true
rm -rf "$DATA" && mkdir -p "$DATA/logs"

start() {
  nohup "$SR/run.sh" "$1" >"$DATA/logs/$1.log" 2>&1 </dev/null &
  echo $! >"$DATA/$1.pid"
}

wait_rpc() {
  for _ in $(seq 1 60); do
    cast chain-id --rpc-url "$1" >/dev/null 2>&1 && return 0
    sleep 0.5
  done
  echo "timed out waiting for $1" >&2 && exit 1
}

echo "starting L1 (anvil)"
nohup anvil --chain-id 900 --block-time 2 --hardfork prague --port 8545 >"$DATA/logs/anvil.log" 2>&1 </dev/null &
echo $! >"$DATA/anvil.pid"
wait_rpc http://127.0.0.1:8545

"$SR/init.sh"

echo "starting DA server, sequencer and verifier"
start da-server
start sequencer-geth && wait_rpc http://127.0.0.1:9545
start sequencer-node
start verifier-geth && wait_rpc http://127.0.0.1:9645
start verifier-node

echo "starting op-batcher"
sleep 3
start op-batcher

echo "starting relayer (hot key) and watcher (cold keys)"
sleep 2
export NODE="${NODE:-$HOME/.nvm/versions/node/v24.13.0/bin/node}"
start relayer
start watcher
echo "logs in $DATA/logs"
