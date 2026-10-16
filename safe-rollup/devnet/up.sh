#!/usr/bin/env bash
# Starts a Safe rollup devnet: anvil as L1, the Safe rollup contracts, an offchain DA server, op-geth,
# op-node and op-batcher. The batcher posts only keccak commitments to L1. There is no proposer and no
# proof system.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
SR="$ROOT/safe-rollup"
BIN="$SR/bin"
DATA="$SR/.devnet"
CONTRACTS="$ROOT/packages/contracts-bedrock"
source "$SR/devnet/accounts.env"

L1_RPC=http://127.0.0.1:8545
L2_RPC=http://127.0.0.1:9545
ROLLUP_RPC=http://127.0.0.1:7545
DA_SERVER=http://127.0.0.1:3100
WITHDRAWAL_DELAY="${WITHDRAWAL_DELAY:-30}"

"$SR/devnet/down.sh" >/dev/null 2>&1 || true
rm -rf "$DATA" && mkdir -p "$DATA/logs"

start() {
  local name=$1; shift
  nohup "$@" >"$DATA/logs/$name.log" 2>&1 </dev/null &
  echo $! >"$DATA/$name.pid"
}

wait_rpc() {
  for _ in $(seq 1 60); do
    cast chain-id --rpc-url "$1" >/dev/null 2>&1 && return 0
    sleep 0.5
  done
  echo "timed out waiting for $1" >&2 && exit 1
}

echo "starting L1 (anvil)"
start anvil anvil --chain-id 900 --block-time 2 --hardfork prague --port 8545
wait_rpc $L1_RPC

echo "deploying Safe, gateway and withdrawal module"
(
  cd "$CONTRACTS" && mkdir -p .testdata
  OWNER_KEYS="$OWNER1_KEY,$OWNER2_KEY,$OWNER3_KEY" THRESHOLD=2 RELAYER="$RELAYER_ADDR" \
    WITHDRAWAL_DELAY="$WITHDRAWAL_DELAY" OUT=.testdata/safe-rollup.json \
    forge script scripts/safe-rollup/DeploySafeRollup.s.sol --rpc-url $L1_RPC --broadcast \
    --private-key "$DEPLOYER_KEY" >"$DATA/logs/deploy.log" 2>&1
  mv .testdata/safe-rollup.json "$DATA/addresses.json"
)
SAFE=$(jq -r .safe "$DATA/addresses.json")
GATEWAY=$(jq -r .gateway "$DATA/addresses.json")

echo "generating L2 genesis and rollup config"
(
  cd "$CONTRACTS"
  L2_CHAIN_ID=901 GATEWAY="$GATEWAY" SAFE="$SAFE" OUT=.testdata/allocs.json \
    forge script scripts/safe-rollup/SafeRollupGenesis.s.sol >"$DATA/logs/genesis.log" 2>&1
  mv .testdata/allocs.json "$DATA/allocs.json"
)
"$BIN/safe-genesis" --l1-rpc $L1_RPC --allocs "$DATA/allocs.json" --gateway "$GATEWAY" \
  --batcher "$BATCHER_ADDR" --l2-chain-id 901 --out "$DATA"
cp "$SR/devnet/l1-chain-config.json" "$DATA/"
openssl rand -hex 32 >"$DATA/jwt.txt"

echo "starting DA server"
mkdir -p "$DATA/da-store"
start da-server "$BIN/da-server" --addr 127.0.0.1 --port 3100 --file.path "$DATA/da-store"

# Two L2 nodes from the same genesis: the sequencer, and a verifier that derives the chain from L1
# only. The watcher reads the verifier, so its view does not depend on the sequencer.
l2_node() {
  local name=$1 http=$2 auth=$3 rollup=$4; shift 4
  "$BIN/op-geth" init --datadir "$DATA/$name-geth" "$DATA/genesis.json" >"$DATA/logs/$name-geth-init.log" 2>&1
  start "$name-geth" "$BIN/op-geth" --datadir "$DATA/$name-geth" --networkid 901 --port 0 \
    --http --http.addr 127.0.0.1 --http.port "$http" --http.api eth,net,web3,debug,txpool,miner \
    --authrpc.addr 127.0.0.1 --authrpc.port "$auth" --authrpc.jwtsecret "$DATA/jwt.txt" \
    --nodiscover --maxpeers 0 --syncmode full --rollup.disabletxpoolgossip
  wait_rpc "http://127.0.0.1:$http"
  start "$name-node" "$BIN/op-node" --l1 $L1_RPC --l1.rpckind standard --l1.trustrpc \
    --l1.http-poll-interval 1s --altda.da-server $DA_SERVER \
    --l2 "http://127.0.0.1:$auth" --l2.jwt-secret "$DATA/jwt.txt" \
    --rollup.config "$DATA/rollup.json" --rollup.l1-chain-config "$DATA/l1-chain-config.json" \
    --verifier.l1-confs 0 --p2p.disable --rpc.addr 127.0.0.1 --rpc.port "$rollup" "$@"
}

echo "starting sequencer (op-geth + op-node)"
l2_node sequencer 9545 9551 7545 --sequencer.enabled --sequencer.l1-confs 0
echo "starting verifier (op-geth + op-node)"
l2_node verifier 9645 9651 7645

echo "starting op-batcher"
sleep 3
start op-batcher "$BIN/op-batcher" --l1-eth-rpc $L1_RPC --l2-eth-rpc $L2_RPC --rollup-rpc $ROLLUP_RPC \
  --private-key "$BATCHER_KEY" --altda.da-server $DA_SERVER --max-channel-duration 1 \
  --poll-interval 1s --num-confirmations 1 --sub-safety-margin 4 --resubmission-timeout 30s \
  --rpc.port 6545

echo "starting relayer (hot key) and watcher (cold keys)"
sleep 2
NODE="${NODE:-$HOME/.nvm/versions/node/v24.13.0/bin/node}"
start relayer "$NODE" "$SR/ts/relayer.ts"
start watcher "$NODE" "$SR/ts/watcher.ts"

echo "Safe:    $SAFE"
echo "Gateway: $GATEWAY"
echo "Module:  $(jq -r .module "$DATA/addresses.json")"
echo "logs in $DATA/logs"
