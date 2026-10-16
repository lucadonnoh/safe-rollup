package main

import (
	"errors"

	"github.com/urfave/cli/v2"

	opservice "github.com/ethereum-optimism/optimism/op-service"
	"github.com/ethereum-optimism/optimism/op-service/log/logcli"
)

const EnvVarPrefix = "OP_ALTDA_SERVER"

func prefixEnvVars(name string) []string {
	return opservice.PrefixEnvVar(EnvVarPrefix, name)
}

var (
	ListenAddrFlag = &cli.StringFlag{
		Name:    "addr",
		Usage:   "server listening address",
		Value:   "127.0.0.1",
		EnvVars: prefixEnvVars("ADDR"),
	}
	PortFlag = &cli.IntFlag{
		Name:    "port",
		Usage:   "server listening port",
		Value:   3100,
		EnvVars: prefixEnvVars("PORT"),
	}
	FileStorePathFlag = &cli.StringFlag{
		Name:    "file.path",
		Usage:   "path to directory for file storage",
		EnvVars: prefixEnvVars("FILESTORE_PATH"),
	}
)

// Flags contains the list of configuration options available to the binary.
var Flags = append([]cli.Flag{ListenAddrFlag, PortFlag, FileStorePathFlag}, logcli.CLIFlags(EnvVarPrefix)...)

func checkFlags(ctx *cli.Context) error {
	if ctx.String(FileStorePathFlag.Name) == "" {
		return errors.New("flag file.path is required")
	}
	return nil
}
