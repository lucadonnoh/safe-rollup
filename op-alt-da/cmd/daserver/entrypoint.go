package main

import (
	"fmt"

	"github.com/urfave/cli/v2"

	altda "github.com/ethereum-optimism/optimism/op-alt-da"
	"github.com/ethereum-optimism/optimism/op-service/ctxinterrupt"
	"github.com/ethereum-optimism/optimism/op-service/log/logcli"
)

func StartDAServer(cliCtx *cli.Context) error {
	if err := checkFlags(cliCtx); err != nil {
		return err
	}
	l := logcli.NewLogger(logcli.AppOut(cliCtx), logcli.ReadCLIConfig(cliCtx))
	logcli.SetGlobalLogHandler(l.Handler())

	dir := cliCtx.String(FileStorePathFlag.Name)
	l.Info("Using file storage", "path", dir)
	server := altda.NewDAServer(cliCtx.String(ListenAddrFlag.Name), cliCtx.Int(PortFlag.Name), NewFileStore(dir), l)
	if err := server.Start(); err != nil {
		return fmt.Errorf("failed to start the DA server: %w", err)
	}
	l.Info("Started DA Server")
	defer func() {
		if err := server.Stop(); err != nil {
			l.Error("failed to stop DA server", "err", err)
		}
	}()
	return ctxinterrupt.Wait(cliCtx.Context)
}
