package main

import (
	"context"
	"sort"

	"github.com/canonical/microcluster/client"
	"github.com/canonical/microcluster/microcluster"
	"github.com/lxc/lxd/lxc/utils"
	"github.com/spf13/cobra"
)

type cmdClusterMembers struct {
	common *CmdControl
}

func (c *cmdClusterMembers) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cluster <address>",
		Short: "List all cluster members locally, or remotely if an address is specified.",
		RunE:  c.Run,
	}

	return cmd
}

func (c *cmdClusterMembers) Run(cmd *cobra.Command, args []string) error {
	if len(args) > 1 {
		return cmd.Help()
	}

	// Get all state information for MicroCluster.
	m, err := microcluster.App(context.Background(), c.common.FlagStateDir, c.common.FlagLogVerbose, c.common.FlagLogDebug)
	if err != nil {
		return err
	}

	var client *client.Client

	// Get a local client connected to the unix socket if no address is specified.
	if len(args) == 1 {
		client, err = m.Client(args[0])
		if err != nil {
			return err
		}
	} else {

		client, err = m.LocalClient()
		if err != nil {
			return err
		}
	}

	clusterMembers, err := client.GetClusterMembers(context.Background())
	if err != nil {
		return err
	}

	data := make([][]string, len(clusterMembers))
	for i, clusterMember := range clusterMembers {
		data[i] = []string{clusterMember.Name, clusterMember.Address.String(), clusterMember.Role, clusterMember.Certificate.String(), string(clusterMember.Status)}
	}

	header := []string{"NAME", "ADDRESS", "ROLE", "CERTIFICATE", "STATUS"}
	sort.Sort(utils.ByName(data))

	return utils.RenderTable(utils.TableFormatTable, header, data, clusterMembers)
}
