package metrics

import (
	"io"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"

	"github.com/ethereum-optimism/optimism/op-node/rollup/derive"
	"github.com/ethereum-optimism/optimism/op-service/eth"
	"github.com/ethereum-optimism/optimism/op-service/log"
	opmetrics "github.com/ethereum-optimism/optimism/op-service/metrics"
	txmetrics "github.com/ethereum-optimism/optimism/op-service/txmgr/metrics"
)

type noopMetrics struct {
	opmetrics.NoopRefMetrics
	txmetrics.NoopTxMetrics
}

var NoopMetrics Metricer = new(noopMetrics)

func (*noopMetrics) Document() []opmetrics.DocumentedMetric { return nil }

func (*noopMetrics) RecordInfo(version string) {}
func (*noopMetrics) RecordUp()                 {}

func (*noopMetrics) RecordLatestL1Block(l1ref eth.L1BlockRef)               {}
func (*noopMetrics) RecordL2BlocksLoaded(eth.L2BlockRef)                    {}
func (*noopMetrics) RecordChannelOpened(derive.ChannelID, int)              {}
func (*noopMetrics) RecordL2BlocksAdded(eth.L2BlockRef, int, int, int, int) {}
func (*noopMetrics) RecordL2BlockInPendingQueue(uint64, uint64)             {}
func (*noopMetrics) RecordL2BlockInChannel(uint64, uint64)                  {}
func (*noopMetrics) RecordPendingBlockPruned(uint64, uint64)                {}

func (*noopMetrics) RecordChannelClosed(derive.ChannelID, int, int, int, int, error) {}

func (*noopMetrics) RecordChannelFullySubmitted(derive.ChannelID) {}
func (*noopMetrics) RecordChannelTimedOut(derive.ChannelID)       {}
func (*noopMetrics) RecordChannelQueueLength(int)                 {}

func (*noopMetrics) RecordUnsafeDABytes(int64) {}

func (*noopMetrics) RecordBatchTxSubmitted() {}
func (*noopMetrics) RecordBatchTxSuccess()   {}
func (*noopMetrics) RecordBatchTxFailed()    {}
func (*noopMetrics) StartBalanceMetrics(log.Logger, *ethclient.Client, common.Address) io.Closer {
	return nil
}
func (nm *noopMetrics) PendingDABytes() float64 {
	return 0.0
}

func (*noopMetrics) ClearAllStateMetrics() {}
