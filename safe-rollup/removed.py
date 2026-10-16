#!/usr/bin/env python3
"""Breaks down the code removed from upstream by reason.

Usage: removed.py <upstream checkout> <this checkout>
Counts non-blank, non-comment lines of Go, Solidity and Rust, like loc.py.
"""
import os
import sys

from loc import EXTS, SKIP_DIRS, is_test, sloc

# First matching prefix wins.
TRUST_MODEL = "trust model: proofs, settlement, L1 system, interop, DA challenges, Ethereum DA"
UNUSED = "not used by this chain: alternative clients, ops tooling, presets"
HARNESS = "e2e and contract test harnesses built around the old L1 system"
FORKS = "hardfork history: forks before the genesis fork, and forks after it"
RULES = [
    (TRUST_MODEL, [
        "op-proposer/", "op-challenger/", "op-dispute-mon/", "cannon/", "op-preimage/", "op-deployer/",
        "op-fetcher/", "op-interop-filter/", "op-interop-mon/", "op-supernode/",
        "op-chain-ops/crossdomain/", "op-chain-ops/interopgen/", "op-chain-ops/interopbridge/",
        "op-chain-ops/interopsmoke/", "op-chain-ops/opcmregistry/", "op-chain-ops/cmd/check-output-root/",
        "op-chain-ops/cmd/check-super-root/", "op-chain-ops/cmd/check-prestate/",
        "op-chain-ops/cmd/unclaimed-credits/", "op-chain-ops/cmd/withdrawal/", "op-chain-ops/cmd/interop-smoke/",
        "op-node/bindings/", "op-node/node/safedb/", "op-node/node/superroot_api", "op-node/rollup/output_root",
        "op-node/withdrawals/", "op-node/cmd/interop-deposits-dump/", "op-node/rollup/derive/blob_data_source",
        "op-node/rollup/finality/altda", "op-node/rollup/derive/altda_data_source_test",
        "op-node/service_depset_test", "op-node/rollup/driver/sync_deriver_test",
        "op-alt-da/bindings/", "op-alt-da/dastate", "op-alt-da/damgr_test", "op-alt-da/metrics", "op-alt-da/params",
        "op-core/interop/", "op-core/devfeatures/", "op-service/txintent/", "op-service/eth/safety/",
        "op-service/apis/interop_filter", "op-service/sources/interop_filter",
        "rust/kona/crates/proof/", "rust/kona/bin/client/", "rust/kona/bin/host/", "rust/kona/sp1/",
        "rust/op-reth/bin/proof-bench/", "rust/kona/tests/proofs/", "rust/op-reth/tests/proofs/",
        "packages/contracts-bedrock/src/L1/", "packages/contracts-bedrock/src/dispute/",
        "packages/contracts-bedrock/src/cannon/", "packages/contracts-bedrock/src/safe/",
        "packages/contracts-bedrock/src/integration/", "packages/contracts-bedrock/src/legacy/AddressManager",
        "packages/contracts-bedrock/src/legacy/L1ChugSplashProxy", "packages/contracts-bedrock/src/legacy/ResolvedDelegateProxy",
        "packages/contracts-bedrock/src/libraries/trie/", "packages/contracts-bedrock/src/libraries/rlp/RLPReader",
        "packages/contracts-bedrock/src/libraries/StaticConfig",
        "packages/contracts-bedrock/src/L2/CrossL2Inbox", "packages/contracts-bedrock/src/L2/L2ToL2CrossDomainMessenger",
        "packages/contracts-bedrock/src/L2/SuperchainETHBridge", "packages/contracts-bedrock/src/L2/ETHLiquidity",
        "packages/contracts-bedrock/src/L2/UndeliveredMessageExporter",
        "packages/contracts-bedrock/interfaces/L1/", "packages/contracts-bedrock/interfaces/dispute/",
        "packages/contracts-bedrock/interfaces/cannon/", "packages/contracts-bedrock/interfaces/safe/",
        "packages/contracts-bedrock/interfaces/L2/ICrossL2Inbox", "packages/contracts-bedrock/interfaces/L2/IL2ToL2",
        "packages/contracts-bedrock/interfaces/L2/ISuperchainETHBridge", "packages/contracts-bedrock/interfaces/L2/IETHLiquidity",
        "packages/contracts-bedrock/interfaces/L2/IUndeliveredMessageExporter",
        "packages/contracts-bedrock/scripts/deploy/", "packages/contracts-bedrock/scripts/upgrade/",
        "packages/contracts-bedrock/scripts/FetchChainInfo", "packages/contracts-bedrock/scripts/go-ffi/",
        "packages/contracts-bedrock/test/L1/", "packages/contracts-bedrock/test/dispute/",
        "packages/contracts-bedrock/test/cannon/", "packages/contracts-bedrock/test/opcm/",
        "packages/contracts-bedrock/test/safe/", "packages/contracts-bedrock/test/kontrol/",
        "packages/contracts-bedrock/test/libraries/trie/", "packages/contracts-bedrock/test/libraries/rlp/RLPReader",
        "packages/contracts-bedrock/test/libraries/StaticConfig", "packages/contracts-bedrock/test/integration/",
        "packages/contracts-bedrock/test/L2/L2ToL2CrossDomainMessenger",
        "packages/contracts-bedrock/test/legacy/L1ChugSplashProxy", "packages/contracts-bedrock/test/legacy/ResolvedDelegateProxy",
        # Ethereum DA: blobs, calldata batches, DA throttling.
        "op-batcher/batcher/throttler/", "op-batcher/batcher/channel_config_provider", "op-batcher/config/",
        "op-batcher/flags/throttle_flags", "op-batcher/flags/types", "op-batcher/metrics/metrics_test",
        "op-service/bgpo/", "op-service/eth/blob", "op-service/sources/l1_beacon_client", "op-service/apis/beacon",
        "op-service/sources/mocks/BeaconClient", "op-service/testutils/mock_blobs_fetcher",
        "op-service/txmgr/test_txmgr", "op-batcher/batcher/test_batch_submitter",
        "op-alt-da/damgr", "op-alt-da/damock", "op-alt-da/cmd/daserver/s3",
        # Interop and supernode: cross-safe authority, follow source, super roots.
        "op-node/rollup/iface.go", "op-node/rollup/engine/cross_safe_cache", "op-node/rollup/engine/super_authority",
        "op-node/rollup/engine/unsafe_ingestion_deny", "op-node/rollup/driver/follow_", "op-node/config/follow_source",
        "op-service/sources/follow_client", "op-service/sources/supernode_client", "op-service/apis/supernode",
        "op-service/eth/supernode_status", "op-service/eth/super_root", "op-service/eth/superroot_at_timestamp",
        "op-node/rollup/conductor/",
        # Output roots and proofs.
        "op-service/eth/output", "op-service/eth/account_proof", "op-service/eth/execution_witness",
        "op-service/eth/transition_state", "op-service/apis/proposer",
        # L1 system config signer slot, read by the runtime config.
        "op-node/node/runcfg/", "op-service/testutils/runtime_config",
    ]),
    (FORKS, [
        "op-core/forks/", "op-node/rollup/toggles", "op-node/rollup/chain_spec_test",
        "op-node/rollup/derive/channel_bank", "op-node/rollup/derive/batch_queue", "op-node/rollup/derive/channel_mux",
        "op-node/rollup/derive/batch_mux", "op-node/rollup/derive/l1_block_info_tob_test",
        "op-core/params/chain_config", "op-core/params/geth", "op-node/metrics/metrics_test",
        "op-core/nuts/", "op-node/rollup/derive/upgrade", "op-node/rollup/derive/ecotone_upgrade",
        "op-node/rollup/derive/fjord_upgrade", "op-node/rollup/derive/isthmus_upgrade",
        "op-node/rollup/derive/jovian_upgrade", "op-node/rollup/derive/karst_upgrade",
        "op-node/rollup/derive/interop_upgrade",
    ]),
    (HARNESS, [
        "op-e2e/", "op-devstack/", "op-acceptance-tests/", "op-up/", "rust/kona/tests/", "rust/op-reth/tests/",
        "packages/contracts-bedrock/test/",
    ]),
    (UNUSED, [""]),
]


