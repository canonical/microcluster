package client

import (
	"context"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/canonical/microcluster/internal/rest/types"
	"github.com/lxc/lxd/shared/api"
)

// Cluster is a list of clients belonging to a cluster.
type Cluster []Client

// SelectRandom returns a randomly selected client.
func (c Cluster) SelectRandom() Client {
	return c[rand.Intn(len(c))]
}

// Query executes the given hook across all members of the cluster.
func (c Cluster) Query(ctx context.Context, concurrent bool, query func(context.Context, *Client) error) error {
	if !concurrent {
		for _, client := range c {
			err := query(ctx, &client)
			if err != nil {
				return err
			}
		}

		return nil
	}

	errors := make([]error, 0, len(c))
	mut := sync.Mutex{}
	wg := sync.WaitGroup{}
	for _, client := range c {
		wg.Add(1)
		go func(client Client) {
			defer wg.Done()
			err := query(ctx, &client)
			if err != nil {
				mut.Lock()
				errors = append(errors, err)
				mut.Unlock()
				return
			}
		}(client)
	}

	// Wait for all queries to complete and check for any errors.
	wg.Wait()
	for _, err := range errors {
		if err != nil {
			return err
		}
	}

	return nil
}

// AddClusterMember records a new cluster member in the trust store of each current cluster member.
func (c *Client) AddClusterMember(ctx context.Context, args types.ClusterMemberPost) error {
	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	return c.QueryStruct(queryCtx, "POST", PublicEndpoint, api.NewURL().Path("cluster"), args, nil)
}

// GetClusterMembers returns the local record of cluster members.
func (c *Client) GetClusterMembers(ctx context.Context) ([]types.ClusterMember, error) {
	endpoint := PublicEndpoint
	if strings.HasSuffix(c.url.String(), "control.socket") {
		endpoint = ControlEndpoint
	}

	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	clusterMembers := []types.ClusterMember{}
	err := c.QueryStruct(queryCtx, "GET", endpoint, api.NewURL().Path("cluster"), nil, &clusterMembers)

	return clusterMembers, err
}
