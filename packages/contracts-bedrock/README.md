# Safe rollup contracts

L1: `src/safe-rollup` (gateway and withdrawal delay module, next to a Safe). L2: the predeploys in
`src/L2` and `src/universal`, set up by `scripts/safe-rollup/SafeRollupGenesis.s.sol`. See
`safe-rollup/README.md` at the repo root for the design.

```bash
just build
just test
```
