# Safe rollup

Fork of the OP stack monorepo, cut down to a chain whose L1 side is a Safe, `SafeRollupGateway` and
`WithdrawalDelayModule`. Design, trust model and numbers: `safe-rollup/README.md`.

- Go: `op-node`, `op-batcher`, `op-alt-da` (DA server), `op-service`, `op-core`, `safe-rollup/cmd/safe-genesis`.
- Contracts: `packages/contracts-bedrock` (`just build`, `just test` there).
- User library and demo: `safe-rollup/ts`.
- `just build`, `just test`, `just devnet` from the root.
