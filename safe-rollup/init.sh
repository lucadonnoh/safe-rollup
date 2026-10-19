#!/usr/bin/env bash
# One-time setup of a Safe rollup on the L1 in $SR_ENV (default: the devnet): deploys the Safe, the
# gateway and the withdrawal module, then writes the L2 genesis and rollup config and initializes
# both op-geth datadirs.
set -euo pipefail

SR="$(cd "$(dirname "$0")" && pwd)"
set -a && source "${SR_ENV:-$SR/devnet/devnet.env}" && set +a
DATA="${DATA:-$SR/.devnet}"
CONTRACTS="$SR/../packages/contracts-bedrock"

if [ -e "$DATA/addresses.json" ]; then
  echo "$DATA already holds a deployment" >&2 && exit 1
fi
mkdir -p "$DATA/logs" "$DATA/da-store" "$CONTRACTS/.testdata"

echo "deploying Safe, gateway and withdrawal module"
# Gas comes from the L1 node, one transaction at a time: Sepolia prices state creation far above
# forge's local EVM. The L1 contracts are rarely called, so they are compiled for size (200 optimizer
# runs, in their own output directory): the gateway then needs about 12.8M gas there, under the 16.77M
# per-tx cap.
(
  cd "$CONTRACTS"
  export FOUNDRY_OPTIMIZER_RUNS=200 FOUNDRY_OUT=.testdata/deploy-artifacts FOUNDRY_CACHE_PATH=.testdata/deploy-cache
  OWNER_KEYS="$OWNER1_KEY,$OWNER2_KEY,$OWNER3_KEY" THRESHOLD=2 RELAYER="$RELAYER_ADDR" \
    OUT=.testdata/safe-rollup.json forge script scripts/safe-rollup/DeploySafeRollup.s.sol \
    --rpc-url "$L1_RPC" --broadcast --slow --skip-simulation --gas-estimate-multiplier 103 \
    --private-key "$DEPLOYER_KEY" >"$DATA/logs/deploy.log" 2>&1
  mv .testdata/safe-rollup.json "$DATA/addresses.json"
)
SAFE=$(jq -r .safe "$DATA/addresses.json")
GATEWAY=$(jq -r .gateway "$DATA/addresses.json")

echo "generating L2 genesis and rollup config"
(
  cd "$CONTRACTS"
  GATEWAY="$GATEWAY" SAFE="$SAFE" OUT=.testdata/allocs.json \
    forge script scripts/safe-rollup/SafeRollupGenesis.s.sol >"$DATA/logs/genesis.log" 2>&1
  mv .testdata/allocs.json "$DATA/allocs.json"
)
"$SR/bin/safe-genesis" --l1-rpc "$L1_RPC" --l1-start "$L1_START" --allocs "$DATA/allocs.json" \
  --gateway "$GATEWAY" --batcher "$BATCHER_ADDR" --l2-chain-id "$L2_CHAIN_ID" --out "$DATA"
openssl rand -hex 32 >"$DATA/jwt.txt"
"$SR/bin/op-geth" init --datadir "$DATA/sequencer-geth" "$DATA/genesis.json" >"$DATA/logs/sequencer-geth-init.log" 2>&1
"$SR/bin/op-geth" init --state.scheme hash --datadir "$DATA/verifier-geth" "$DATA/genesis.json" \
  >"$DATA/logs/verifier-geth-init.log" 2>&1

echo "Safe:    $SAFE"
echo "Gateway: $GATEWAY"
echo "Module:  $(jq -r .module "$DATA/addresses.json")"
