package main

import (
	"context"
	"fmt"

	"github.com/canonical/microcluster/microcluster"
	"github.com/spf13/cobra"
)

type cmdSecrets struct {
	common *CmdControl
}

func (c *cmdSecrets) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "secrets <server.crt>",
		Short: "Manage join tokens for MicroCluster",
		RunE:  c.Run,
	}

	return cmd
}

func (c *cmdSecrets) Run(cmd *cobra.Command, args []string) error {
	if len(args) != 1 {
		return cmd.Help()
	}

	m, err := microcluster.App(context.Background(), c.common.FlagStateDir, c.common.FlagLogVerbose, c.common.FlagLogDebug)
	if err != nil {
		return err
	}

	token, err := m.NewJoinToken(args[0])
	if err != nil {
		return err
	}

	fmt.Println(token)

	return nil
}
