package client

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
)

// Cluster is a list of clients belonging to a cluster.
type Cluster []Client

// SelectRandom returns a randomly selected client.
func (c Cluster) SelectRandom() (*Client, error) {
	switch len(c) {
	case 0:
		// Returns an error if the cluster is uninitialized (not bootstrapped, not joined).
		return nil, fmt.Errorf("Cluster is uninitialized or has no members")
	case 1:
		// Returns the only available client if cluster size is 1.
		return &c[0], nil
	default:
		// Returns a randomly selected client for clusters with multiple members.
		return &c[rand.Intn(len(c))], nil
	}
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
