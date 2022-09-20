// Package microd provides the daemon.
package main

import (
	"context"
	"math/rand"
	"os"
	"time"

	"github.com/lxc/lxd/shared/logger"
	"github.com/spf13/cobra"

	"github.com/canonical/microcluster/example/api"
	"github.com/canonical/microcluster/example/database"
	"github.com/canonical/microcluster/example/version"
	"github.com/canonical/microcluster/microcluster"
	"github.com/canonical/microcluster/state"
)

// Debug indicates whether to log debug messages or not.
var Debug bool

// Verbose indicates verbosity.
var Verbose bool

type cmdGlobal struct {
	cmd *cobra.Command //nolint:structcheck,unused // FIXME: Remove the nolint flag when this is in use.

	flagHelp    bool
	flagVersion bool

	flagLogDebug   bool
	flagLogVerbose bool
}

func (c *cmdGlobal) Run(cmd *cobra.Command, args []string) error {
	Debug = c.flagLogDebug
	Verbose = c.flagLogVerbose

	return logger.InitLogger("", "", c.flagLogVerbose, c.flagLogDebug, nil)
}

type cmdDaemon struct {
	global *cmdGlobal

	flagStateDir string
}

func (c *cmdDaemon) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "microd",
		Short:   "Example daemon for MicroCluster - This will start a daemon with a running control socket and no database",
		Version: version.Version,
	}

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdDaemon) Run(cmd *cobra.Command, args []string) error {
	m, err := microcluster.App(context.Background(), c.flagStateDir, c.global.flagLogVerbose, c.global.flagLogDebug)
	if err != nil {
		return err
	}

	return m.Start(api.Endpoints, database.SchemaExtensions, func(state *state.State, bootstrap bool) error {
		if bootstrap {
			logger.Info("This is a hook run on bootstrap")
		} else {
			logger.Info("This is a hook run on join")
		}

		return nil
	})
}

func init() {
	rand.Seed(time.Now().UnixNano())
}

func main() {
	daemonCmd := cmdDaemon{global: &cmdGlobal{}}
	app := daemonCmd.Command()
	app.SilenceUsage = true
	app.CompletionOptions = cobra.CompletionOptions{DisableDefaultCmd: true}

	app.PersistentFlags().BoolVarP(&daemonCmd.global.flagHelp, "help", "h", false, "Print help")
	app.PersistentFlags().BoolVar(&daemonCmd.global.flagVersion, "version", false, "Print version number")
	app.PersistentFlags().BoolVarP(&daemonCmd.global.flagLogDebug, "debug", "d", false, "Show all debug messages")
	app.PersistentFlags().BoolVarP(&daemonCmd.global.flagLogVerbose, "verbose", "v", false, "Show all information messages")

	app.PersistentFlags().StringVar(&daemonCmd.flagStateDir, "state-dir", "", "Path to store state information"+"``")

	app.SetVersionTemplate("{{.Version}}\n")

	err := app.Execute()
	if err != nil {
		os.Exit(1)
	}
}
