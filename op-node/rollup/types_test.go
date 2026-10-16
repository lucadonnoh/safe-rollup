package rollup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"math/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"

	opparams "github.com/ethereum-optimism/optimism/op-core/params"
	"github.com/ethereum-optimism/optimism/op-service/eth"
	"github.com/ethereum-optimism/optimism/op-service/log"
	"github.com/ethereum-optimism/optimism/op-service/ptr"
	"github.com/ethereum-optimism/optimism/op-service/testlog"
)

func randConfig() *Config {
	rng := rand.New(rand.NewSource(1234))
	randHash := func() (out [32]byte) {
		rng.Read(out[:])
		return out
	}
	randAddr := func() (out common.Address) { // we need generics...
		rng.Read(out[:])
		return out
	}
	return &Config{
		Genesis: Genesis{
			L1:     eth.BlockID{Hash: randHash(), Number: 424242},
			L2:     eth.BlockID{Hash: randHash(), Number: 1337},
			L2Time: uint64(time.Now().Unix()),
			SystemConfig: eth.SystemConfig{
				BatcherAddr: randAddr(),
				Overhead:    randHash(),
				Scalar:      randHash(),
				GasLimit:    1234567,
			},
		},
		BlockTime:              2,
		SeqWindowSize:          2,
		L1ChainID:              big.NewInt(900),
		L2ChainID:              big.NewInt(901),
		BatchInboxAddress:      randAddr(),
		DepositContractAddress: randAddr(),
		L1SystemConfigAddress:  randAddr(),
		ChainOpConfig: &opparams.OptimismConfig{
			EIP1559Elasticity:        6,
			EIP1559Denominator:       50,
			EIP1559DenominatorCanyon: ptr.New(uint64(250)),
		},
	}
}

func TestConfigJSON(t *testing.T) {
	config := randConfig()
	data, err := json.Marshal(config)
	assert.NoError(t, err)
	var roundTripped Config
	assert.NoError(t, json.Unmarshal(data, &roundTripped))
	assert.Equal(t, &roundTripped, config)
}

// TestConfigChainOpConfigJSONWireFormat pins the on-the-wire serialization of the
// ChainOpConfig field. Its type moved from op-geth's params.OptimismConfig to
// op-core/params.OptimismConfig; the JSON must remain byte-for-byte identical.
func TestConfigChainOpConfigJSONWireFormat(t *testing.T) {
	config := randConfig()
	config.ChainOpConfig = &opparams.OptimismConfig{
		EIP1559Elasticity:        6,
		EIP1559Denominator:       50,
		EIP1559DenominatorCanyon: ptr.New(uint64(250)),
	}
	data, err := json.Marshal(config)
	require.NoError(t, err)
	require.Contains(t, string(data),
		`"chain_op_config":{"eip1559Elasticity":6,"eip1559Denominator":50,"eip1559DenominatorCanyon":250}`)

	// The optional canyon denominator is omitted when unset (omitempty).
	config.ChainOpConfig.EIP1559DenominatorCanyon = nil
	data, err = json.Marshal(config)
	require.NoError(t, err)
	require.Contains(t, string(data),
		`"chain_op_config":{"eip1559Elasticity":6,"eip1559Denominator":50}`)

	// The field is dropped entirely when nil (omitempty).
	config.ChainOpConfig = nil
	data, err = json.Marshal(config)
	require.NoError(t, err)
	require.NotContains(t, string(data), "chain_op_config")
}

type mockL1Client struct {
	chainID *big.Int
	Hash    common.Hash
	err     error
}

func (m *mockL1Client) ChainID(context.Context) (*big.Int, error) {
	return m.chainID, nil
}

func (m *mockL1Client) L1BlockRefByNumber(ctx context.Context, number uint64) (eth.L1BlockRef, error) {
	if m.err != nil {
		return eth.L1BlockRef{}, m.err
	}
	return eth.L1BlockRef{
		Hash:   m.Hash,
		Number: 100,
	}, nil
}

func TestValidateL1Config(t *testing.T) {
	config := randConfig()
	config.L1ChainID = big.NewInt(100)
	config.Genesis.L1.Number = 100
	config.Genesis.L1.Hash = [32]byte{0x01}
	mockClient := mockL1Client{chainID: big.NewInt(100), Hash: common.Hash{0x01}}
	err := config.ValidateL1Config(context.TODO(), testlog.Logger(t, log.LevelInfo), &mockClient)
	assert.NoError(t, err)
}

