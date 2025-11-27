// Package client provides a full Go API client.
package client

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/canonical/microcluster/v3/client"
	"github.com/canonical/microcluster/v3/example/api/types"
)

// ExtendedSimpleCmd is a client function that sets a context timeout and sends a POST to /1.0/extended/simple using the given
// client. This function is expected to be called from an api endpoint handler, which gives us access to the
// daemon state, from which we can create a client.
func ExtendedSimpleCmd(ctx context.Context, c *client.Client, data *types.ExtendedType) (string, error) {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*30)
	defer cancel()

	path := url.URL{
		Path: "extended/simple",
	}

	var outStr string
	err := c.Query(queryCtx, "POST", types.ExtendedPathPrefix, &path, data, &outStr)
	if err != nil {
		clientURL := c.URL()
		return "", fmt.Errorf("Failed performing action on %q: %w", clientURL.String(), err)
	}

	return outStr, nil
}
