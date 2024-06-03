// Package microctl provides the main client tool.
package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/canonical/microcluster/example/version"
)

// CmdControl has functions that are common to the microctl commands.
// command line tools.
type CmdControl struct {
	cmd *cobra.Command //nolint:structcheck,unused // FIXME: Remove the nolint flag when this is in use.

	FlagHelp       bool
	FlagVersion    bool
	FlagLogDebug   bool
	FlagLogVerbose bool
	FlagStateDir   string
}

func main() {
	// common flags.
	commonCmd := CmdControl{}

	app := &cobra.Command{
		Use:               "microctl",
		Short:             "Command for managing the MicroCluster daemon",
		Version:           version.Version,
		SilenceUsage:      true,
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
	}

	app.PersistentFlags().StringVar(&commonCmd.FlagStateDir, "state-dir", "", "Path to store state information"+"``")
	app.PersistentFlags().BoolVarP(&commonCmd.FlagHelp, "help", "h", false, "Print help")
	app.PersistentFlags().BoolVar(&commonCmd.FlagVersion, "version", false, "Print version number")
	app.PersistentFlags().BoolVarP(&commonCmd.FlagLogDebug, "debug", "d", false, "Show all debug messages")
	app.PersistentFlags().BoolVarP(&commonCmd.FlagLogVerbose, "verbose", "v", false, "Show all information messages")

	app.SetVersionTemplate("{{.Version}}\n")

	var cmdInit = cmdInit{common: &commonCmd}
	app.AddCommand(cmdInit.command())

	var cmdPeers = cmdClusterMembers{common: &commonCmd}
	app.AddCommand(cmdPeers.command())

	var cmdShutdown = cmdShutdown{common: &commonCmd}
	app.AddCommand(cmdShutdown.command())

	var cmdSQL = cmdSQL{common: &commonCmd}
	app.AddCommand(cmdSQL.command())

	var cmdSecrets = cmdSecrets{common: &commonCmd}
	app.AddCommand(cmdSecrets.command())

	var cmdWaitready = cmdWaitready{common: &commonCmd}
	app.AddCommand(cmdWaitready.command())

	var cmdExtended = cmdExtended{common: &commonCmd}
	app.AddCommand(cmdExtended.command())

	app.InitDefaultHelpCmd()

	err := app.Execute()
	if err != nil {
		os.Exit(1)
	}
}
