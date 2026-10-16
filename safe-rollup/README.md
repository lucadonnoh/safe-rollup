# Safe rollup

An OP stack chain that takes the Stage 0 trust model at face value: the operator's cold keys are
trusted, so the L1 side is a Safe and two small contracts, and everything that only exists to
constrain the operator is gone.

## Trust model

Hot keys (sequencer, batcher, relayer) can fail. Cold keys (the Safe owners) never fail, watch
every payout and recover from any hot key failure. Under this model the guarantees users get are
the same as with the standard OP stack contracts at Stage 0, because those can also be upgraded by
the admin at any time.

## What runs

L1:

- **Safe** (v1.4.1, unchanged): holds all bridged ETH and ERC20s. The owners are the cold keys.
- **`SafeRollupGateway`**: holds nothing. It turns deposits into the `TransactionDeposited` events
  op-node derives from, acts as the L1 messenger and bridge the unchanged L2 predeploys talk to,
  relays L2 to L1 messages for the Safe, and emits `ConfigUpdate` for op-node.
- **`WithdrawalDelayModule`**: Safe module. The relayer queues one payout per L2 withdrawal, anyone
  executes it after the delay, the Safe can veto it before that.

No portal, no state roots, no proofs, no dispute games, no proxies, no deploy or upgrade manager.
The batcher posts only keccak commitments (34 bytes per batch) to an EOA inbox; the batch data
lives in an offchain DA server.

Tokens: an L1-native ERC20 is escrowed in the Safe and minted on L2 as an `OptimismMintableERC20`
created with the L2 factory predeploy. An L2-native ERC20 needs an L1 copy, deployed as
`OptimismMintableERC20(gateway, l2Token, name, symbol, decimals)`: withdrawals mint it through a
queued Safe payout, deposits burn it and the L2 bridge releases the escrowed tokens.

Offchain:

- op-geth, op-node, op-batcher and the reference DA server from op-alt-da.
- `ts/relayer.ts` (hot key): queues the payout of every L2 withdrawal once its block is derived
  from L1, and executes payouts after the delay. Replaces op-proposer.
- `ts/watcher.ts` (cold keys): checks every queued payout against its own verifier node, which
  derives the chain from L1 and the DA server only, and vetoes anything it cannot match.
- `ts/lib.ts`: client library for deposits, withdrawals and their status.
- `cmd/safe-genesis`: L2 genesis and rollup config. Replaces op-deployer.

## Run it

```sh
go build -o safe-rollup/bin/op-node ./op-node/cmd
go build -o safe-rollup/bin/op-batcher ./op-batcher/cmd
go build -o safe-rollup/bin/da-server ./op-alt-da/cmd/daserver
go build -o safe-rollup/bin/safe-genesis ./safe-rollup/cmd/safe-genesis
# op-geth: build ./cmd/geth from github.com/ethereum-optimism/op-geth at the tag pinned in go.mod
(cd packages/contracts-bedrock && forge build)
(cd safe-rollup/ts && npm install)

safe-rollup/devnet/up.sh          # anvil, contracts, DA server, sequencer, verifier, batcher, relayer, watcher
node safe-rollup/ts/demo.ts       # deposits, withdrawals, and a compromised relayer that gets vetoed
safe-rollup/devnet/down.sh
```

## What was removed

Counted with `loc.py` / `removed.py` (non-blank, non-comment lines of Go, Solidity and Rust)
against upstream `develop` at e45aad88:

| | lines |
|---|---:|
| Upstream total | 633,212 |
| Removed because of the trust model: proofs, settlement, L1 system, interop, DA challenges, Ethereum DA | 244,310 |
| Removed because this chain does not use it: alternative clients, ops tooling, presets | 171,020 |
| Removed e2e and contract test harnesses built around the old L1 system | 98,030 |
| Net lines cut from files that were kept | 2,193 |
| **Remaining** | **118,472** |
| Added: contracts, genesis tool, deploy script and tests (plus ~680 lines of TypeScript) | 813 |

The withdrawal flow in viem's OP stack extension (prove, finalize, games, output roots, timing) is
about 1,900 lines; `ts/lib.ts` is about 250.

## Not done

- NFTs are not bridged.
- Code paths for hardforks older than the genesis fork are still in op-node.
- The test harnesses that deployed the full L1 system were deleted, not rewritten; coverage of
  op-node and op-batcher against a live L1 is now only the devnet demo.
- `test/libraries/EOA.t.sol` has one fuzz failure with forge < 1.8 (7702 delegation to address 0).
