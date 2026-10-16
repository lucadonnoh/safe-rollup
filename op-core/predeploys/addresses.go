package predeploys

import "github.com/ethereum/go-ethereum/common"

// L2 predeploy addresses the Go services read. The full list lives in
// packages/contracts-bedrock/src/libraries/Predeploys.sol.
var (
	SequencerFeeVaultAddr   = common.HexToAddress("0x4200000000000000000000000000000000000011")
	L1BlockAddr             = common.HexToAddress("0x4200000000000000000000000000000000000015")
	L2ToL1MessagePasserAddr = common.HexToAddress("0x4200000000000000000000000000000000000016")
)