func TestValidateL1ConfigInvalidChainIdFails(t *testing.T) {
	config := randConfig()
	config.L1ChainID = big.NewInt(101)
	config.Genesis.L1.Number = 100
	config.Genesis.L1.Hash = [32]byte{0x01}
	mockClient := mockL1Client{chainID: big.NewInt(100), Hash: common.Hash{0x01}}
	logger := testlog.Logger(t, log.LevelInfo)
	err := config.ValidateL1Config(context.TODO(), logger, &mockClient)
	assert.Error(t, err)
	config.L1ChainID = big.NewInt(99)
	err = config.ValidateL1Config(context.TODO(), logger, &mockClient)
	assert.Error(t, err)
}

func TestValidateL1ConfigInvalidGenesisHashFails(t *testing.T) {
	config := randConfig()
	config.L1ChainID = big.NewInt(100)
	config.Genesis.L1.Number = 100
	config.Genesis.L1.Hash = [32]byte{0x00}
	mockClient := mockL1Client{chainID: big.NewInt(100), Hash: common.Hash{0x01}}
	logger := testlog.Logger(t, log.LevelInfo)
	err := config.ValidateL1Config(context.TODO(), logger, &mockClient)
	assert.Error(t, err)
	config.Genesis.L1.Hash = [32]byte{0x02}
	err = config.ValidateL1Config(context.TODO(), logger, &mockClient)
	assert.Error(t, err)
}

func TestCheckL1ChainID(t *testing.T) {
	config := randConfig()
	config.L1ChainID = big.NewInt(100)
	err := config.CheckL1ChainID(context.TODO(), &mockL1Client{chainID: big.NewInt(100)})
	assert.NoError(t, err)
	err = config.CheckL1ChainID(context.TODO(), &mockL1Client{chainID: big.NewInt(101)})
	assert.Error(t, err)
	err = config.CheckL1ChainID(context.TODO(), &mockL1Client{chainID: big.NewInt(99)})
	assert.Error(t, err)
}

func TestCheckL1BlockRefByNumber(t *testing.T) {
	logger := testlog.Logger(t, log.LevelInfo)
	config := randConfig()
	config.Genesis.L1.Number = 100
	config.Genesis.L1.Hash = [32]byte{0x01}
	mockClient := mockL1Client{chainID: big.NewInt(100), Hash: common.Hash{0x01}}
	err := config.CheckL1GenesisBlockHash(context.Background(), logger, &mockClient)
	assert.NoError(t, err)
	mockClient.Hash = common.Hash{0x02}
	err = config.CheckL1GenesisBlockHash(context.Background(), logger, &mockClient)
	assert.Error(t, err)
	mockClient.Hash = common.Hash{0x00}
	err = config.CheckL1GenesisBlockHash(context.Background(), logger, &mockClient)
	assert.Error(t, err)

	mockClient.err = errors.New("block not found")
	err = config.CheckL1GenesisBlockHash(context.Background(), logger, &mockClient)
	assert.NoError(t, err)

	// A history-pruned execution engine can no longer serve the genesis block; the configured
	// genesis hash is authoritative, so the check is skipped rather than failing.
	mockClient.err = historyPrunedRPCError{}
	err = config.CheckL1GenesisBlockHash(context.Background(), logger, &mockClient)
	assert.NoError(t, err)

	// Any other fetch error still fails the check.
	mockClient.err = errors.New("connection refused")
	err = config.CheckL1GenesisBlockHash(context.Background(), logger, &mockClient)
	assert.Error(t, err)
}

// TestRandomConfigDescription tests that the description works for different variations of a random rollup config.
func TestLogDescription(t *testing.T) {
	config := randConfig()

	t.Run("named L2", func(t *testing.T) {
		lgr, logs := testlog.CaptureLogger(t, log.LevelInfo)
		config.LogDescription(lgr, map[string]string{config.L2ChainID.String(): "foobar chain"})
		rec := logs.FindLog(testlog.NewMessageFilter("Rollup Config"))
		require.NotNil(t, rec)
		require.Equal(t, "foobar chain", rec.AttrValue("l2_network"))
	})

	t.Run("unnamed L2", func(t *testing.T) {
		lgr, logs := testlog.CaptureLogger(t, log.LevelInfo)
		config.LogDescription(lgr, nil)
		rec := logs.FindLog(testlog.NewMessageFilter("Rollup Config"))
		require.NotNil(t, rec)
		require.Nil(t, rec.AttrValue("l2_network"), "l2_network is omitted when no name is known")
		require.Equal(t, "unknown L1", rec.AttrValue("l1_network"))
	})
}

