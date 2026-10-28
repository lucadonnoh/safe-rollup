# Safe rollup

An OP stack chain that takes the Stage 0 trust model at face value: the operator's cold keys are
trusted, so the L1 side is a Safe and two small contracts, and everything that only exists to
constrain the operator is gone.

## What this is

This is a parody. It shows that a Stage 0 rollup gives its
users nothing the operator's multisig does not already give them. Read Safe as in the Safe
multisig, not as in safe funds. It ran on Sepolia with test ETH from October 16 to October 28,
2026, and is not intended for any other use. "Safe" means the unmodified Safe v1.4.1 contracts
this chain is built on. The project is not affiliated with, sponsored or endorsed by the Safe
Ecosystem Foundation. SAFE{CORE}® and SAFE{WALLET}®, and the Safe logos are trademarks or
registered trademarks of Safe Ecosystem Foundation.

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
  relays L2 to L1 messages for the Safe, and emits `ConfigUpdate` for op-node. The Safe can also
  deposit a transaction from any L2 account with `depositTransactionOverride`, which can move any
  account's ETH and tokens on L2. HashKey Chain's admin used a function with the same name and
  parameters on 2026-10-13: it swapped OptimismPortal2 for a custom implementation for six minutes
  to move 142,818 USDT out of a user's L2 account.
- **`WithdrawalDelayModule`**: Safe module. The relayer queues one payout per L2 withdrawal, anyone
  executes it after the delay, the Safe can veto it before that. The relayer and the delay are fixed:
  the Safe changes them by enabling a new module.

No portal, no state roots, no proofs, no dispute games, no proxies, no deploy or upgrade manager.
The batcher posts only keccak commitments (34 bytes per batch) to an EOA inbox; the batch data
lives in an offchain DA server. There is no Ethereum DA mode, no blob support and no DA throttling.

Every fork up to Jovian is active from genesis, so op-node and op-batcher carry no fork schedule:
one L1 info format, one batch pipeline (the Holocene stages), fixed engine API versions. Interop,
the supernode authority, follow-source sync and the HA conductor hooks are gone. The unsafe block
signer for p2p gossip is set offchain with `--p2p.sequencer.address`, since there is no signer
slot on L1.

Tokens: an L1-native ERC20 is escrowed in the Safe and minted on L2 as an `OptimismMintableERC20`
created with the L2 factory predeploy. An L2-native ERC20 needs an L1 copy, deployed as
`OptimismMintableERC20(safe, l2Token, name, symbol, decimals)`: withdrawals mint it through a
queued Safe payout, deposits move it into the Safe and the L2 bridge releases the escrowed tokens.

Offchain:

- op-geth, op-node, op-batcher and the reference DA server from op-alt-da.
- `ts/relayer.ts` (hot key): queues the payout of every L2 withdrawal once its block is derived
  from L1, and executes payouts after the delay. Replaces op-proposer.
- `ts/watcher.ts` (cold keys): checks every queued payout against its own verifier node, which
  derives the chain from L1 and the DA server only, and vetoes anything it cannot match.
- `ts/lib.ts`: client library for deposits, withdrawals and their status.
- `ts/server.ts`: status page built from chain data, and a rate-limited RPC proxy to the sequencer.
- `cmd/safe-genesis`: L2 genesis and rollup config, from the L2 state that
  `SafeRollupGenesis.s.sol` dumps with `forge script`. Replaces op-deployer and op-chain-ops.

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
safe-rollup/run.sh demo           # deposits, withdrawals, and a compromised relayer that gets vetoed
safe-rollup/devnet/down.sh
```

One env file configures everything: `devnet/devnet.env` by default, or the file in `$SR_ENV`.
`init.sh` deploys the L1 contracts and writes the genesis once, and `run.sh <service>` runs one
service in the foreground. `up.sh` is anvil plus those two.

### On Sepolia

The public instance ran from October 16 to October 28, 2026, with a status page at
[saferollup.fyi](https://saferollup.fyi) and a Blockscout explorer at
[explorer.saferollup.fyi](https://explorer.saferollup.fyi). It was then stopped for good, and both
sites now show its final state: with `FROZEN=true` in the env file, `ts/server.ts` serves the last
status it saved and answers RPC calls with an error, and the explorer serves what it had indexed.

`deploy/` has an env template for Sepolia (canonical Safe, finalized start block, low fee floors,
a 10 minute payout delay) and systemd user units: one per `run.sh` service, plus `story`, which
repeats the demo. Copy the template outside the repo, fill in fresh keys and fund the deployer,
batcher, relayer, first owner and demo user, then:

```sh
SR_ENV=~/safe-rollup/sepolia.env safe-rollup/init.sh
cp safe-rollup/deploy/safe-rollup@.service safe-rollup/deploy/safe-rollup.target ~/.config/systemd/user/
systemctl --user daemon-reload && systemctl --user enable --now safe-rollup.target
```

`deploy/explorer/` runs Blockscout (Postgres, backend, frontend, nginx) with Docker Compose on
`127.0.0.1:8091`. It takes blocks from the sequencer and traces from the verifier, which is an
archive node for this reason. It is pinned to backend 9.0.2 and frontend v2.3.1, the last releases
under GPL-3.0 with public images: Blockscout moved to a proprietary licence on 2026-04-22. As
committed it serves a stopped chain (`DISABLE_INDEXER=true` in `backend.env`). For a live one, set
that to false and start it with `--profile live`, which adds the relay to the two op-geth RPCs.

Never reuse well-known keys there: anvil's test accounts carry EIP-7702 sweeper delegations on
Sepolia, so any ETH paid out to them is gone at once.

## What was removed

Counted with `loc.py` / `removed.py` (non-blank, non-comment lines of Go, Solidity and Rust)
against upstream `develop` at e45aad88:

| | lines |
|---|---:|
| Upstream total | 633,212 |
| Removed because of the trust model: proofs, settlement, L1 system, interop, DA challenges, Ethereum DA | 251,849 |
| Removed because this chain does not use it: alternative clients, ops tooling, presets | 193,944 |
| Removed e2e and contract test harnesses built around the old L1 system | 101,022 |
| Removed hardfork history: forks before the genesis fork, and forks after it | 4,025 |
| Net lines cut from files that were kept, mostly fork switches and Ethereum DA paths | 11,444 |
| **Remaining** | **72,562** |
| Added: contracts, genesis tool, deploy and genesis scripts, tests (plus 1,306 lines of TypeScript, including the demo and the status page) | 1,634 |

Of the remaining lines, 41k are production code and 32k are tests.

The withdrawal flow in viem's OP stack extension (prove, finalize, games, output roots, timing) is
about 1,900 lines; `ts/lib.ts` is about 360.

## Not done

- NFTs are not bridged.
- The chain is pinned at Jovian: adopting a later fork means adding its code back to op-node.
- The test harnesses that deployed the full L1 system were deleted, not rewritten; coverage of
  op-node and op-batcher against a live L1 is now only the devnet demo.
- `test/libraries/EOA.t.sol` has one fuzz failure with forge < 1.8 (7702 delegation to address 0).
