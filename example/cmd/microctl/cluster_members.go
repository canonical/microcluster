package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/canonical/lxd/shared"
	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/termios"
	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"
	"gopkg.in/yaml.v3"

	"github.com/canonical/microcluster/client"
	"github.com/canonical/microcluster/cluster"
	"github.com/canonical/microcluster/microcluster"
)

const recoveryConfirmation = `You should only run this command if:
 - A quorum of cluster members is permanently lost
 - You are *absolutely* sure all microd instances are stopped
 - This instance has the most up to date database

Do you want to proceed? (yes/no): `

const recoveryYamlComment = `# Member roles can be modified. Unrecoverable nodes should be given the role "spare".
#
# "voter" - Voting member of the database. A majority of voters is a quorum.
# "stand-by" - Non-voting member of the database; can be promoted to voter.
# "spare" - Not a member of the database.
#
# The edit is aborted if:
# - the number of members changes
# - the name of any member changes
# - the ID of any member changes
# - the address of any member changes
# - no changes are made
`

type cmdClusterMembers struct {
	common *CmdControl
}

func (c *cmdClusterMembers) command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cluster",
		Short: "manage cluster members.",
		RunE:  c.run,
	}

	var cmdRemove = cmdClusterMemberRemove{common: c.common}
	cmd.AddCommand(cmdRemove.command())

	var cmdList = cmdClusterMembersList{common: c.common}
	cmd.AddCommand(cmdList.command())

	var cmdRestore = cmdClusterEdit{common: c.common}
	cmd.AddCommand(cmdRestore.command())

	return cmd
}

func (c *cmdClusterMembers) run(cmd *cobra.Command, args []string) error {
	return cmd.Help()
}

type cmdClusterMembersList struct {
	common *CmdControl

	flagLocal  bool
	flagFormat string
}

func (c *cmdClusterMembersList) command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list <address>",
		Short: "List cluster members locally, or remotely if an address is specified.",
		RunE:  c.run,
	}

	cmd.Flags().BoolVarP(&c.flagLocal, "local", "l", false, "display the locally available cluster info (no database query)")
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", cli.TableFormatTable, "Format (csv|json|table|yaml|compact)")

	return cmd
}

func (c *cmdClusterMembersList) run(cmd *cobra.Command, args []string) error {
	if len(args) > 1 {
		return cmd.Help()
	}

	// Get all state information for MicroCluster.
	m, err := microcluster.App(microcluster.Args{StateDir: c.common.FlagStateDir, Verbose: c.common.FlagLogVerbose, Debug: c.common.FlagLogDebug})
	if err != nil {
		return err
	}

	if c.flagLocal {
		return c.listLocalClusterMembers(m)
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

	return c.listClusterMembers(cmd.Context(), client)
}

func (c *cmdClusterMembersList) listClusterMembers(ctx context.Context, client *client.Client) error {
	clusterMembers, err := client.GetClusterMembers(ctx)
	if err != nil {
		return err
	}

	data := make([][]string, len(clusterMembers))
	for i, clusterMember := range clusterMembers {
		data[i] = []string{clusterMember.Name, clusterMember.Address.String(), clusterMember.Role, shared.CertFingerprint(clusterMember.Certificate.Certificate), string(clusterMember.Status)}
	}

	header := []string{"NAME", "ADDRESS", "ROLE", "FINGERPRINT", "STATUS"}
	sort.Sort(cli.SortColumnsNaturally(data))

	return cli.RenderTable(c.flagFormat, header, data, clusterMembers)
}

func (c *cmdClusterMembersList) listLocalClusterMembers(m *microcluster.MicroCluster) error {
	members, err := m.GetDqliteClusterMembers()
	if err != nil {
		return err
	}

	data := make([][]string, len(members))
	for i, member := range members {
		data[i] = []string{strconv.FormatUint(member.DqliteID, 10), member.Name, member.Address, member.Role}
	}

	header := []string{"ID", "NAME", "ADDRESS", "ROLE"}
	sort.Sort(cli.SortColumnsNaturally(data))

	return cli.RenderTable(c.flagFormat, header, data, members)
}

type cmdClusterMemberRemove struct {
	common *CmdControl

	flagForce bool
}

func (c *cmdClusterMemberRemove) command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove the cluster member with the given name.",
		RunE:  c.run,
	}

	cmd.Flags().BoolVarP(&c.flagForce, "force", "f", false, "Forcibly remove the cluster member")

	return cmd
}

func (c *cmdClusterMemberRemove) run(cmd *cobra.Command, args []string) error {
	if len(args) != 1 {
		return cmd.Help()
	}

	m, err := microcluster.App(microcluster.Args{StateDir: c.common.FlagStateDir, Verbose: c.common.FlagLogVerbose, Debug: c.common.FlagLogDebug})
	if err != nil {
		return err
	}

	client, err := m.LocalClient()
	if err != nil {
		return err
	}

	err = client.DeleteClusterMember(cmd.Context(), args[0], c.flagForce)
	if err != nil {
		return err
	}

	return nil
}

type cmdClusterEdit struct {
	common *CmdControl
}

func (c *cmdClusterEdit) command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "edit",
		Short: "Recover the cluster from this node if quorum is lost",
		RunE:  c.run,
	}

	return cmd
}

func (c *cmdClusterEdit) run(cmd *cobra.Command, args []string) error {
	m, err := microcluster.App(microcluster.Args{StateDir: c.common.FlagStateDir, Verbose: c.common.FlagLogVerbose, Debug: c.common.FlagLogDebug})
	if err != nil {
		return err
	}

	members, err := m.GetDqliteClusterMembers()
	if err != nil {
		return err
	}

	membersYaml, err := yaml.Marshal(members)
	if err != nil {
		return err
	}

	var content []byte
	if !termios.IsTerminal(unix.Stdin) {
		content, err = io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
	} else {
		reader := bufio.NewReader(os.Stdin)
		fmt.Print(recoveryConfirmation)

		input, _ := reader.ReadString('\n')
		input = strings.TrimSuffix(input, "\n")

		if strings.ToLower(input) != "yes" {
			fmt.Println("Cluster edit aborted; no changes made")
			return nil
		}

		content, err = shared.TextEditor("", append([]byte(recoveryYamlComment), membersYaml...))
		if err != nil {
			return err
		}
	}

	newMembers := []cluster.DqliteMember{}
	err = yaml.Unmarshal(content, &newMembers)
	if err != nil {
		return err
	}

	tarballPath, err := m.RecoverFromQuorumLoss(newMembers)
	if err != nil {
		return fmt.Errorf("cluster edit: %w", err)
	}

	fmt.Printf("Cluster changes applied; new database state saved to %s\n\n", tarballPath)
	fmt.Printf("*Before* starting any cluster member, copy %s to %s on all remaining cluster members.\n\n", tarballPath, tarballPath)
	fmt.Printf("microd will load this file during startup.\n")

	return nil
}