func TestRandomConfigDescription(t *testing.T) {
	t.Run("named L2", func(t *testing.T) {
		config := randConfig()
		out := config.Description(map[string]string{config.L2ChainID.String(): "foobar chain"})
		require.Contains(t, out, "foobar chain")
	})
	t.Run("named L1", func(t *testing.T) {
		config := randConfig()
		config.L1ChainID = big.NewInt(11155111)
		out := config.Description(map[string]string{config.L2ChainID.String(): "foobar chain"})
		require.Contains(t, out, "sepolia")
	})
	t.Run("unnamed", func(t *testing.T) {
		config := randConfig()
		out := config.Description(nil)
		require.Contains(t, out, "(unknown L1)")
		require.Contains(t, out, "(unknown L2)")
	})
}

// historyPrunedRPCError implements rpc.Error with the EIP-4444 history-pruned error code,
// matching what an execution engine with history expiry returns for an expired block.
type historyPrunedRPCError struct{}

func (historyPrunedRPCError) Error() string  { return "pruned history unavailable" }
func (historyPrunedRPCError) ErrorCode() int { return historyPrunedErrCode }

type mockL2Client struct {
	chainID *big.Int
	Hash    common.Hash
	err     error
}

func (m *mockL2Client) ChainID(context.Context) (*big.Int, error) {
	return m.chainID, nil
}

func (m *mockL2Client) L2BlockRefByNumber(ctx context.Context, number uint64) (eth.L2BlockRef, error) {
	if m.err != nil {
		return eth.L2BlockRef{}, m.err
	}
	return eth.L2BlockRef{
		Hash:   m.Hash,
		Number: 100,
	}, nil
}

func TestValidateL2Config(t *testing.T) {
	config := randConfig()
	config.L2ChainID = big.NewInt(100)
	config.Genesis.L2.Number = 100
	config.Genesis.L2.Hash = [32]byte{0x01}
	mockClient := mockL2Client{chainID: big.NewInt(100), Hash: common.Hash{0x01}}
	err := config.ValidateL2Config(context.TODO(), testlog.Logger(t, log.LevelInfo), &mockClient, false)
	assert.NoError(t, err)
}

func TestValidateL2ConfigInvalidChainIdFails(t *testing.T) {
	config := randConfig()
	config.L2ChainID = big.NewInt(101)
	config.Genesis.L2.Number = 100
	config.Genesis.L2.Hash = [32]byte{0x01}
	mockClient := mockL2Client{chainID: big.NewInt(100), Hash: common.Hash{0x01}}
	err := config.ValidateL2Config(context.TODO(), testlog.Logger(t, log.LevelInfo), &mockClient, false)
	assert.Error(t, err)
	config.L2ChainID = big.NewInt(99)
	err = config.ValidateL2Config(context.TODO(), testlog.Logger(t, log.LevelInfo), &mockClient, false)
	assert.Error(t, err)
}

func TestValidateL2ConfigInvalidGenesisHashFails(t *testing.T) {
	config := randConfig()
	config.L2ChainID = big.NewInt(100)
	config.Genesis.L2.Number = 100
	config.Genesis.L2.Hash = [32]byte{0x00}
	mockClient := mockL2Client{chainID: big.NewInt(100), Hash: common.Hash{0x01}}
	err := config.ValidateL2Config(context.TODO(), testlog.Logger(t, log.LevelInfo), &mockClient, false)
	assert.Error(t, err)
	config.Genesis.L2.Hash = [32]byte{0x02}
	err = config.ValidateL2Config(context.TODO(), testlog.Logger(t, log.LevelInfo), &mockClient, false)
	assert.Error(t, err)
}

