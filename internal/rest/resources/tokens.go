package resources

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/gorilla/mux"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/shared"

	"github.com/canonical/microcluster/internal/db"
	"github.com/canonical/microcluster/internal/db/cluster"
	"github.com/canonical/microcluster/internal/rest"
	"github.com/canonical/microcluster/internal/rest/access"
	"github.com/canonical/microcluster/internal/rest/types"
	"github.com/canonical/microcluster/internal/state"
)

var tokensCmd = rest.Endpoint{
	Path: "tokens",

	Post: rest.EndpointAction{Handler: tokensPost, AccessHandler: access.AllowAuthenticated},
	Get:  rest.EndpointAction{Handler: tokensGet, AccessHandler: access.AllowAuthenticated},
}

var tokenCmd = rest.Endpoint{
	Path: "tokens/{joinerCert}",

	Post:   rest.EndpointAction{Handler: tokenPost, AllowUntrusted: true},
	Delete: rest.EndpointAction{Handler: tokenDelete, AccessHandler: access.AllowAuthenticated},
}

func tokensPost(state *state.State, r *http.Request) response.Response {
	req := types.TokenRecord{}

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

	joinAddress, err := types.ParseAddrPort(state.Address.URL.Host)
	if err != nil {
		return response.InternalError(err)
	}

	token := types.Token{
		Token:       tokenKey,
		ClusterCert: types.X509Certificate{Certificate: clusterCert},
		JoinAddress: joinAddress,
	}

	tokenString, err := token.String()
	if err != nil {
		return response.InternalError(err)
	}

	err = state.Database.Transaction(state.Context, func(ctx context.Context, tx *db.Tx) error {
		exists, err := cluster.InternalTokenRecordExists(ctx, tx, req.JoinerCert)
		if err != nil {
			return err
		}

		if exists {
			return fmt.Errorf("A join token already exists for the name %q", req.JoinerCert)
		}

		_, err = cluster.CreateInternalTokenRecord(ctx, tx, cluster.InternalTokenRecord{JoinerCert: req.JoinerCert, Token: tokenKey})
		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponse(true, tokenString)
}

func tokensGet(state *state.State, r *http.Request) response.Response {
	var records []cluster.InternalTokenRecord
	err := state.Database.Transaction(state.Context, func(ctx context.Context, tx *db.Tx) error {
		var err error
		records, err = cluster.GetInternalTokenRecords(ctx, tx, cluster.InternalTokenRecordFilter{})

		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponse(true, records)
}

func tokenPost(state *state.State, r *http.Request) response.Response {
	joinerCert, err := url.PathUnescape(mux.Vars(r)["joinerCert"])
	if err != nil {
		return response.SmartError(err)
	}

	// Parse the request.
	req := types.TokenRecord{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	var record *cluster.InternalTokenRecord
	err = state.Database.Transaction(state.Context, func(ctx context.Context, tx *db.Tx) error {
		var err error
		record, err = cluster.GetInternalTokenRecord(ctx, tx, joinerCert)
		if err != nil {
			return err
		}

		if record.Token != req.Token {
			return fmt.Errorf("Received invalid token for the given joiner certificate")
		}

		return cluster.DeleteInternalTokenRecord(ctx, tx, joinerCert)
	})
	if err != nil {
		return response.SmartError(err)
	}

	clusterCert, err := state.ClusterCert().PublicKeyX509()
	if err != nil {
		return response.SmartError(err)
	}

	remotes := state.Remotes()
	clusterMembers := make([]types.ClusterMemberLocal, 0, len(remotes))
	for _, clusterMember := range remotes {
		clusterMember := types.ClusterMemberLocal{
			Name:        clusterMember.Name,
			Address:     clusterMember.Address,
			Certificate: clusterMember.Certificate,
		}

		clusterMembers = append(clusterMembers, clusterMember)
	}

	tokenResponse := types.TokenResponse{
		ClusterCert: types.X509Certificate{Certificate: clusterCert},
		ClusterKey:  string(state.ClusterCert().PrivateKey()),

		ClusterMembers: clusterMembers,
	}

	return response.SyncResponse(true, tokenResponse)
}

func tokenDelete(state *state.State, r *http.Request) response.Response {
	joinerCert, err := url.PathUnescape(mux.Vars(r)["joinerCert"])
	if err != nil {
		return response.SmartError(err)
	}

	err = state.Database.Transaction(state.Context, func(ctx context.Context, tx *db.Tx) error {
		return cluster.DeleteInternalTokenRecord(ctx, tx, joinerCert)
	})
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}
