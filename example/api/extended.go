package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	extendedTypes "github.com/canonical/microcluster/v3/example/api/types"
	extendedClient "github.com/canonical/microcluster/v3/example/client"
	"github.com/canonical/microcluster/v3/microcluster/rest/response"
	"github.com/canonical/microcluster/v3/microcluster/types"
)

// This is an example extended endpoint reachable at /1.0/extended.
var extendedCmd = types.Endpoint{
	Path: "extended",
	Post: types.EndpointAction{Handler: cmdPost, AllowUntrusted: true},
}

// This is the POST handler for the /1.0/extended endpoint.
// This example shows how to forward a request to other cluster members.
func cmdPost(state types.State, r *http.Request) response.Response {
	// Check the user agent header to check if we are the notifying cluster member.
	if !types.IsNotification(r) {
		// Get a collection of clients every other cluster member, with the notification user-agent set.
		clients, err := state.Connect().Cluster(true)
		if err != nil {
			return response.SmartError(fmt.Errorf("Failed to get a client for every cluster member: %w", err))
		}

		messages := make([]string, 0, len(clients))
		err = clients.Query(r.Context(), true, func(ctx context.Context, c types.Client) error {
			addrPort, err := types.ParseAddrPort(state.Address().Host)
			if err != nil {
				return fmt.Errorf("Failed to parse addr:port of listen address %q: %w", state.Address().Host, err)
			}

			// Our payload in this case is defined by us as ExtendedType.
			data := &extendedTypes.ExtendedType{
				Sender:  addrPort,
				Message: "Testing 1 2 3...",
			}

			// Asynchronously send a POST on /1.0/extended to each other cluster member.
			outMessage, err := extendedClient.ExtendedPostCmd(ctx, c, data)
			if err != nil {
				clientURL := c.URL()
				return fmt.Errorf("Failed to POST to cluster member with address %q: %w", clientURL.String(), err)
			}

			messages = append(messages, outMessage)

			return nil
		})
		if err != nil {
			return response.SmartError(err)
		}

		// Having received the result from all forwarded requests, compile them as a string and return.
		var outMsg string
		for _, message := range messages {
			outMsg = outMsg + message + "\n"
		}

		return response.SyncResponse(true, outMsg)
	}

	// Decode the POST body using our defined ExtendedType.
	var info extendedTypes.ExtendedType
	err := json.NewDecoder(r.Body).Decode(&info)
	if err != nil {
		return response.SmartError(err)
	}

	// Return some identifying information.
	message := fmt.Sprintf("cluster member at address %q received message %q from cluster member at address %q", state.Address().Host, info.Message, info.Sender.String())

	return response.SyncResponse(true, message)
}