func TestValidateL2ConfigInvalidGenesisHashSkippedWhenRequested(t *testing.T) {
	config := randConfig()
	config.L2ChainID = big.NewInt(100)
	config.Genesis.L2.Number = 100
	config.Genesis.L2.Hash = [32]byte{0x00}
	mockClient := mockL2Client{chainID: big.NewInt(100), Hash: common.Hash{0x01}}
	err := config.ValidateL2Config(context.TODO(), testlog.Logger(t, log.LevelInfo), &mockClient, true)
	assert.NoError(t, err)
	config.Genesis.L2.Hash = [32]byte{0x02}
	err = config.ValidateL2Config(context.TODO(), testlog.Logger(t, log.LevelInfo), &mockClient, true)
	assert.NoError(t, err)
}

func TestCheckL2ChainID(t *testing.T) {
	config := randConfig()
	config.L2ChainID = big.NewInt(100)
	err := config.CheckL2ChainID(context.TODO(), &mockL2Client{chainID: big.NewInt(100)})
	assert.NoError(t, err)
	err = config.CheckL2ChainID(context.TODO(), &mockL2Client{chainID: big.NewInt(101)})
	assert.Error(t, err)
	err = config.CheckL2ChainID(context.TODO(), &mockL2Client{chainID: big.NewInt(99)})
	assert.Error(t, err)
}

func TestCheckL2BlockRefByNumber(t *testing.T) {
	config := randConfig()
	config.Genesis.L2.Number = 100
	config.Genesis.L2.Hash = [32]byte{0x01}
	mockClient := mockL2Client{chainID: big.NewInt(100), Hash: common.Hash{0x01}}
	err := config.CheckL2GenesisBlockHash(context.TODO(), testlog.Logger(t, log.LevelInfo), &mockClient)
	assert.NoError(t, err)
	mockClient.Hash = common.Hash{0x02}
	err = config.CheckL2GenesisBlockHash(context.TODO(), testlog.Logger(t, log.LevelInfo), &mockClient)
	assert.Error(t, err)
	mockClient.Hash = common.Hash{0x00}
	err = config.CheckL2GenesisBlockHash(context.TODO(), testlog.Logger(t, log.LevelInfo), &mockClient)
	assert.Error(t, err)

	// A history-pruned execution engine can no longer serve the genesis block; the configured
	// genesis hash is authoritative, so the check is skipped rather than failing.
	mockClient = mockL2Client{chainID: big.NewInt(100), Hash: common.Hash{0x01}, err: historyPrunedRPCError{}}
	err = config.CheckL2GenesisBlockHash(context.TODO(), testlog.Logger(t, log.LevelInfo), &mockClient)
	assert.NoError(t, err)

	// A NotFound result is likewise tolerated.
	mockClient = mockL2Client{chainID: big.NewInt(100), Hash: common.Hash{0x01}, err: ethereum.NotFound}
	err = config.CheckL2GenesisBlockHash(context.TODO(), testlog.Logger(t, log.LevelInfo), &mockClient)
	assert.NoError(t, err)

	// Any other fetch error still fails the check.
	mockClient = mockL2Client{chainID: big.NewInt(100), Hash: common.Hash{0x01}, err: errors.New("connection refused")}
	err = config.CheckL2GenesisBlockHash(context.TODO(), testlog.Logger(t, log.LevelInfo), &mockClient)
	assert.Error(t, err)
}

