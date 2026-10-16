package txmgr

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v2"
)

var l1EthRpcValue = "http://localhost:9546"

func TestDefaultCLIOptionsMatchDefaultConfig(t *testing.T) {
	cfg := configForArgs()
	defaultCfg := NewCLIConfig(l1EthRpcValue, DefaultBatcherFlagValues)

	require.Equal(t, defaultCfg, cfg)
}

func TestDefaultConfigIsValid(t *testing.T) {
	cfg := NewCLIConfig(l1EthRpcValue, DefaultBatcherFlagValues)
	require.NoError(t, cfg.Check())
}

func configForArgs(args ...string) CLIConfig {
	app := cli.NewApp()
	// txmgr expects the --l1-eth-rpc option to be declared externally
	flags := append(CLIFlags("TEST_"), &cli.StringFlag{
		Name:  L1RPCFlagName,
		Value: l1EthRpcValue,
	})
	app.Flags = flags
	app.Name = "test"
	var config CLIConfig
	app.Action = func(ctx *cli.Context) error {
		config = ReadCLIConfig(ctx)
		return nil
	}
	_ = app.Run(args)
	return config
}

func TestCLIConfigCheck(t *testing.T) {
	tests := []struct {
		name      string
		override  func(*CLIConfig)
		errString string
	}{
		{
			name:      "empty L1 RPC URL",
			override:  func(c *CLIConfig) { c.L1RPCURL = "" },
			errString: "must provide a L1 RPC url",
		},
		{
			name:      "zero NumConfirmations",
			override:  func(c *CLIConfig) { c.NumConfirmations = 0 },
			errString: "NumConfirmations must not be 0",
		},
		{
			name:      "zero NetworkTimeout",
			override:  func(c *CLIConfig) { c.NetworkTimeout = 0 },
			errString: "must provide NetworkTimeout",
		},
		{
			name:      "zero FeeLimitMultiplier",
			override:  func(c *CLIConfig) { c.FeeLimitMultiplier = 0 },
			errString: "must provide FeeLimitMultiplier",
		},
		{
			name: "minBaseFee smaller than minTipCap",
			override: func(c *CLIConfig) {
				c.MinBaseFeeGwei = 1.0
				c.MinTipCapGwei = 2.0
			},
			errString: "minBaseFee smaller than minTipCap",
		},
		{
			name:      "zero ResubmissionTimeout",
			override:  func(c *CLIConfig) { c.ResubmissionTimeout = 0 },
			errString: "must provide ResubmissionTimeout",
		},
		{
			name:      "zero ReceiptQueryInterval",
			override:  func(c *CLIConfig) { c.ReceiptQueryInterval = 0 },
			errString: "must provide ReceiptQueryInterval",
		},
		{
			name:      "zero TxNotInMempoolTimeout",
			override:  func(c *CLIConfig) { c.TxNotInMempoolTimeout = 0 },
			errString: "must provide TxNotInMempoolTimeout",
		},
		{
			name:      "zero SafeAbortNonceTooLowCount",
			override:  func(c *CLIConfig) { c.SafeAbortNonceTooLowCount = 0 },
			errString: "SafeAbortNonceTooLowCount must not be 0",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := NewCLIConfig(l1EthRpcValue, DefaultBatcherFlagValues)
			tc.override(&cfg)
			err := cfg.Check()
			if tc.errString == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.errString)
			}
		})
	}
}
