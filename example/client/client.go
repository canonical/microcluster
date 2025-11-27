// Package client provides a full Go API client.
package client

import (
	"context"
	"fmt"
	"time"

	"github.com/canonical/lxd/shared/api"
	"github.com/gorilla/websocket"

	"github.com/canonical/microcluster/v2/client"
	"github.com/canonical/microcluster/v2/example/api/types"
)

// ExtendedSimpleCmd is a client function that sets a context timeout and sends a POST to /1.0/extended/simple using the given
// client. This function is expected to be called from an api endpoint handler, which gives us access to the
// daemon state, from which we can create a client.
func ExtendedSimpleCmd(ctx context.Context, c *client.Client, data *types.ExtendedType) (string, error) {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*30)
	defer cancel()

	var outStr string
	err := c.Query(queryCtx, "POST", types.ExtendedPathPrefix, api.NewURL().Path("extended", "simple"), data, &outStr)
	if err != nil {
		clientURL := c.URL()
		return "", fmt.Errorf("Failed performing action on %q: %w", clientURL.String(), err)
	}

	return outStr, nil
}

// ExtendedWebsocketCmd is a client function that sets a context timeout and sends a GET to /1.0/extended/websocket using the given
// client. This function is expected to be called from an api endpoint handler, which gives us access to the
// daemon state, from which we can create a client.
func ExtendedWebsocketCmd(ctx context.Context, c *client.Client) error {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second*30)
	defer cancel()

	conn, err := c.Websocket(queryCtx, types.ExtendedPathPrefix, api.NewURL().Path("extended", "websocket"))
	if err != nil {
		return fmt.Errorf("Failed to create websocket connection: %w", err)
	}

	defer conn.Close()

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			// If the server closes the connection, exit gracefully.
			if websocket.IsCloseError(err, websocket.CloseAbnormalClosure) {
				return nil
			}

			return fmt.Errorf("Failed to read message from websocket: %w", err)
		}

		fmt.Println(string(message))
	}
}
