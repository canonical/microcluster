package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/canonical/microcluster/v3/example/client"
	"github.com/canonical/microcluster/v3/microcluster"
)

type cmdExtended struct {
	common *CmdControl
}

type cmdExtendedSimple struct {
	common *CmdControl

	flagTarget string
}

type cmdExtendedWebsocket struct {
	common *CmdControl

	flagTarget string
}

func (c *cmdExtended) command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "extended",
		Short: "An extended command not part of the default MicroCluster API",
		RunE:  c.run,
	}

	var cmdSimple = cmdExtendedSimple{common: c.common}
	cmd.AddCommand(cmdSimple.command())

	var cmdWebsocket = cmdExtendedWebsocket{common: c.common}
	cmd.AddCommand(cmdWebsocket.command())

	return cmd
}

func (c *cmdExtended) run(cmd *cobra.Command, args []string) error {
	return cmd.Help()
}

func (c *cmdExtendedSimple) command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "simple",
		Short: "Send a simple request",
		RunE:  c.run,
	}

	cmd.Flags().StringVar(&c.flagTarget, "target", "", "target cluster member for sending the simple request")

	return cmd
}

func (c *cmdExtendedSimple) run(cmd *cobra.Command, args []string) error {
	if len(args) > 1 {
		return cmd.Help()
	}

	m, err := microcluster.App(microcluster.Args{
		LogHandler: logHandler,
		StateDir:   c.common.FlagStateDir,
	})
	if err != nil {
		return err
	}

	cli, err := m.LocalClient()
	if err != nil {
		return err
	}

	if c.flagTarget != "" {
		cli = cli.UseTarget(c.flagTarget)
	}

	outMsg, err := client.ExtendedSimpleCmd(cmd.Context(), cli, nil)
	if err != nil {
		return err
	}

	fmt.Print(outMsg)

	return nil
}

func (c *cmdExtendedWebsocket) command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "websocket",
		Short: "Send a websocket request",
		RunE:  c.run,
	}

	cmd.Flags().StringVar(&c.flagTarget, "target", "", "target cluster member for sending the websocket request")

	return cmd
}

func (c *cmdExtendedWebsocket) run(cmd *cobra.Command, args []string) error {
	if len(args) > 1 {
		return cmd.Help()
	}

	m, err := microcluster.App(microcluster.Args{
		LogHandler: logHandler,
		StateDir:   c.common.FlagStateDir,
	})
	if err != nil {
		return err
	}

	cli, err := m.LocalClient()
	if err != nil {
		return err
	}

	if c.flagTarget != "" {
		cli = cli.UseTarget(c.flagTarget)
	}

	err = client.ExtendedWebsocketCmd(cmd.Context(), cli)
	if err != nil {
		return err
	}

	return nil
}
