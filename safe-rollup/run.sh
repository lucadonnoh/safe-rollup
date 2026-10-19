#!/usr/bin/env bash
# Runs one Safe rollup service in the foreground, configured by the env file in $SR_ENV (default:
# the devnet). devnet/up.sh starts every service this way, and so do the systemd units in deploy/.
set -euo pipefail

SR="$(cd "$(dirname "$0")" && pwd)"
set -a && source "${SR_ENV:-$SR/devnet/devnet.env}" && set +a
DATA="${DATA:-$SR/.devnet}"
BIN="$SR/bin"
DA_SERVER=http://127.0.0.1:3100

l2_geth() {
  local name=$1 http=$2 auth=$3
  shift 3
  exec "$BIN/op-geth" --datadir "$DATA/$name-geth" --networkid "$L2_CHAIN_ID" --port 0 \
    --http --http.addr 127.0.0.1 --http.port "$http" --http.api eth,net,web3,debug,txpool,miner \
    --authrpc.addr 127.0.0.1 --authrpc.port "$auth" --authrpc.jwtsecret "$DATA/jwt.txt" \
    --nodiscover --maxpeers 0 --syncmode full --rollup.disabletxpoolgossip "$@"
}

op_node() {
  local auth=$1 rpc=$2 l1_config=()
  shift 2
  [ -n "${L1_CHAIN_CONFIG:-}" ] && l1_config=(--rollup.l1-chain-config "$SR/$L1_CHAIN_CONFIG")
  # shellcheck disable=SC2086
  exec "$BIN/op-node" --l1 "$L1_RPC" --l1.rpckind standard $OP_NODE_FLAGS "${l1_config[@]}" \
    --altda.da-server $DA_SERVER --l2 "http://127.0.0.1:$auth" --l2.jwt-secret "$DATA/jwt.txt" \
    --rollup.config "$DATA/rollup.json" --p2p.disable --rpc.addr 127.0.0.1 --rpc.port "$rpc" "$@"
}

case "${1:-}" in
  da-server) exec "$BIN/da-server" --addr 127.0.0.1 --port 3100 --file.path "$DATA/da-store" ;;
  # Two L2 nodes from the same genesis: the sequencer, and a verifier that derives the chain from L1
  # and the DA server only. The watcher reads the verifier, so its view does not depend on the sequencer.
  # The verifier keeps every historical state, so an explorer can trace any block.
  sequencer-geth) l2_geth sequencer 9545 9551 ;;
  verifier-geth) l2_geth verifier 9645 9651 --state.scheme hash --gcmode archive ;;
  # shellcheck disable=SC2086
  sequencer-node) op_node 9551 7545 --sequencer.enabled $SEQUENCER_FLAGS ;;
  verifier-node) op_node 9651 7645 ;;
  # shellcheck disable=SC2086
  op-batcher) exec "$BIN/op-batcher" --l1-eth-rpc "$L1_RPC" --l2-eth-rpc http://127.0.0.1:9545 \
    --rollup-rpc http://127.0.0.1:7545 --private-key "$BATCHER_KEY" --altda.da-server $DA_SERVER \
    --rpc.port 6545 $BATCHER_FLAGS ;;
  relayer | watcher | demo | server) exec "${NODE:-node}" "$SR/ts/$1.ts" ;;
  # The demo on repeat, so a public instance always shows deposits, payouts and a veto.
  story) while true; do "${NODE:-node}" "$SR/ts/demo.ts" || true; sleep "${STORY_PAUSE:-600}"; done ;;
  *) echo "usage: run.sh <service>" >&2 && exit 1 ;;
esac
