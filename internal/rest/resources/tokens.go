package resources

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/gorilla/mux"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/shared"

	"github.com/canonical/microcluster/cluster"
	"github.com/canonical/microcluster/internal/db"
	"github.com/canonical/microcluster/internal/rest/access"
	internalTypes "github.com/canonical/microcluster/internal/rest/types"
	"github.com/canonical/microcluster/internal/state"
	"github.com/canonical/microcluster/rest"
	"github.com/canonical/microcluster/rest/types"
)

var tokensCmd = rest.Endpoint{
	Path: "tokens",

	Post: rest.EndpointAction{Handler: tokensPost, AccessHandler: access.AllowAuthenticated},
	Get:  rest.EndpointAction{Handler: tokensGet, AccessHandler: access.AllowAuthenticated},
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

	err = state.Database.Transaction(state.Context, func(ctx context.Context, tx *db.Tx) error {
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
	err = state.Database.Transaction(state.Context, func(ctx context.Context, tx *db.Tx) error {
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

	err = state.Database.Transaction(state.Context, func(ctx context.Context, tx *db.Tx) error {
		return cluster.DeleteInternalTokenRecord(ctx, tx, name)
	})
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}