func TestConfig_Check(t *testing.T) {
	tests := []struct {
		name        string
		modifier    func(cfg *Config)
		expectedErr error
	}{
		{
			name:        "BlockTimeZero",
			modifier:    func(cfg *Config) { cfg.BlockTime = 0 },
			expectedErr: ErrBlockTimeZero,
		},
		{
			name:        "SeqWindowSizeZero",
			modifier:    func(cfg *Config) { cfg.SeqWindowSize = 0 },
			expectedErr: ErrInvalidSeqWindowSize,
		},
		{
			name:        "SeqWindowSizeOne",
			modifier:    func(cfg *Config) { cfg.SeqWindowSize = 1 },
			expectedErr: ErrInvalidSeqWindowSize,
		},
		{
			name:        "NoL1Genesis",
			modifier:    func(cfg *Config) { cfg.Genesis.L1.Hash = common.Hash{} },
			expectedErr: ErrMissingGenesisL1Hash,
		},
		{
			name:        "NoL2Genesis",
			modifier:    func(cfg *Config) { cfg.Genesis.L2.Hash = common.Hash{} },
			expectedErr: ErrMissingGenesisL2Hash,
		},
		{
			name:        "GenesisHashesEqual",
			modifier:    func(cfg *Config) { cfg.Genesis.L2.Hash = cfg.Genesis.L1.Hash },
			expectedErr: ErrGenesisHashesSame,
		},
		{
			name:        "GenesisL2TimeZero",
			modifier:    func(cfg *Config) { cfg.Genesis.L2Time = 0 },
			expectedErr: ErrMissingGenesisL2Time,
		},
		{
			name:        "NoBatcherAddr",
			modifier:    func(cfg *Config) { cfg.Genesis.SystemConfig.BatcherAddr = common.Address{} },
			expectedErr: ErrMissingBatcherAddr,
		},
		{
			name:        "NoScalar",
			modifier:    func(cfg *Config) { cfg.Genesis.SystemConfig.Scalar = eth.Bytes32{} },
			expectedErr: ErrMissingScalar,
		},
		{
			name:        "NoGasLimit",
			modifier:    func(cfg *Config) { cfg.Genesis.SystemConfig.GasLimit = 0 },
			expectedErr: ErrMissingGasLimit,
		},
		{
			name:        "NoBatchInboxAddress",
			modifier:    func(cfg *Config) { cfg.BatchInboxAddress = common.Address{} },
			expectedErr: ErrMissingBatchInboxAddress,
		},
		{
			name:        "NoDepositContractAddress",
			modifier:    func(cfg *Config) { cfg.DepositContractAddress = common.Address{} },
			expectedErr: ErrMissingDepositContractAddress,
		},
		{
			name:        "NoL1ChainId",
			modifier:    func(cfg *Config) { cfg.L1ChainID = nil },
			expectedErr: ErrMissingL1ChainID,
		},
		{
			name:        "NoL2ChainId",
			modifier:    func(cfg *Config) { cfg.L2ChainID = nil },
			expectedErr: ErrMissingL2ChainID,
		},
		{
			name:        "ChainIDsEqual",
			modifier:    func(cfg *Config) { cfg.L2ChainID = cfg.L1ChainID },
			expectedErr: ErrChainIDsSame,
		},
		{
			name:        "L1ChainIdNegative",
			modifier:    func(cfg *Config) { cfg.L1ChainID = big.NewInt(-1) },
			expectedErr: ErrL1ChainIDNotPositive,
		},
		{
			name:        "L1ChainIdZero",
			modifier:    func(cfg *Config) { cfg.L1ChainID = big.NewInt(0) },
			expectedErr: ErrL1ChainIDNotPositive,
		},
		{
			name:        "L2ChainIdNegative",
			modifier:    func(cfg *Config) { cfg.L2ChainID = big.NewInt(-1) },
			expectedErr: ErrL2ChainIDNotPositive,
		},
		{
			name:        "L2ChainIdZero",
			modifier:    func(cfg *Config) { cfg.L2ChainID = big.NewInt(0) },
			expectedErr: ErrL2ChainIDNotPositive,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := randConfig()
			test.modifier(cfg)
			err := cfg.Check()
			assert.ErrorIs(t, err, test.expectedErr)
		})
	}

}

func TestTimestampForBlock(t *testing.T) {
	config := randConfig()

	tests := []struct {
		name              string
		genesisTime       uint64
		genesisBlock      uint64
		blockTime         uint64
		blockNum          uint64
		expectedBlockTime uint64
	}{
		{
			name:              "FirstBlock",
			genesisTime:       100,
			genesisBlock:      0,
			blockTime:         2,
			blockNum:          0,
			expectedBlockTime: 100,
		},
		{
			name:              "SecondBlock",
			genesisTime:       100,
			genesisBlock:      0,
			blockTime:         2,
			blockNum:          1,
			expectedBlockTime: 102,
		},
		{
			name:              "NBlock",
			genesisTime:       100,
			genesisBlock:      0,
			blockTime:         2,
			blockNum:          25,
			expectedBlockTime: 150,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(fmt.Sprintf("TestTimestampForBlock_%s", test.name), func(t *testing.T) {
			config.Genesis.L2Time = test.genesisTime
			config.Genesis.L2.Number = test.genesisBlock
			config.BlockTime = test.blockTime

			timestamp := config.TimestampForBlock(test.blockNum)
			assert.Equal(t, timestamp, test.expectedBlockTime)
		})
	}
}
