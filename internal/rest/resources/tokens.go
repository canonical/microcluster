package resources

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/logger"
	"github.com/gorilla/mux"

	"github.com/canonical/microcluster/cluster"
	internalTypes "github.com/canonical/microcluster/internal/rest/types"
	"github.com/canonical/microcluster/internal/state"
	"github.com/canonical/microcluster/rest"
	"github.com/canonical/microcluster/rest/access"
	"github.com/canonical/microcluster/rest/types"
)

var tokensCmd = rest.Endpoint{
	Path: "tokens",

	// swagger:operation POST /cluster/1.0/tokens tokens tokens_post
	//
	//  Issue a join token
	//
	//	Generate a join token to be used by another system to join the cluster.
	//
	//	---
	//	consumes:
	//	  - application/json
	//	produces:
	//	  - application/json
	//	parameters:
	//	  - in: body
	//	    name: cluster
	//	    description: Joining system name
	//	    required: true
	//	    schema:
	//	      $ref: "#/definitions/TokenRecord"
	//	responses:
	//	  "200":
	//	    description: Cluster token response
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
	// 						type: string
	//	  "400":
	//	    $ref: "#/responses/BadRequest"
	//	  "403":
	//	    $ref: "#/responses/Forbidden"
	//	  "500":
	//	    $ref: "#/responses/InternalServerError"
	Post: rest.EndpointAction{Handler: tokensPost, AccessHandler: access.AllowAuthenticated},

	// swagger:operation GET /cluster/1.0/tokens tokens tokens_get
	//
	//  List join tokens
	//
	//	List all existing join tokens and the system they correspond to.
	//
	//	---
	//	produces:
	//	  - application/json
	//	responses:
	//	  "200":
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
	//	          type: array
	//	          description: List of tokens
	//	          items:
	//  	          $ref: "#/definitions/TokenRecord"
	//	  "400":
	//	    $ref: "#/responses/BadRequest"
	//	  "403":
	//	    $ref: "#/responses/Forbidden"
	//	  "500":
	//	    $ref: "#/responses/InternalServerError"
	Get: rest.EndpointAction{Handler: tokensGet, AccessHandler: access.AllowAuthenticated},
}

var tokenCmd = rest.Endpoint{
	Path: "tokens/{name}",

	Delete: rest.EndpointAction{Handler: tokenDelete, AccessHandler: access.AllowAuthenticated},
}

func tokensPost(state *state.State, r *http.Request) response.Response {
	req := internalTypes.TokenRecord{}

	// Parse the request.
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Generate join token for new member. This will be stored alongside the join
	// address and cluster certificate to simplify setup.
	tokenKey, err := shared.RandomCryptoString()
	if err != nil {
		return response.InternalError(err)
	}

	clusterCert, err := state.ClusterCert().PublicKeyX509()
	if err != nil {
		return response.InternalError(err)
	}

	joinAddresses := []types.AddrPort{}
	for _, addr := range state.Remotes().Addresses() {
		joinAddresses = append(joinAddresses, addr)
	}

	if len(joinAddresses) == 0 {
		logger.Warnf("Failed to check trust store for eligible join addresses. Issuing token with join address %q", state.Address().URL.Host)
		joinAddresses, err = types.ParseAddrPorts([]string{state.Address().URL.Host})
		if err != nil {
			return response.SmartError(err)
		}
	}

	token := internalTypes.Token{
		Name:          req.Name,
		Secret:        tokenKey,
		Fingerprint:   shared.CertFingerprint(clusterCert),
		JoinAddresses: joinAddresses,
	}

	tokenString, err := token.String()
	if err != nil {
		return response.InternalError(err)
	}

	err = state.Database.Transaction(state.Context, func(ctx context.Context, tx *sql.Tx) error {
		_, err = cluster.CreateInternalTokenRecord(ctx, tx, cluster.InternalTokenRecord{Name: req.Name, Secret: tokenKey})
		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponse(true, tokenString)
}

func tokensGet(state *state.State, r *http.Request) response.Response {
	clusterCert, err := state.ClusterCert().PublicKeyX509()
	if err != nil {
		return response.InternalError(err)
	}

	joinAddresses := []types.AddrPort{}
	for _, addr := range state.Remotes().Addresses() {
		joinAddresses = append(joinAddresses, addr)
	}

	var records []internalTypes.TokenRecord
	err = state.Database.Transaction(state.Context, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		tokens, err := cluster.GetInternalTokenRecords(ctx, tx)
		if err != nil {
			return err
		}

		records = make([]internalTypes.TokenRecord, 0, len(tokens))
		for _, token := range tokens {
			apiToken, err := token.ToAPI(clusterCert, joinAddresses)
			if err != nil {
				return err
			}

			records = append(records, *apiToken)
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponse(true, records)
}

func tokenDelete(state *state.State, r *http.Request) response.Response {
	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	err = state.Database.Transaction(state.Context, func(ctx context.Context, tx *sql.Tx) error {
		return cluster.DeleteInternalTokenRecord(ctx, tx, name)
	})
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}
