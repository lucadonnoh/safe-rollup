// Command safe-genesis writes the L2 genesis and rollup config of a Safe rollup. It takes the L2
// state dumped by the SafeRollupGenesis forge script and points op-node at the SafeRollupGateway for
// both deposits and system config updates. Every fork up to Jovian is active from genesis.
package main

import (
	"context"
	"flag"
	"fmt"
	"math/big"
	"os"
	"path/filepath"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/misc/eip1559"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/params"

	opparams "github.com/ethereum-optimism/optimism/op-core/params"
	"github.com/ethereum-optimism/optimism/op-core/predeploys"
	"github.com/ethereum-optimism/optimism/op-node/rollup"
	"github.com/ethereum-optimism/optimism/op-service/eth"
	"github.com/ethereum-optimism/optimism/op-service/ioutil"
	"github.com/ethereum-optimism/optimism/op-service/jsonutil"
)

const (
	blockTime                = 2
	gasLimit                 = 60_000_000
	eip1559Elasticity        = 6
	eip1559Denominator       = 50
	eip1559DenominatorCanyon = 250
	basefeeScalar            = 1368
	blobBaseFeeScalar        = 801949
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	l1RPC := flag.String("l1-rpc", "http://127.0.0.1:8545", "L1 RPC used to pick the L1 start block")
	allocsPath := flag.String("allocs", "", "L2 state dumped by SafeRollupGenesis.s.sol")
	gateway := flag.String("gateway", "", "SafeRollupGateway address")
	batcher := flag.String("batcher", "", "batcher address")
	l2ChainID := flag.Uint64("l2-chain-id", 901, "L2 chain ID")
	outDir := flag.String("out", ".", "output directory for genesis.json and rollup.json")
	flag.Parse()

	ctx := context.Background()
	l1, err := ethclient.DialContext(ctx, *l1RPC)
	if err != nil {
		return fmt.Errorf("dial L1: %w", err)
	}
	l1ChainID, err := l1.ChainID(ctx)
	if err != nil {
		return fmt.Errorf("L1 chain ID: %w", err)
	}
	l1Start, err := l1.HeaderByNumber(ctx, nil)
	if err != nil {
		return fmt.Errorf("L1 head: %w", err)
	}

	allocs, err := jsonutil.LoadJSON[types.GenesisAlloc](*allocsPath)
	if err != nil {
		return fmt.Errorf("read allocs: %w", err)
	}
	gatewayAddr := common.HexToAddress(*gateway)

	l2Genesis := genesisBlock(*l2ChainID, l1Start.Time, *allocs)
	l2Block := l2Genesis.ToBlock()
	rollupCfg := &rollup.Config{
		Genesis: rollup.Genesis{
			L1:     eth.BlockID{Hash: l1Start.Hash(), Number: l1Start.Number.Uint64()},
			L2:     eth.BlockID{Hash: l2Block.Hash(), Number: l2Block.NumberU64()},
			L2Time: l2Genesis.Timestamp,
			SystemConfig: eth.SystemConfig{
				BatcherAddr: common.HexToAddress(*batcher),
				Scalar: eth.EncodeScalar(eth.EcotoneScalars{
					BlobBaseFeeScalar: blobBaseFeeScalar,
					BaseFeeScalar:     basefeeScalar,
				}),
				GasLimit:          gasLimit,
				OperatorFeeParams: eth.EncodeOperatorFeeParams(eth.OperatorFeeParams{}),
			},
		},
		BlockTime:              blockTime,
		SeqWindowSize:          3600,
		L1ChainID:              l1ChainID,
		L2ChainID:              new(big.Int).SetUint64(*l2ChainID),
		BatchInboxAddress:      batchInboxAddress(*l2ChainID),
		DepositContractAddress: gatewayAddr,
		L1SystemConfigAddress:  gatewayAddr,
		ChainOpConfig:          chainOpConfig(),
	}
	if err := rollupCfg.Check(); err != nil {
		return fmt.Errorf("invalid rollup config: %w", err)
	}

	if err := jsonutil.WriteJSON(l2Genesis, ioutil.ToAtomicFile(filepath.Join(*outDir, "genesis.json"), 0o644)); err != nil {
		return err
	}
	return jsonutil.WriteJSON(rollupCfg, ioutil.ToAtomicFile(filepath.Join(*outDir, "rollup.json"), 0o644))
}

func chainOpConfig() *opparams.OptimismConfig {
	canyon := uint64(eip1559DenominatorCanyon)
	return &opparams.OptimismConfig{
		EIP1559Elasticity:        eip1559Elasticity,
		EIP1559Denominator:       eip1559Denominator,
		EIP1559DenominatorCanyon: &canyon,
	}
}

func genesisBlock(chainID uint64, timestamp uint64, allocs types.GenesisAlloc) *core.Genesis {
	zero := uint64(0)
	canyon := uint64(eip1559DenominatorCanyon)
	cfg := &params.ChainConfig{
		ChainID:                 new(big.Int).SetUint64(chainID),
		HomesteadBlock:          common.Big0,
		EIP150Block:             common.Big0,
		EIP155Block:             common.Big0,
		EIP158Block:             common.Big0,
		ByzantiumBlock:          common.Big0,
		ConstantinopleBlock:     common.Big0,
		PetersburgBlock:         common.Big0,
		IstanbulBlock:           common.Big0,
		MuirGlacierBlock:        common.Big0,
		BerlinBlock:             common.Big0,
		LondonBlock:             common.Big0,
		ArrowGlacierBlock:       common.Big0,
		GrayGlacierBlock:        common.Big0,
		MergeNetsplitBlock:      common.Big0,
		TerminalTotalDifficulty: common.Big0,
		BedrockBlock:            common.Big0,
		RegolithTime:            &zero,
		CanyonTime:              &zero,
		ShanghaiTime:            &zero,
		CancunTime:              &zero,
		EcotoneTime:             &zero,
		FjordTime:               &zero,
		GraniteTime:             &zero,
		HoloceneTime:            &zero,
		PragueTime:              &zero,
		IsthmusTime:             &zero,
		JovianTime:              &zero,
		Optimism: &params.OptimismConfig{
			EIP1559Elasticity:        eip1559Elasticity,
			EIP1559Denominator:       eip1559Denominator,
			EIP1559DenominatorCanyon: &canyon,
		},
	}
	return &core.Genesis{
		Config:        cfg,
		Timestamp:     timestamp,
		GasLimit:      gasLimit,
		Difficulty:    common.Big0,
		Coinbase:      predeploys.SequencerFeeVaultAddr,
		BaseFee:       big.NewInt(1_000_000_000),
		BlobGasUsed:   &zero,
		ExcessBlobGas: &zero,
		ExtraData:     eip1559.EncodeJovianExtraData(eip1559DenominatorCanyon, eip1559Elasticity, 0),
		Alloc:         allocs,
	}
}

// batchInboxAddress is 0xff00..00 followed by the chain ID, the usual convention for devnets.
func batchInboxAddress(chainID uint64) common.Address {
	var addr common.Address
	addr[0] = 0xff
	new(big.Int).SetUint64(chainID).FillBytes(addr[12:])
	return addr
}
