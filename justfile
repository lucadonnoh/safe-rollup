# Builds the Go binaries the devnet runs (op-geth is built separately, see safe-rollup/README.md).
build:
  go build -o safe-rollup/bin/op-node ./op-node/cmd
  go build -o safe-rollup/bin/op-batcher ./op-batcher/cmd
  go build -o safe-rollup/bin/da-server ./op-alt-da/cmd/daserver
  go build -o safe-rollup/bin/safe-genesis ./safe-rollup/cmd/safe-genesis
  cd packages/contracts-bedrock && forge build

# Runs the Go and contract tests.
test:
  go test ./...
  cd packages/contracts-bedrock && forge test

# Starts the devnet and runs the user demo against it.
devnet: build
  safe-rollup/devnet/up.sh
  node safe-rollup/ts/demo.ts
