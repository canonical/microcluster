package main

import (
	"context"
	"fmt"

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
	if len(args) != 1 {
		return cmd.Help()
	}

	m, err := microcluster.App(context.Background(), c.common.FlagStateDir, c.common.FlagLogVerbose, c.common.FlagLogDebug)
	if err != nil {
		return err
	}

	cli, err := m.Client(args[0])
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
