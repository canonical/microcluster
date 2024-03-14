package resources

import (
	"net/http"

	"github.com/canonical/lxd/lxd/response"

	internalTypes "github.com/canonical/microcluster/internal/rest/types"
	"github.com/canonical/microcluster/internal/state"
	"github.com/canonical/microcluster/rest"
	"github.com/canonical/microcluster/rest/types"
)

var api10Cmd = rest.Endpoint{
	AllowedBeforeInit: true,

	// swagger:operation GET /cluster/1.0 server server_get
	//
	//	Get the server environment and configuration
	//
	//	Shows the full server environment and configuration.
	//
	//	---
	//	produces:
	//	  - application/json
	//	responses:
	//	  "200":
	//	    description: Server environment and configuration
	//	    schema:
	//	      type: object
	//	      description: Sync response
	//	      properties:
	//	        type:
	//	          type: string
	//	          description: Response type
	//	          example: sync
	//	        status:
	//	          type: string
	//	          description: Status description
	//	          example: Success
	//	        status_code:
	//	          type: integer
	//	          description: Status code
	//	          example: 200
	//	        metadata:
	//	          $ref: "#/definitions/Server"
	//	  "400":
	//	    $ref: "#/responses/BadRequest"
	//	  "500":
	//	    $ref: "#/responses/InternalServerError"
	Get: rest.EndpointAction{Handler: api10Get, AllowUntrusted: true},
}

func api10Get(s *state.State, r *http.Request) response.Response {
	addrPort, err := types.ParseAddrPort(s.Address().URL.Host)
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponse(true, internalTypes.Server{
		Name:    s.Name(),
		Address: addrPort,
		Ready:   s.Database.IsOpen(),
	})
}
