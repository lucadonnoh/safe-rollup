package derive

import (
	"context"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	altda "github.com/ethereum-optimism/optimism/op-alt-da"
	optypes "github.com/ethereum-optimism/optimism/op-core/types"
	"github.com/ethereum-optimism/optimism/op-node/rollup"
	"github.com/ethereum-optimism/optimism/op-service/eth"
	"github.com/ethereum-optimism/optimism/op-service/log"
)

type DataIter interface {
	Next(ctx context.Context) (eth.Data, error)
}

type L1TransactionFetcher interface {
	InfoAndTxsByHash(ctx context.Context, hash common.Hash) (eth.BlockInfo, types.Transactions, error)
}

type AltDAInputFetcher interface {
	// GetInput fetches the data for the given commitment from the DA service.
	GetInput(ctx context.Context, c altda.Commitment) ([]byte, error)
}

// DataSourceFactory reads the batcher's commitments from a given block and resolves them through
// the DA service.
// This is not a stage in the pipeline, but a wrapper for another stage in the pipeline
type DataSourceFactory struct {
	log          log.Logger
	dsCfg        DataSourceConfig
	fetcher      L1Fetcher
	altDAFetcher AltDAInputFetcher
}

func NewDataSourceFactory(log log.Logger, cfg *rollup.Config, fetcher L1Fetcher, altDAFetcher AltDAInputFetcher) *DataSourceFactory {
	config := DataSourceConfig{
		l1Signer:          cfg.L1Signer(),
		batchInboxAddress: cfg.BatchInboxAddress,
	}
	return &DataSourceFactory{
		log:          log,
		dsCfg:        config,
		fetcher:      fetcher,
		altDAFetcher: altDAFetcher,
	}
}

// OpenData returns the appropriate data source for the L1 block `ref`.
func (ds *DataSourceFactory) OpenData(ctx context.Context, ref eth.L1BlockRef, batcherAddr common.Address) (DataIter, error) {
	src := NewCalldataSource(ctx, ds.log, ds.dsCfg, ds.fetcher, ref, batcherAddr)
	return NewAltDADataSource(ds.log, src, ds.altDAFetcher), nil
}

// DataSourceConfig regroups the mandatory rollup.Config fields needed for DataFromEVMTransactions.
type DataSourceConfig struct {
	l1Signer          types.Signer
	batchInboxAddress common.Address
}

// isValidBatchTx returns true if:
//  1. the transaction type is any of Legacy, ACL, DynamicFee, Blob, or Deposit (for L3s).
//  2. the transaction has a To() address that matches the batch inbox address, and
//  3. the transaction has a valid signature from the batcher address
func isValidBatchTx(tx *types.Transaction, l1Signer types.Signer, batchInboxAddr, batcherAddr common.Address, logger log.Logger) bool {
	// For now, we want to disallow the SetCodeTx type or any future types.
	if tx.Type() > types.BlobTxType && tx.Type() != optypes.DepositTxType {
		return false
	}

	to := tx.To()
	if to == nil || *to != batchInboxAddr {
		return false
	}
	seqDataSubmitter, err := l1Signer.Sender(tx) // optimization: only derive sender if To is correct
	if err != nil {
		logger.Warn("tx in inbox with invalid signature", "hash", tx.Hash(), "err", err)
		return false
	}
	// some random L1 user might have sent a transaction to our batch inbox, ignore them
	if seqDataSubmitter != batcherAddr {
		logger.Warn("tx in inbox with unauthorized submitter", "addr", seqDataSubmitter, "hash", tx.Hash(), "err", err)
		return false
	}
	return true
}
