package main

import (
	"context"
	"fmt"

	microClient "github.com/canonical/microcluster/client"
	"github.com/canonical/microcluster/example/client"
	"github.com/canonical/microcluster/microcluster"
	"github.com/spf13/cobra"
)

type cmdExtended struct {
	common *CmdControl
}

func (c *cmdExtended) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "extended <address>",
		Short: "An extended command not part of the default MicroCluster API",
		RunE:  c.Run,
	}

	return cmd
}

func (c *cmdExtended) Run(cmd *cobra.Command, args []string) error {
	if len(args) > 1 {
		return cmd.Help()
	}

	m, err := microcluster.App(context.Background(), microcluster.Args{StateDir: c.common.FlagStateDir, Verbose: c.common.FlagLogVerbose, Debug: c.common.FlagLogDebug})
	if err != nil {
		return err
	}

	var cli *microClient.Client
	if len(args) == 1 {
		cli, err = m.RemoteClient(args[0])
	} else {
		cli, err = m.LocalClient()
	}

	if err != nil {
		return err
	}

	outMsg, err := client.ExtendedPostCmd(context.Background(), cli, nil)
	if err != nil {
		return err
	}

	fmt.Printf(outMsg)

	return nil
}
