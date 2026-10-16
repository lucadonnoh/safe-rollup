package flags

import (
	"github.com/urfave/cli/v2"

	opservice "github.com/ethereum-optimism/optimism/op-service"
)

const (
	RollupConfigFlagName = "rollup.config"
)

func CLIFlags(envPrefix string, category string) []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:     RollupConfigFlagName,
			Usage:    "Rollup chain parameters",
			EnvVars:  opservice.PrefixEnvVar(envPrefix, "ROLLUP_CONFIG"),
			Category: category,
		},
	}
}
