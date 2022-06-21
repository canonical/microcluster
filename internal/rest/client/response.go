package client

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/lxc/lxd/shared/api"
)

func parseResponse(resp *http.Response) (*api.Response, error) {
	// Decode the response
	decoder := json.NewDecoder(resp.Body)
	response := api.Response{}

	err := decoder.Decode(&response)
	if err != nil {
		// Check the return value for a cleaner error
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("Failed to fetch %q: %q", resp.Request.URL.String(), resp.Status)
		}

		return nil, err
	}

	// Handle errors
	if response.Type == api.ErrorResponse {
		return nil, api.StatusErrorf(resp.StatusCode, response.Error)
	}

	return &response, nil
}
