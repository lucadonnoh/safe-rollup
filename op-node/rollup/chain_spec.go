package rollup

import (
	"math/big"

	"github.com/ethereum-optimism/optimism/op-node/params"
)

// MaxRLPBytesPerChannel is the maximum amount of bytes that will be read from a channel. This
// limit is set when decoding the RLP.
const MaxRLPBytesPerChannel = 100_000_000

// MaxSequencerDrift is the maximum number of seconds an L2 block may be ahead of its L1 origin.
const MaxSequencerDrift = 1800

// ChainSpec exposes the protocol constants derivation uses. Every fork up to Jovian is active from
// genesis, so none of them depend on the block timestamp.
type ChainSpec struct {
	config *Config
}

func NewChainSpec(config *Config) *ChainSpec {
	return &ChainSpec{config: config}
}

// L2ChainID returns the chain ID of the L2 chain.
func (s *ChainSpec) L2ChainID() *big.Int {
	return s.config.L2ChainID
}

// L2GenesisTime returns the genesis time of the L2 chain.
func (s *ChainSpec) L2GenesisTime() uint64 {
	return s.config.Genesis.L2Time
}

// ChannelTimeout returns the number of L1 blocks a channel may stay open.
func (s *ChainSpec) ChannelTimeout(t uint64) uint64 {
	return params.ChannelTimeoutGranite
}

// MaxRLPBytesPerChannel returns the maximum amount of bytes that will be read from a channel.
func (s *ChainSpec) MaxRLPBytesPerChannel(t uint64) uint64 {
	return MaxRLPBytesPerChannel
}

// MaxSequencerDrift returns the maximum sequencer drift.
func (s *ChainSpec) MaxSequencerDrift(t uint64) uint64 {
	return MaxSequencerDrift
}
