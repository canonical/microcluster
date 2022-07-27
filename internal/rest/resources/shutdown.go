package resources

import (
	"fmt"
	"net/http"

	"github.com/lxc/lxd/lxd/response"

	"github.com/canonical/microcluster/internal/rest/access"
	"github.com/canonical/microcluster/internal/state"
	"github.com/canonical/microcluster/rest"
)

var shutdownCmd = rest.Endpoint{
	Path: "shutdown",

	Post: rest.EndpointAction{Handler: shutdownPost, AccessHandler: access.AllowAuthenticated},
}

func shutdownPost(state *state.State, r *http.Request) response.Response {
	if state.Context.Err() != nil {
		return response.SmartError(fmt.Errorf("Shutdown already in progress"))
	}

	return response.ManualResponse(func(w http.ResponseWriter) error {
		<-state.ReadyCh // Wait for daemon to start.

		// Run shutdown sequence synchronously.
		stopErr := state.Stop()
		err := response.SmartError(stopErr).Render(w)
		if err != nil {
			return err
		}

		// Send the response before the daemon process ends.
		f, ok := w.(http.Flusher)
		if ok {
			f.Flush()
		} else {
			return fmt.Errorf("http.ResponseWriter is not type http.Flusher")
		}

		// Send result of d.Stop() to cmdDaemon so that process stops with correct exit code from Stop().
		go func() {
			<-r.Context().Done() // Wait until request is finished.
			state.ShutdownDoneCh <- stopErr
		}()

		return nil
	})
}
