package altda

import (
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/urfave/cli/v2"

	"github.com/ethereum-optimism/optimism/op-service/cliiface"
)

var (
	DaServerAddressFlagName       = "altda.da-server"
	PutTimeoutFlagName            = "altda.put-timeout"
	GetTimeoutFlagName            = "altda.get-timeout"
	MaxConcurrentRequestsFlagName = "altda.max-concurrent-da-requests"
)

func altDAEnvs(envprefix, v string) []string {
	return []string{envprefix + "_ALTDA_" + v}
}

func CLIFlags(envPrefix string, category string) []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:     DaServerAddressFlagName,
			Usage:    "HTTP address of the DA server",
			EnvVars:  altDAEnvs(envPrefix, "DA_SERVER"),
			Category: category,
		},
		&cli.DurationFlag{
			Name:     PutTimeoutFlagName,
			Usage:    "Timeout for put requests. 0 means no timeout.",
			EnvVars:  altDAEnvs(envPrefix, "PUT_TIMEOUT"),
			Category: category,
		},
		&cli.DurationFlag{
			Name:     GetTimeoutFlagName,
			Usage:    "Timeout for get requests. 0 means no timeout.",
			EnvVars:  altDAEnvs(envPrefix, "GET_TIMEOUT"),
			Category: category,
		},
		&cli.Uint64Flag{
			Name:     MaxConcurrentRequestsFlagName,
			Usage:    "Maximum number of concurrent requests to the DA server",
			Value:    1,
			EnvVars:  altDAEnvs(envPrefix, "MAX_CONCURRENT_DA_REQUESTS"),
			Category: category,
		},
	}
}

type CLIConfig struct {
	DAServerURL           string
	PutTimeout            time.Duration
	GetTimeout            time.Duration
	MaxConcurrentRequests uint64
}

func (c CLIConfig) Check() error {
	if c.DAServerURL == "" {
		return errors.New("DA server URL is required")
	}
	if _, err := url.Parse(c.DAServerURL); err != nil {
		return fmt.Errorf("DA server URL is invalid: %w", err)
	}
	return nil
}

func (c CLIConfig) NewDAClient() *DAClient {
	return &DAClient{url: c.DAServerURL, getTimeout: c.GetTimeout, putTimeout: c.PutTimeout}
}

func ReadCLIConfig(c cliiface.Context) CLIConfig {
	return CLIConfig{
		DAServerURL:           c.String(DaServerAddressFlagName),
		PutTimeout:            c.Duration(PutTimeoutFlagName),
		GetTimeout:            c.Duration(GetTimeoutFlagName),
		MaxConcurrentRequests: c.Uint64(MaxConcurrentRequestsFlagName),
	}
}
