package config

import (
	"context"
	"fmt"
	"time"

	altda "github.com/ethereum-optimism/optimism/op-alt-da"
	"github.com/ethereum-optimism/optimism/op-node/flags"
	"github.com/ethereum-optimism/optimism/op-node/node/tracer"
	"github.com/ethereum-optimism/optimism/op-node/p2p"
	"github.com/ethereum-optimism/optimism/op-node/rollup"
	"github.com/ethereum-optimism/optimism/op-node/rollup/driver"
	"github.com/ethereum-optimism/optimism/op-node/rollup/sync"
	"github.com/ethereum-optimism/optimism/op-service/log"
	opmetrics "github.com/ethereum-optimism/optimism/op-service/metrics"
	"github.com/ethereum-optimism/optimism/op-service/oppprof"
	oprpc "github.com/ethereum-optimism/optimism/op-service/rpc"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/params"
)

type Config struct {
	L1 L1EndpointSetup
	L2 L2EndpointSetup

	Driver driver.Config

	Rollup rollup.Config

	L1ChainConfig *params.ChainConfig

	// P2PSigner will be used for signing off on published content
	// if the node is sequencing and if the p2p stack is enabled
	P2PSigner p2p.SignerSetup

	RPC oprpc.CLIConfig

	P2P p2p.SetupP2P

	Metrics opmetrics.CLIConfig

	Pprof oppprof.CLIConfig

	// Used to poll the L1 for new finalized or safe blocks
	L1EpochPollInterval time.Duration

	ConfigPersistence ConfigPersistence

	// P2PSequencerAddress is the address that signs gossiped unsafe blocks.
	P2PSequencerAddress common.Address

	// Optional
	Tracer tracer.Tracer

	// Optional

	Sync sync.Config

	// Cancel to request a premature shutdown of the node itself. This may be nil.
	Cancel context.CancelCauseFunc

	// AltDA config
	AltDA altda.CLIConfig
}

func (cfg *Config) LoadPersisted(log log.Logger) error {
	if !cfg.Driver.SequencerEnabled {
		return nil
	}
	if state, err := cfg.ConfigPersistence.SequencerState(); err != nil {
		return err
	} else if state != StateUnset {
		stopped := state == StateStopped
		if stopped != cfg.Driver.SequencerStopped {
			log.Warn(fmt.Sprintf("Overriding %v with persisted state", flags.SequencerStoppedFlag.Name), "stopped", stopped)
		}
		cfg.Driver.SequencerStopped = stopped
	} else {
		log.Info("No persisted sequencer state loaded")
	}
	return nil
}

// Check verifies that the given configuration makes sense
func (cfg *Config) Check() error {
	if err := cfg.L1.Check(); err != nil {
		return fmt.Errorf("l1 endpoint config error: %w", err)
	}
	if err := cfg.L2.Check(); err != nil {
		return fmt.Errorf("l2 endpoint config error: %w", err)
	}
	if cfg.L1ChainConfig == nil {
		return fmt.Errorf("missing L1ChainConfig")
	}
	if err := cfg.Rollup.Check(); err != nil {
		return fmt.Errorf("rollup config error: %w", err)
	}
	if err := cfg.Metrics.Check(); err != nil {
		return fmt.Errorf("metrics config error: %w", err)
	}
	if err := cfg.Pprof.Check(); err != nil {
		return fmt.Errorf("pprof config error: %w", err)
	}
	if cfg.P2P != nil {
		if err := cfg.P2P.Check(); err != nil {
			return fmt.Errorf("p2p config error: %w", err)
		}
	}
	if err := cfg.AltDA.Check(); err != nil {
		return fmt.Errorf("altDA config error: %w", err)
	}
	return nil
}

func (cfg *Config) P2PEnabled() bool {
	return cfg.P2P != nil && !cfg.P2P.Disabled()
}