def bucket(rel):
    for name, prefixes in RULES:
        if any(rel.startswith(p) for p in prefixes):
            return name


def files(root):
    out = {}
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames[:] = [d for d in dirnames if d not in SKIP_DIRS]
        for f in filenames:
            if os.path.splitext(f)[1] in EXTS:
                rel = os.path.relpath(os.path.join(dirpath, f), root)
                out[rel] = os.path.join(dirpath, f)
    return out


def main():
    before, after = files(sys.argv[1]), files(sys.argv[2])
    removed, shrunk, added = {}, {"prod": 0, "test": 0}, {"prod": 0, "test": 0}
    for rel, path in before.items():
        kind = "test" if is_test(rel) else "prod"
        n = sloc(path)
        if rel not in after:
            key = (bucket(rel), kind)
            removed[key] = removed.get(key, 0) + n
        else:
            shrunk[kind] += n - sloc(after[rel])
    for rel, path in after.items():
        if rel not in before:
            added["test" if is_test(rel) else "prod"] += sloc(path)

    total = 0
    for name, _ in RULES:
        prod, test = removed.get((name, "prod"), 0), removed.get((name, "test"), 0)
        total += prod + test
        print(f"{prod:9,d} prod {test:9,d} test  {name}")
    print(f"{shrunk['prod']:9,d} prod {shrunk['test']:9,d} test  net lines cut from files that were kept")
    total += shrunk["prod"] + shrunk["test"]
    print(f"{total:9,d} removed in total")
    print(f"{added['prod']:9,d} prod {added['test']:9,d} test  added (Safe rollup contracts, tools, devnet)")


if __name__ == "__main__":
    main()
