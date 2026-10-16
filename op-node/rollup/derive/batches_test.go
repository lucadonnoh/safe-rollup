package derive

import (
	"context"
	"errors"
	"math/big"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ethereum-optimism/optimism/op-node/rollup"
	"github.com/ethereum-optimism/optimism/op-service/eth"
	"github.com/ethereum-optimism/optimism/op-service/log"
	"github.com/ethereum-optimism/optimism/op-service/testlog"
	"github.com/ethereum-optimism/optimism/op-service/testutils"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
)

type ValidBatchTestCase struct {
	Name           string
	L1Blocks       []eth.L1BlockRef
	L2SafeHead     eth.L2BlockRef
	Batch          BatchWithL1InclusionBlock
	Expected       BatchValidity
	ExpectedLog    string // log message that must be included
	NotExpectedLog string // log message that must not be included
}

const defaultBlockTime = 2

func TestValidBatch(t *testing.T) {
	defaultConf := func() *rollup.Config {
		return &rollup.Config{
			Genesis: rollup.Genesis{
				L2Time: 31, // a genesis time that itself does not align to make it more interesting
			},
			BlockTime:     defaultBlockTime,
			SeqWindowSize: 4,
		}
	}

	rng := rand.New(rand.NewSource(1234))

	chainId := new(big.Int).SetUint64(rng.Uint64())
	signer := types.NewPragueSigner(chainId)
	randTx := testutils.RandomTx(rng, new(big.Int).SetUint64(rng.Uint64()), signer)
	randTxData, _ := randTx.MarshalBinary()
	postExecTxData, err := testPostExecTx().MarshalBinary()
	require.NoError(t, err)

	l1A := testutils.RandomBlockRef(rng)
	l1B := eth.L1BlockRef{
		Hash:       testutils.RandomHash(rng),
		Number:     l1A.Number + 1,
		ParentHash: l1A.Hash,
		Time:       l1A.Time + 7,
	}
	l1C := eth.L1BlockRef{
		Hash:       testutils.RandomHash(rng),
		Number:     l1B.Number + 1,
		ParentHash: l1B.Hash,
		Time:       l1B.Time + 7,
	}
	l1D := eth.L1BlockRef{
		Hash:       testutils.RandomHash(rng),
		Number:     l1C.Number + 1,
		ParentHash: l1C.Hash,
		Time:       l1C.Time + 7,
	}
	l1E := eth.L1BlockRef{
		Hash:       testutils.RandomHash(rng),
		Number:     l1D.Number + 1,
		ParentHash: l1D.Hash,
		Time:       l1D.Time + 7,
	}
	l1F := eth.L1BlockRef{
		Hash:       testutils.RandomHash(rng),
		Number:     l1E.Number + 1,
		ParentHash: l1E.Hash,
		Time:       l1E.Time + 7,
	}

	l2A0 := eth.L2BlockRef{
		Hash:           testutils.RandomHash(rng),
		Number:         100,
		ParentHash:     testutils.RandomHash(rng),
		Time:           l1A.Time,
		L1Origin:       l1A.ID(),
		SequenceNumber: 0,
	}

	l2A1 := eth.L2BlockRef{
		Hash:           testutils.RandomHash(rng),
		Number:         l2A0.Number + 1,
		ParentHash:     l2A0.Hash,
		Time:           l2A0.Time + defaultBlockTime,
		L1Origin:       l1A.ID(),
		SequenceNumber: 1,
	}

	l2A2 := eth.L2BlockRef{
		Hash:           testutils.RandomHash(rng),
		Number:         l2A1.Number + 1,
		ParentHash:     l2A1.Hash,
		Time:           l2A1.Time + defaultBlockTime,
		L1Origin:       l1A.ID(),
		SequenceNumber: 2,
	}

	l2A3 := eth.L2BlockRef{
		Hash:           testutils.RandomHash(rng),
		Number:         l2A2.Number + 1,
		ParentHash:     l2A2.Hash,
		Time:           l2A2.Time + defaultBlockTime,
		L1Origin:       l1A.ID(),
		SequenceNumber: 3,
	}

	l2B0 := eth.L2BlockRef{
		Hash:           testutils.RandomHash(rng),
		Number:         l2A3.Number + 1,
		ParentHash:     l2A3.Hash,
		Time:           l2A3.Time + defaultBlockTime, // 8 seconds larger than l1A0, 1 larger than origin
		L1Origin:       l1B.ID(),
		SequenceNumber: 0,
	}

	l2B1 := eth.L2BlockRef{
		Hash:           testutils.RandomHash(rng),
		Number:         l2B0.Number + 1,
		ParentHash:     l2B0.Hash,
		Time:           l2B0.Time + defaultBlockTime,
		L1Origin:       l1B.ID(),
		SequenceNumber: 1,
	}

	l2B2 := eth.L2BlockRef{
		Hash:           testutils.RandomHash(rng),
		Number:         l2B1.Number + 1,
		ParentHash:     l2B1.Hash,
		Time:           l2B1.Time + defaultBlockTime,
		L1Origin:       l1B.ID(),
		SequenceNumber: 2,
	}

	l1X := eth.L1BlockRef{
		Hash:       testutils.RandomHash(rng),
		Number:     42,
		ParentHash: testutils.RandomHash(rng),
		Time:       10_000,
	}
	l1Y := eth.L1BlockRef{
		Hash:       testutils.RandomHash(rng),
		Number:     l1X.Number + 1,
		ParentHash: l1X.Hash,
		Time:       l1X.Time + 12,
	}
	l1Z := eth.L1BlockRef{
		Hash:       testutils.RandomHash(rng),
		Number:     l1Y.Number + 1,
		ParentHash: l1Y.Hash,
		Time:       l1Y.Time + 12,
	}
	l2X0 := eth.L2BlockRef{
		Hash:           testutils.RandomHash(rng),
		Number:         1000,
		ParentHash:     testutils.RandomHash(rng),
		Time:           10_000 + 24 + rollup.MaxSequencerDrift - 1, // add one block, and you get ahead of next l1 block by more than the drift
		L1Origin:       l1X.ID(),
		SequenceNumber: 0,
	}
	l2Y0 := eth.L2BlockRef{
		Hash:           testutils.RandomHash(rng),
		Number:         l2X0.Number + 1,
		ParentHash:     l2X0.Hash,
		Time:           l2X0.Time + defaultBlockTime, // exceeds sequencer time drift, forced to be empty block
		L1Origin:       l1Y.ID(),
		SequenceNumber: 0,
	}
	l2A4 := eth.L2BlockRef{
		Hash:           testutils.RandomHash(rng),
		Number:         l2A3.Number + 1,
		ParentHash:     l2A3.Hash,
		Time:           l2A3.Time + defaultBlockTime, // 4*2 = 8, higher than seq time drift
		L1Origin:       l1A.ID(),
		SequenceNumber: 4,
	}

	// l2D3 and l2D4 stay on origin l1A until l2D4 exceeds the max sequencer drift.
	l2D3 := eth.L2BlockRef{
		Hash:           testutils.RandomHash(rng),
		Number:         200,
		ParentHash:     testutils.RandomHash(rng),
		Time:           l1A.Time + rollup.MaxSequencerDrift,
		L1Origin:       l1A.ID(),
		SequenceNumber: 3,
	}
	l2D4 := eth.L2BlockRef{
		Hash:           testutils.RandomHash(rng),
		Number:         l2D3.Number + 1,
		ParentHash:     l2D3.Hash,
		Time:           l2D3.Time + defaultBlockTime, // exceeds the max sequencer drift
		L1Origin:       l1A.ID(),
		SequenceNumber: 4,
	}
	l1DLate := eth.L1BlockRef{
		Hash:       testutils.RandomHash(rng),
		Number:     l1A.Number + 1,
		ParentHash: l1A.Hash,
		Time:       l2D4.Time + 1, // too late for l2D4 to adopt yet
	}

	singularBatchTestCases := []ValidBatchTestCase{
		{
			Name:       "missing L1 info",
			L1Blocks:   []eth.L1BlockRef{},
			L2SafeHead: l2A0,
			Batch: BatchWithL1InclusionBlock{
				L1InclusionBlock: l1B,
				Batch: &SingularBatch{
					ParentHash:   l2A1.ParentHash,
					EpochNum:     rollup.Epoch(l2A1.L1Origin.Number),
					EpochHash:    l2A1.L1Origin.Hash,
					Timestamp:    l2A1.Time,
					Transactions: nil,
				},
			},
			Expected: BatchUndecided,
		},
		{
			Name:       "future timestamp with Holocene at L1 inc",
			L1Blocks:   []eth.L1BlockRef{l1A, l1B, l1C},
			L2SafeHead: l2A0,
			Batch: BatchWithL1InclusionBlock{
				L1InclusionBlock: l1B,
				Batch: &SingularBatch{
					ParentHash: l2A1.ParentHash,
					EpochNum:   rollup.Epoch(l2A1.L1Origin.Number),
					EpochHash:  l2A1.L1Origin.Hash,
					Timestamp:  l2A1.Time + 1, // 1 too high
				},
			},
			Expected:    BatchDrop,
			ExpectedLog: "dropping future batch",
		},
		{
			Name:       "past timestamp with Holocene at L1 inc",
			L1Blocks:   []eth.L1BlockRef{l1A, l1B, l1C},
			L2SafeHead: l2A0,
			Batch: BatchWithL1InclusionBlock{
				L1InclusionBlock: l1B,
				Batch: &SingularBatch{
					ParentHash: l2A1.ParentHash,
					EpochNum:   rollup.Epoch(l2A1.L1Origin.Number),
					EpochHash:  l2A1.L1Origin.Hash,
					Timestamp:  l2A0.Time, // repeating the same time
				},
			},
			Expected:    BatchPast,
			ExpectedLog: "dropping past batch with old timestamp",
		},
		{
			Name:       "misaligned timestamp",
			L1Blocks:   []eth.L1BlockRef{l1A, l1B, l1C},
			L2SafeHead: l2A0,
			Batch: BatchWithL1InclusionBlock{
				L1InclusionBlock: l1B,
				Batch: &SingularBatch{
					ParentHash:   l2A1.ParentHash,
					EpochNum:     rollup.Epoch(l2A1.L1Origin.Number),
					EpochHash:    l2A1.L1Origin.Hash,
					Timestamp:    l2A1.Time - 1, // block time is 2, so this is 1 too low
					Transactions: nil,
				},
			},
			Expected: BatchPast,
		},
		{
			Name:       "invalid parent block hash",
			L1Blocks:   []eth.L1BlockRef{l1A, l1B, l1C},
			L2SafeHead: l2A0,
			Batch: BatchWithL1InclusionBlock{
				L1InclusionBlock: l1B,
				Batch: &SingularBatch{
					ParentHash:   testutils.RandomHash(rng),
					EpochNum:     rollup.Epoch(l2A1.L1Origin.Number),
					EpochHash:    l2A1.L1Origin.Hash,
					Timestamp:    l2A1.Time,
					Transactions: nil,
				},
			},
			Expected: BatchDrop,
		},
		{
			Name:       "sequence window expired",
			L1Blocks:   []eth.L1BlockRef{l1A, l1B, l1C, l1D, l1E, l1F},
			L2SafeHead: l2A0,
			Batch: BatchWithL1InclusionBlock{
				L1InclusionBlock: l1F, // included in 5th block after epoch of batch, while seq window is 4
				Batch: &SingularBatch{
					ParentHash:   l2A1.ParentHash,
					EpochNum:     rollup.Epoch(l2A1.L1Origin.Number),
					EpochHash:    l2A1.L1Origin.Hash,
					Timestamp:    l2A1.Time,
					Transactions: nil,
				},
			},
			Expected: BatchDrop,
		},
		{
			Name:       "epoch too old, but good parent hash and timestamp", // repeat of now outdated l2A3 data
			L1Blocks:   []eth.L1BlockRef{l1B, l1C, l1D},
			L2SafeHead: l2B0, // we already moved on to B
			Batch: BatchWithL1InclusionBlock{
				L1InclusionBlock: l1C,
				Batch: &SingularBatch{
					ParentHash:   l2B0.Hash,                          // build on top of safe head to continue
					EpochNum:     rollup.Epoch(l2A3.L1Origin.Number), // epoch A is no longer valid
					EpochHash:    l2A3.L1Origin.Hash,
					Timestamp:    l2B0.Time + defaultBlockTime, // pass the timestamp check to get too epoch check
					Transactions: nil,
				},
			},
			Expected: BatchDrop,
		},
		{
			Name:       "insufficient L1 info for eager derivation",
			L1Blocks:   []eth.L1BlockRef{l1A}, // don't know about l1B yet
			L2SafeHead: l2A3,
			Batch: BatchWithL1InclusionBlock{
				L1InclusionBlock: l1C,
				Batch: &SingularBatch{
					ParentHash:   l2B0.ParentHash,
					EpochNum:     rollup.Epoch(l2B0.L1Origin.Number),
					EpochHash:    l2B0.L1Origin.Hash,
					Timestamp:    l2B0.Time,
					Transactions: nil,
				},
			},
			Expected: BatchUndecided,
		},
		{
			Name:       "epoch too new",
			L1Blocks:   []eth.L1BlockRef{l1A, l1B, l1C, l1D},
			L2SafeHead: l2A3,
			Batch: BatchWithL1InclusionBlock{
				L1InclusionBlock: l1D,
				Batch: &SingularBatch{
					ParentHash:   l2B0.ParentHash,
					EpochNum:     rollup.Epoch(l1C.Number), // invalid, we need to adopt epoch B before C
					EpochHash:    l1C.Hash,
					Timestamp:    l2B0.Time,
					Transactions: nil,
				},
			},
			Expected: BatchDrop,
		},
		{
			Name:       "epoch hash wrong",
			L1Blocks:   []eth.L1BlockRef{l1A, l1B},
			L2SafeHead: l2A3,
			Batch: BatchWithL1InclusionBlock{
				L1InclusionBlock: l1C,
				Batch: &SingularBatch{
					ParentHash:   l2B0.ParentHash,
					EpochNum:     rollup.Epoch(l2B0.L1Origin.Number),
					EpochHash:    l1A.Hash, // invalid, epoch hash should be l1B
					Timestamp:    l2B0.Time,
					Transactions: nil,
				},
			},
			Expected: BatchDrop,
		},
		{
			Name:       "sequencer time drift on same epoch with non-empty txs",
			L1Blocks:   []eth.L1BlockRef{l1A, l1B},
			L2SafeHead: l2D3,
			Batch: BatchWithL1InclusionBlock{
				L1InclusionBlock: l1B,
				Batch: &SingularBatch{ // we build l2D4, which has a timestamp of 2*4 = 8 higher than l2A0
					ParentHash:   l2D4.ParentHash,
					EpochNum:     rollup.Epoch(l2D4.L1Origin.Number),
					EpochHash:    l2D4.L1Origin.Hash,
					Timestamp:    l2D4.Time,
					Transactions: []hexutil.Bytes{[]byte("sequencer should not include this tx")},
				},
			},
			Expected: BatchDrop,
		},
		{ // this is the same test case as above, but with Fjord activated at the L1 origin, so accepted batch
			Name:       "no sequencer time drift on same epoch with non-empty txs and Fjord",
			L1Blocks:   []eth.L1BlockRef{l1A, l1B},
			L2SafeHead: l2A3,
			Batch: BatchWithL1InclusionBlock{
				L1InclusionBlock: l1B,
				Batch: &SingularBatch{ // we build l2A4, which has a timestamp of 2*4 = 8 higher than l2A0
					ParentHash:   l2A4.ParentHash,
					EpochNum:     rollup.Epoch(l2A4.L1Origin.Number),
					EpochHash:    l2A4.L1Origin.Hash,
					Timestamp:    l2A4.Time,
					Transactions: []hexutil.Bytes{[]byte("sequencer should include this tx")},
				},
			},
			Expected: BatchAccept,
		},
		{
			Name:       "sequencer time drift on changing epoch with non-empty txs",
			L1Blocks:   []eth.L1BlockRef{l1X, l1Y, l1Z},
			L2SafeHead: l2X0,
			Batch: BatchWithL1InclusionBlock{
				L1InclusionBlock: l1Z,
				Batch: &SingularBatch{
					ParentHash:   l2Y0.ParentHash,
					EpochNum:     rollup.Epoch(l2Y0.L1Origin.Number),
					EpochHash:    l2Y0.L1Origin.Hash,
					Timestamp:    l2Y0.Time, // valid, but more than 6 ahead of l1Y.Time
					Transactions: []hexutil.Bytes{[]byte("sequencer should not include this tx")},
				},
			},
			Expected: BatchDrop,
		},
		{
			Name:       "sequencer time drift on same epoch with empty txs and late next epoch",
			L1Blocks:   []eth.L1BlockRef{l1A, l1DLate},
			L2SafeHead: l2D3,
			Batch: BatchWithL1InclusionBlock{
				L1InclusionBlock: l1DLate,
				Batch: &SingularBatch{ // l2D4 time < l1DLate time, so we cannot adopt origin B yet
					ParentHash:   l2D4.ParentHash,
					EpochNum:     rollup.Epoch(l2D4.L1Origin.Number),
					EpochHash:    l2D4.L1Origin.Hash,
					Timestamp:    l2D4.Time,
					Transactions: nil,
				},
			},
			Expected: BatchAccept, // accepted because empty & preserving L2 time invariant
		},
		{
			Name:       "sequencer time drift on changing epoch with empty txs",
			L1Blocks:   []eth.L1BlockRef{l1X, l1Y, l1Z},
			L2SafeHead: l2X0,
			Batch: BatchWithL1InclusionBlock{
				L1InclusionBlock: l1Z,
				Batch: &SingularBatch{
					ParentHash:   l2Y0.ParentHash,
					EpochNum:     rollup.Epoch(l2Y0.L1Origin.Number),
					EpochHash:    l2Y0.L1Origin.Hash,
					Timestamp:    l2Y0.Time, // valid, but more than 6 ahead of l1Y.Time
					Transactions: nil,
				},
			},
			Expected: BatchAccept, // accepted because empty & still advancing epoch
		},
		{
			Name:       "sequencer time drift on same epoch with empty txs and no next epoch in sight yet",
			L1Blocks:   []eth.L1BlockRef{l1A},
			L2SafeHead: l2D3,
			Batch: BatchWithL1InclusionBlock{
				L1InclusionBlock: l1B,
				Batch: &SingularBatch{ // we build l2D4, which has a timestamp of 2*4 = 8 higher than l2A0
					ParentHash:   l2D4.ParentHash,
					EpochNum:     rollup.Epoch(l2D4.L1Origin.Number),
					EpochHash:    l2D4.L1Origin.Hash,
					Timestamp:    l2D4.Time,
					Transactions: nil,
				},
			},
			Expected: BatchUndecided, // we have to wait till the next epoch is in sight to check the time
		},
		{
			Name:       "sequencer time drift on same epoch with empty txs and but in-sight epoch that invalidates it",
			L1Blocks:   []eth.L1BlockRef{l1A, l1B, l1C},
			L2SafeHead: l2D3,
			Batch: BatchWithL1InclusionBlock{
				L1InclusionBlock: l1C,
				Batch: &SingularBatch{ // we build l2D4, which has a timestamp of 2*4 = 8 higher than l2A0
					ParentHash:   l2D4.ParentHash,
					EpochNum:     rollup.Epoch(l2D4.L1Origin.Number),
					EpochHash:    l2D4.L1Origin.Hash,
					Timestamp:    l2D4.Time,
					Transactions: nil,
				},
			},
			Expected: BatchDrop, // dropped because it could have advanced the epoch to B
		},
		{
			Name:       "empty tx included",
			L1Blocks:   []eth.L1BlockRef{l1A, l1B},
			L2SafeHead: l2A0,
			Batch: BatchWithL1InclusionBlock{
				L1InclusionBlock: l1B,
				Batch: &SingularBatch{
					ParentHash: l2A1.ParentHash,
					EpochNum:   rollup.Epoch(l2A1.L1Origin.Number),
					EpochHash:  l2A1.L1Origin.Hash,
					Timestamp:  l2A1.Time,
					Transactions: []hexutil.Bytes{
						[]byte{}, // empty tx data
					},
				},
			},
			Expected: BatchDrop,
		},
		{
			Name:       "deposit tx included",
			L1Blocks:   []eth.L1BlockRef{l1A, l1B},
			L2SafeHead: l2A0,
			Batch: BatchWithL1InclusionBlock{
				L1InclusionBlock: l1B,
				Batch: &SingularBatch{
					ParentHash: l2A1.ParentHash,
					EpochNum:   rollup.Epoch(l2A1.L1Origin.Number),
					EpochHash:  l2A1.L1Origin.Hash,
					Timestamp:  l2A1.Time,
					Transactions: []hexutil.Bytes{
						[]byte{types.DepositTxType, 0}, // piece of data alike to a deposit
					},
				},
			},
			Expected:    BatchDrop,
			ExpectedLog: "sequencers may not embed any deposits into batch data, but found tx that has one",
		},
		{
			Name:       "postExec tx included pre-SDM",
			L1Blocks:   []eth.L1BlockRef{l1A, l1B},
			L2SafeHead: l2A0,
			Batch: BatchWithL1InclusionBlock{
				L1InclusionBlock: l1B,
				Batch: &SingularBatch{
					ParentHash: l2A1.ParentHash,
					EpochNum:   rollup.Epoch(l2A1.L1Origin.Number),
					EpochHash:  l2A1.L1Origin.Hash,
					Timestamp:  l2A1.Time,
					Transactions: []hexutil.Bytes{
						postExecTxData,
					},
				},
			},
			Expected:    BatchDrop,
			ExpectedLog: "sequencers may not embed any PostExec transactions",
		},
		{
			Name:       "valid batch same epoch",
			L1Blocks:   []eth.L1BlockRef{l1A, l1B},
			L2SafeHead: l2A0,
			Batch: BatchWithL1InclusionBlock{
				L1InclusionBlock: l1B,
				Batch: &SingularBatch{
					ParentHash: l2A1.ParentHash,
					EpochNum:   rollup.Epoch(l2A1.L1Origin.Number),
					EpochHash:  l2A1.L1Origin.Hash,
					Timestamp:  l2A1.Time,
					Transactions: []hexutil.Bytes{
						[]byte{0x02, 0x42, 0x13, 0x37},
						[]byte{0x02, 0xde, 0xad, 0xbe, 0xef},
					},
				},
			},
			Expected: BatchAccept,
		},
		{
			Name:       "valid batch changing epoch",
			L1Blocks:   []eth.L1BlockRef{l1A, l1B, l1C},
			L2SafeHead: l2A3,
			Batch: BatchWithL1InclusionBlock{
				L1InclusionBlock: l1C,
				Batch: &SingularBatch{
					ParentHash: l2B0.ParentHash,
					EpochNum:   rollup.Epoch(l2B0.L1Origin.Number),
					EpochHash:  l2B0.L1Origin.Hash,
					Timestamp:  l2B0.Time,
					Transactions: []hexutil.Bytes{
						[]byte{0x02, 0x42, 0x13, 0x37},
						[]byte{0x02, 0xde, 0xad, 0xbe, 0xef},
					},
				},
			},
			Expected: BatchAccept,
		},
		{
			Name:       "batch with L2 time before L1 time",
			L1Blocks:   []eth.L1BlockRef{l1A, l1B, l1C},
			L2SafeHead: l2A2,
			Batch: BatchWithL1InclusionBlock{
				L1InclusionBlock: l1B,
				Batch: &SingularBatch{ // we build l2B0', which starts a new epoch too early
					ParentHash:   l2A2.Hash,
					EpochNum:     rollup.Epoch(l2B0.L1Origin.Number),
					EpochHash:    l2B0.L1Origin.Hash,
					Timestamp:    l2A2.Time + defaultBlockTime,
					Transactions: nil,
				},
			},
			Expected: BatchDrop,
		},
	}

	// Log level can be increased for debugging purposes
	logger, logs := testlog.CaptureLogger(t, log.LevelTrace)

	l2Client := testutils.MockL2Client{}
	var nilErr error
	tempErr := errors.New("temp error")
	// will return an error for block #99 (parent of l2A0)
	l2Client.Mock.On("L2BlockRefByNumber", l2A0.Number-1).Return(eth.L2BlockRef{}, &tempErr)
	// will return an error for l2A3
	l2Client.Mock.On("PayloadByNumber", l2A3.Number).Return(&eth.ExecutionPayloadEnvelope{}, &tempErr)

	// make payloads for L2 blocks and set as expected return value of MockL2Client
	for _, l2Block := range []eth.L2BlockRef{l2A0, l2A1, l2A2, l2B0} {
		l2Client.ExpectL2BlockRefByNumber(l2Block.Number, l2Block, nil)
		txData := l1InfoDepositTx(t, l2Block.L1Origin.Number)
		payload := eth.ExecutionPayloadEnvelope{
			ExecutionPayload: &eth.ExecutionPayload{
				ParentHash:   l2Block.ParentHash,
				BlockNumber:  hexutil.Uint64(l2Block.Number),
				Timestamp:    hexutil.Uint64(l2Block.Time),
				BlockHash:    l2Block.Hash,
				Transactions: []hexutil.Bytes{txData},
			},
		}
		l2Client.Mock.On("L2BlockRefByNumber", l2Block.Number).Return(l2Block, &nilErr)
		l2Client.Mock.On("PayloadByNumber", l2Block.Number).Return(&payload, &nilErr)
	}

	runTestCase := func(t *testing.T, testCase ValidBatchTestCase) {
		ctx := context.Background()
		rcfg := defaultConf()
		var validity BatchValidity
		if batch, ok := testCase.Batch.AsSingularBatch(); ok {
			validity = checkSingularBatch(rcfg, logger, testCase.L1Blocks, testCase.L2SafeHead, batch, testCase.Batch.L1InclusionBlock)
		} else {
			batch, ok := testCase.Batch.AsSpanBatch()
			require.True(t, ok)
			validity = checkSpanBatchHolocene(ctx, rcfg, logger, testCase.L1Blocks, testCase.L2SafeHead, batch, testCase.Batch.L1InclusionBlock, &l2Client)
		}
		require.Equal(t, testCase.Expected, validity, "batch check must return expected validity level")
		if expLog := testCase.ExpectedLog; expLog != "" {
			// Check if ExpectedLog is contained in the log buffer
			containsFilter := testlog.NewMessageContainsFilter(expLog)
			if l := logs.FindLog(containsFilter); l == nil {
				t.Errorf("Expected log message was not logged: %q", expLog)
			}
		}
		if notExpLog := testCase.NotExpectedLog; notExpLog != "" {
			// Check if NotExpectedLog is contained in the log buffer
			containsFilter := testlog.NewMessageContainsFilter(notExpLog)
			if l := logs.FindLog(containsFilter); l != nil {
				t.Errorf("Unexpected log message containing %q was logged: %q", notExpLog, l.Message)
			}
		}
		logs.Clear()
	}

	// Run singular batch test cases
	for _, testCase := range singularBatchTestCases {
		t.Run("singular_"+testCase.Name, func(t *testing.T) {
			runTestCase(t, testCase)
		})
	}

	// ====== Test different TX for overlapping batches ======
	l2Client.ExpectL2BlockRefByNumber(l2B1.Number, l2B1, nil)
	txData := l1InfoDepositTx(t, l2B1.L1Origin.Number)
	randTx = testutils.RandomTx(rng, new(big.Int).SetUint64(rng.Uint64()), signer)
	randTxData, _ = randTx.MarshalBinary()
	payload := eth.ExecutionPayloadEnvelope{
		ExecutionPayload: &eth.ExecutionPayload{
			ParentHash:   l2B0.Hash,
			BlockNumber:  hexutil.Uint64(l2B1.Number),
			Timestamp:    hexutil.Uint64(l2B1.Time),
			BlockHash:    l2B1.Hash,
			Transactions: []hexutil.Bytes{txData, randTxData},
		},
	}
	l2Client.Mock.On("PayloadByNumber", l2B1.Number).Return(&payload, &nilErr).Once()

	randTx = testutils.RandomTx(rng, new(big.Int).SetUint64(rng.Uint64()), signer)
	randTxData, _ = randTx.MarshalBinary()
	differentTxtestCase := ValidBatchTestCase{
		Name:       "different_tx_overlapping_batch",
		L1Blocks:   []eth.L1BlockRef{l1B},
		L2SafeHead: l2B1,
		Batch: BatchWithL1InclusionBlock{
			L1InclusionBlock: l1B,
			Batch: initializedSpanBatch([]*SingularBatch{
				{
					ParentHash:   l2B0.Hash,
					EpochNum:     rollup.Epoch(l2B1.L1Origin.Number),
					EpochHash:    l2B1.L1Origin.Hash,
					Timestamp:    l2B1.Time,
					Transactions: []hexutil.Bytes{randTxData}, // Random generated TX that does not match overlapping block
				},
				{
					ParentHash:   l2B1.Hash,
					EpochNum:     rollup.Epoch(l2B2.L1Origin.Number),
					EpochHash:    l2B2.L1Origin.Hash,
					Timestamp:    l2B2.Time,
					Transactions: nil,
				},
			}, uint64(0), big.NewInt(0)),
		},
		Expected:    BatchDrop,
		ExpectedLog: "overlapped block's transaction does not match",
	}

	t.Run(differentTxtestCase.Name, func(t *testing.T) {
		runTestCase(t, differentTxtestCase)
	})

	// ====== Test invalid TX for overlapping batches ======
	payload = eth.ExecutionPayloadEnvelope{
		ExecutionPayload: &eth.ExecutionPayload{
			ParentHash:  l2B0.Hash,
			BlockNumber: hexutil.Uint64(l2B1.Number),
			Timestamp:   hexutil.Uint64(l2B1.Time),
			BlockHash:   l2B1.Hash,
			// First TX is not a deposit TX. it will make error when extracting L2BlockRef from the payload
			Transactions: []hexutil.Bytes{randTxData},
		},
	}
	l2Client.Mock.On("PayloadByNumber", l2B1.Number).Return(&payload, &nilErr).Once()

	invalidTxTestCase := ValidBatchTestCase{
		Name:       "invalid_tx_overlapping_batch",
		L1Blocks:   []eth.L1BlockRef{l1B},
		L2SafeHead: l2B1,
		Batch: BatchWithL1InclusionBlock{
			L1InclusionBlock: l1B,
			Batch: initializedSpanBatch([]*SingularBatch{
				{
					ParentHash:   l2B0.Hash,
					EpochNum:     rollup.Epoch(l2B1.L1Origin.Number),
					EpochHash:    l2B1.L1Origin.Hash,
					Timestamp:    l2B1.Time,
					Transactions: []hexutil.Bytes{randTxData},
				},
				{
					ParentHash:   l2B1.Hash,
					EpochNum:     rollup.Epoch(l2B2.L1Origin.Number),
					EpochHash:    l2B2.L1Origin.Hash,
					Timestamp:    l2B2.Time,
					Transactions: nil,
				},
			}, uint64(0), big.NewInt(0)),
		},
		Expected:    BatchDrop,
		ExpectedLog: "failed to extract L2BlockRef from execution payload",
	}

	t.Run(invalidTxTestCase.Name, func(t *testing.T) {
		runTestCase(t, invalidTxTestCase)
	})
}
