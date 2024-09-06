package response

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
)

// ParseResponse takes a http response, parses it and returns the extracted result.
func ParseResponse(resp *http.Response) (*api.Response, error) {
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
		return nil, api.StatusErrorf(resp.StatusCode, "%s", response.Error)
	}

	defer resp.Body.Close()
	_, err = io.Copy(io.Discard, resp.Body)
	if err != nil {
		logger.Error("Failed to read response body", logger.Ctx{"error": err})
	}

	return &response, nil
}
