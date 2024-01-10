package main

import (
	"context"
	"sort"

	"github.com/canonical/lxd/shared"
	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/microcluster/client"
	"github.com/canonical/microcluster/internal/rest/types"
	"github.com/canonical/microcluster/microcluster"
	"github.com/spf13/cobra"
)

type cmdClusterMembers struct {
	common *CmdControl
}

func (c *cmdClusterMembers) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cluster",
		Short: "manage cluster members.",
		RunE:  c.Run,
	}

	var cmdRemove = cmdClusterMemberRemove{common: c.common}
	cmd.AddCommand(cmdRemove.Command())

	var cmdList = cmdClusterMembersList{common: c.common}
	cmd.AddCommand(cmdList.Command())

	var cmdUpgrade = cmdClusterMemberUpgrade{common: c.common}

	cmd.AddCommand(cmdUpgrade.Command())
	return cmd
}

func (c *cmdClusterMembers) Run(cmd *cobra.Command, args []string) error {
	return cmd.Help()
}

type cmdClusterMembersList struct {
	common *CmdControl

	flagNonCluster bool
}

func (c *cmdClusterMembersList) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list <address>",
		Short: "List cluster members locally, or remotely if an address is specified.",
		RunE:  c.Run,
	}

	cmd.Flags().BoolVar(&c.flagNonCluster, "non-cluster", false, "List non-cluster members")

	return cmd
}

func (c *cmdClusterMembersList) Run(cmd *cobra.Command, args []string) error {
	if len(args) > 1 {
		return cmd.Help()
	}

	// Get all state information for MicroCluster.
	m, err := microcluster.App(context.Background(), microcluster.Args{StateDir: c.common.FlagStateDir, Verbose: c.common.FlagLogVerbose, Debug: c.common.FlagLogDebug})
	if err != nil {
		return err
	}

	var client *client.Client

	// Get a local client connected to the unix socket if no address is specified.
	if len(args) == 1 {
		client, err = m.RemoteClient(args[0])
		if err != nil {
			return err
		}
	} else {
		client, err = m.LocalClient()
		if err != nil {
			return err
		}
	}

	if c.flagNonCluster {
		clusterMembers, err := client.GetNonClusterMembers(context.Background())
		if err != nil {
			return err
		}

		data := make([][]string, len(clusterMembers))
		for i, clusterMember := range clusterMembers {
			data[i] = []string{clusterMember.Name, clusterMember.Address.String(), shared.CertFingerprint(clusterMember.Certificate.Certificate)}
		}

		header := []string{"NAME", "ADDRESS", "FINGERPRINT"}
		sort.Sort(cli.SortColumnsNaturally(data))

		return cli.RenderTable(cli.TableFormatTable, header, data, clusterMembers)
	} else {
		clusterMembers, err := client.GetClusterMembers(context.Background())
		if err != nil {
			return err
		}

		data := make([][]string, len(clusterMembers))
		for i, clusterMember := range clusterMembers {
			data[i] = []string{clusterMember.Name, clusterMember.Address.String(), clusterMember.Role, clusterMember.Certificate.String(), string(clusterMember.Status)}
		}

		header := []string{"NAME", "ADDRESS", "ROLE", "CERTIFICATE", "STATUS"}
		sort.Sort(cli.SortColumnsNaturally(data))

		return cli.RenderTable(cli.TableFormatTable, header, data, clusterMembers)
	}
}

type cmdClusterMemberRemove struct {
	common *CmdControl

	flagForce bool
}

func (c *cmdClusterMemberRemove) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove the cluster member with the given name.",
		RunE:  c.Run,
	}

	cmd.Flags().BoolVarP(&c.flagForce, "force", "f", false, "Forcibly remove the cluster member")

	return cmd
}

func (c *cmdClusterMemberRemove) Run(cmd *cobra.Command, args []string) error {
	if len(args) != 1 {
		return cmd.Help()
	}

	m, err := microcluster.App(context.Background(), microcluster.Args{StateDir: c.common.FlagStateDir, Verbose: c.common.FlagLogVerbose, Debug: c.common.FlagLogDebug})
	if err != nil {
		return err
	}

	client, err := m.LocalClient()
	if err != nil {
		return err
	}

	err = client.DeleteClusterMember(context.Background(), args[0], c.flagForce)
	if err != nil {
		return err
	}

	return nil
}

type cmdClusterMemberUpgrade struct {
	common *CmdControl

	flagForce bool
}

func (c *cmdClusterMemberUpgrade) Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "upgrade <name>",
		Short: "Upgrade the cluster member with the given name.",
		RunE:  c.Run,
	}

	return cmd
}

func (c *cmdClusterMemberUpgrade) Run(cmd *cobra.Command, args []string) error {
	if len(args) != 1 {
		return cmd.Help()
	}

	m, err := microcluster.App(context.Background(), microcluster.Args{StateDir: c.common.FlagStateDir, Verbose: c.common.FlagLogVerbose, Debug: c.common.FlagLogDebug})
	if err != nil {
		return err
	}

	client, err := m.LocalClient()
	if err != nil {
		return err
	}

	req := types.ClusterMemberUpgrade{
		Name:       args[0],
		InitConfig: map[string]string{},
	}

	return client.UpgradeClusterMember(context.Background(), req)
}
