package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/canonical/microcluster/microcluster"
	"github.com/spf13/cobra"
)

type cmdInit struct {
	common *CmdControl

	flagBootstrap bool
	flagToken     string
	flagConfig    []string
}

func (c *cmdInit) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init <name> <address>",
		Short: "Initialize the network endpoint and create or join a new cluster",
		RunE:  c.Run,
		Example: `  microctl init member1 127.0.0.1:8443 --bootstrap
    microctl init member1 127.0.0.1:8443 --token <token>`,
	}

	cmd.Flags().BoolVar(&c.flagBootstrap, "bootstrap", false, "Configure a new cluster with this daemon")
	cmd.Flags().StringVar(&c.flagToken, "token", "", "Join a cluster with a join token")
	cmd.Flags().StringSliceVar(&c.flagConfig, "config", nil, "Extra configuration to be applied during bootstrap")
	cmd.MarkFlagsMutuallyExclusive("bootstrap", "token")

	return cmd
}

func (c *cmdInit) Run(cmd *cobra.Command, args []string) error {
	if len(args) != 2 {
		return cmd.Help()
	}

	m, err := microcluster.App(microcluster.Args{StateDir: c.common.FlagStateDir, Verbose: c.common.FlagLogVerbose, Debug: c.common.FlagLogDebug})
	if err != nil {
		return fmt.Errorf("Unable to configure MicroCluster: %w", err)
	}

	conf := make(map[string]string, len(c.flagConfig))
	for _, setting := range c.flagConfig {
		key, value, ok := strings.Cut(setting, "=")
		if !ok {
			return fmt.Errorf("Malformed additional configuration value %s", setting)
		}

		conf[key] = value
	}

	if c.flagBootstrap {
		return m.NewCluster(cmd.Context(), args[0], args[1], conf, time.Second*30)
	}

	if c.flagToken != "" {
		return m.JoinCluster(cmd.Context(), args[0], args[1], c.flagToken, conf, time.Second*30)
	}

	return fmt.Errorf("Option must be one of bootstrap or token")
}
