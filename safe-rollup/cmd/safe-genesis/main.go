// Command safe-genesis writes the L2 genesis and rollup config of a Safe rollup. It runs the
// upstream L2Genesis script with the SafeRollupGateway as the L1 messenger and bridge, and points
// op-node at the gateway for both deposits and system config updates.
package main

import (
	"context"
	"flag"
	"fmt"
	"math/big"
	"os"
	"path/filepath"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/log"

	"github.com/ethereum-optimism/optimism/op-chain-ops/foundry"
	"github.com/ethereum-optimism/optimism/op-chain-ops/genesis"
	"github.com/ethereum-optimism/optimism/op-chain-ops/script"
	"github.com/ethereum-optimism/optimism/op-core/forks"
	"github.com/ethereum-optimism/optimism/op-service/eth"
	"github.com/ethereum-optimism/optimism/op-service/ioutil"
	"github.com/ethereum-optimism/optimism/op-service/jsonutil"
	oplog "github.com/ethereum-optimism/optimism/op-service/log"
)

// Default OP stack fee parameters.
const (
	basefeeScalar     = 1368
	blobBaseFeeScalar = 801949
)

// L2GenesisInput mirrors the input struct of scripts/L2Genesis.s.sol.
type L2GenesisInput struct {
	L1ChainID                                *big.Int
	L2ChainID                                *big.Int
	L1CrossDomainMessengerProxy              common.Address
	L1StandardBridgeProxy                    common.Address
	L1ERC721BridgeProxy                      common.Address
	OpChainProxyAdminOwner                   common.Address
	SequencerFeeVaultRecipient               common.Address
	SequencerFeeVaultMinimumWithdrawalAmount *big.Int
	SequencerFeeVaultWithdrawalNetwork       *big.Int
	BaseFeeVaultRecipient                    common.Address
	BaseFeeVaultMinimumWithdrawalAmount      *big.Int
	BaseFeeVaultWithdrawalNetwork            *big.Int
	L1FeeVaultRecipient                      common.Address
	L1FeeVaultMinimumWithdrawalAmount        *big.Int
	L1FeeVaultWithdrawalNetwork              *big.Int
	OperatorFeeVaultRecipient                common.Address
	OperatorFeeVaultMinimumWithdrawalAmount  *big.Int
	OperatorFeeVaultWithdrawalNetwork        *big.Int
	GovernanceTokenOwner                     common.Address
	Fork                                     *big.Int
	EnableGovernance                         bool
	FundDevAccounts                          bool
	UseCustomGasToken                        bool
	GasPayingTokenName                       string
	GasPayingTokenSymbol                     string
	NativeAssetLiquidityAmount               *big.Int
	LiquidityControllerOwner                 common.Address
	DevFeatureBitmap                         common.Hash
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	l1RPC := flag.String("l1-rpc", "http://127.0.0.1:8545", "L1 RPC used to pick the L1 start block")
	artifactsDir := flag.String("artifacts", "packages/contracts-bedrock/forge-artifacts", "contracts-bedrock forge-artifacts")
	gateway := flag.String("gateway", "", "SafeRollupGateway address")
	safe := flag.String("safe", "", "Safe address, owner of the L2 ProxyAdmin through its alias")
	batcher := flag.String("batcher", "", "batcher address")
	sequencer := flag.String("sequencer", "", "unsafe block signer address")
	feeRecipient := flag.String("fee-recipient", "", "L2 fee vaults recipient")
	l2ChainID := flag.Uint64("l2-chain-id", 901, "L2 chain ID")
	outDir := flag.String("out", ".", "output directory for genesis.json and rollup.json")
	fork := flag.String("fork", "jovian", "latest L2 hardfork, active from genesis")
	altDA := flag.Bool("alt-da", true, "post keccak commitments to L1 and keep batch data in an offchain DA service")
	flag.Parse()

	ctx := context.Background()
	lgr := oplog.NewLogger(log.DiscardHandler())

	l1, err := ethclient.DialContext(ctx, *l1RPC)
	if err != nil {
		return fmt.Errorf("dial L1: %w", err)
	}
	l1ChainID, err := l1.ChainID(ctx)
	if err != nil {
		return fmt.Errorf("L1 chain ID: %w", err)
	}
	head, err := l1.HeaderByNumber(ctx, nil)
	if err != nil {
		return fmt.Errorf("L1 head: %w", err)
	}
	l1Start := eth.BlockRefFromHeader(head)

	gatewayAddr := common.HexToAddress(*gateway)
	l2ProxyAdminOwner := applyL1ToL2Alias(common.HexToAddress(*safe))
	feeRecipientAddr := common.HexToAddress(*feeRecipient)
	schedule := &genesis.UpgradeScheduleDeployConfig{}
	schedule.ActivateForkAtGenesis(forks.Name(*fork))

	artifacts := os.DirFS(*artifactsDir).(foundry.StatDirFs)
	deployer := common.Address{19: 0xde}
	scriptCtx := script.DefaultContext
	scriptCtx.Sender, scriptCtx.Origin = deployer, deployer
	host := script.NewHost(lgr, &foundry.ArtifactsFS{FS: artifacts}, nil, scriptCtx,
		script.WithIsolatedBroadcasts(), script.WithCreate2Deployer())
	if err := host.EnableCheats(); err != nil {
		return fmt.Errorf("enable cheats: %w", err)
	}
	l2Genesis, err := script.NewDeployScriptWithoutOutputFromFile[L2GenesisInput](host, "L2Genesis.s.sol", "L2Genesis")
	if err != nil {
		return fmt.Errorf("load L2Genesis: %w", err)
	}
	minWithdrawal := new(big.Int).Mul(big.NewInt(10), big.NewInt(1e18))
	localNetwork := genesis.WithdrawalNetwork("local")
	local := big.NewInt(int64(localNetwork.ToUint8()))
	if err := l2Genesis.Run(L2GenesisInput{
		L1ChainID:                                l1ChainID,
		L2ChainID:                                new(big.Int).SetUint64(*l2ChainID),
		L1CrossDomainMessengerProxy:              gatewayAddr,
		L1StandardBridgeProxy:                    gatewayAddr,
		L1ERC721BridgeProxy:                      gatewayAddr,
		OpChainProxyAdminOwner:                   l2ProxyAdminOwner,
		SequencerFeeVaultRecipient:               feeRecipientAddr,
		SequencerFeeVaultMinimumWithdrawalAmount: minWithdrawal,
		SequencerFeeVaultWithdrawalNetwork:       local,
		BaseFeeVaultRecipient:                    feeRecipientAddr,
		BaseFeeVaultMinimumWithdrawalAmount:      minWithdrawal,
		BaseFeeVaultWithdrawalNetwork:            local,
		L1FeeVaultRecipient:                      feeRecipientAddr,
		L1FeeVaultMinimumWithdrawalAmount:        minWithdrawal,
		L1FeeVaultWithdrawalNetwork:              local,
		OperatorFeeVaultRecipient:                feeRecipientAddr,
		OperatorFeeVaultMinimumWithdrawalAmount:  minWithdrawal,
		OperatorFeeVaultWithdrawalNetwork:        local,
		Fork:                                     big.NewInt(schedule.SolidityForkNumber(1)),
		NativeAssetLiquidityAmount:               big.NewInt(0),
	}); err != nil {
		return fmt.Errorf("run L2Genesis: %w", err)
	}
	host.Wipe(deployer)
	allocs, err := host.StateDump()
	if err != nil {
		return fmt.Errorf("dump L2 state: %w", err)
	}

	baseFee := hexutil.Big(*big.NewInt(1_000_000_000))
	cfg := genesis.DeployConfig{
		L1DependenciesConfig: genesis.L1DependenciesConfig{
			L1StandardBridgeProxy:       gatewayAddr,
			L1CrossDomainMessengerProxy: gatewayAddr,
			L1ERC721BridgeProxy:         gatewayAddr,
			SystemConfigProxy:           gatewayAddr,
			OptimismPortalProxy:         gatewayAddr,
		},
		L2InitializationConfig: genesis.L2InitializationConfig{
			L2GenesisBlockDeployConfig: genesis.L2GenesisBlockDeployConfig{
				L2GenesisBlockGasLimit:      60_000_000,
				L2GenesisBlockBaseFeePerGas: &baseFee,
			},
			GasPriceOracleDeployConfig: genesis.GasPriceOracleDeployConfig{
				GasPriceOracleBaseFeeScalar:     basefeeScalar,
				GasPriceOracleBlobBaseFeeScalar: blobBaseFeeScalar,
			},
			EIP1559DeployConfig: genesis.EIP1559DeployConfig{
				EIP1559Denominator:       50,
				EIP1559DenominatorCanyon: 250,
				EIP1559Elasticity:        6,
			},
			UpgradeScheduleDeployConfig: *schedule,
			L2CoreDeployConfig: genesis.L2CoreDeployConfig{
				L1ChainID:             l1ChainID.Uint64(),
				L2ChainID:             *l2ChainID,
				L2BlockTime:           2,
				MaxSequencerDrift:     600,
				SequencerWindowSize:   3600,
				ChannelTimeoutBedrock: 300,
				BatchInboxAddress:     batchInboxAddress(*l2ChainID),
			},
			AltDADeployConfig: genesis.AltDADeployConfig{
				UseAltDA:         *altDA,
				DACommitmentType: "KeccakCommitment",
			},
			OperatorDeployConfig: genesis.OperatorDeployConfig{
				BatchSenderAddress:  common.HexToAddress(*batcher),
				P2PSequencerAddress: common.HexToAddress(*sequencer),
			},
		},
	}

	l2Gen, err := genesis.BuildL2Genesis(&cfg, allocs, l1Start)
	if err != nil {
		return fmt.Errorf("build L2 genesis: %w", err)
	}
	l2Block := l2Gen.ToBlock()
	rollupCfg, err := cfg.RollupConfig(l1Start, l2Block.Hash(), l2Block.NumberU64())
	if err != nil {
		return fmt.Errorf("build rollup config: %w", err)
	}
	if err := rollupCfg.Check(); err != nil {
		return fmt.Errorf("invalid rollup config: %w", err)
	}

	if err := jsonutil.WriteJSON(l2Gen, ioutil.ToAtomicFile(filepath.Join(*outDir, "genesis.json"), 0o644)); err != nil {
		return err
	}
	return jsonutil.WriteJSON(rollupCfg, ioutil.ToAtomicFile(filepath.Join(*outDir, "rollup.json"), 0o644))
}

// batchInboxAddress is 0xff00..00 followed by the chain ID, the usual convention for devnets.
func batchInboxAddress(chainID uint64) common.Address {
	var addr common.Address
	addr[0] = 0xff
	new(big.Int).SetUint64(chainID).FillBytes(addr[12:])
	return addr
}

func applyL1ToL2Alias(addr common.Address) common.Address {
	offset := new(big.Int).SetBytes(common.FromHex("0x1111000000000000000000000000000000001111"))
	aliased := new(big.Int).Add(new(big.Int).SetBytes(addr.Bytes()), offset)
	return common.BigToAddress(aliased)
}
