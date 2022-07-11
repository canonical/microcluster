package main

import (
	"context"
	"fmt"

	"github.com/canonical/microcluster/microcluster"
	"github.com/spf13/cobra"
)

type cmdInit struct {
	common *CmdControl

	flagBootstrap bool
	flagToken     string
}

func (c *cmdInit) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize the network endpoint and create or join a new cluster",
		RunE:  c.Run,
		Example: `  microctl init --bootstrap
  microctl init --token <token>`,
	}

	cmd.Flags().BoolVar(&c.flagBootstrap, "bootstrap", false, "Configure a new cluster with this daemon")
	cmd.Flags().StringVar(&c.flagToken, "token", "", "Join a cluster with a join token")
	return cmd
}

func (c *cmdInit) Run(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		return cmd.Help()
	}

	m, err := microcluster.App(context.Background(), c.common.FlagStateDir, c.common.FlagLogVerbose, c.common.FlagLogDebug)
	if err != nil {
		return fmt.Errorf("Unable to configure MicroCluster: %w", err)
	}

	if c.flagBootstrap && c.flagToken != "" {
		return fmt.Errorf("Option must be one of bootstrap or token")
	}

	if c.flagBootstrap {
		return m.NewCluster()
	}

	if c.flagToken != "" {
		return m.JoinCluster(c.flagToken)
	}

	return fmt.Errorf("Option must be one of bootstrap or token")
}
