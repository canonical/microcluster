package resources

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/canonical/lxd/shared"
	"github.com/gorilla/mux"

	"github.com/canonical/microcluster/v4/internal/cluster"
	"github.com/canonical/microcluster/v4/internal/log"
	"github.com/canonical/microcluster/v4/internal/rest/access"
	internalState "github.com/canonical/microcluster/v4/internal/state"
	"github.com/canonical/microcluster/v4/internal/utils"
	"github.com/canonical/microcluster/v4/microcluster/types"
)

var tokensCmd = types.Endpoint{
	Path: "tokens",

	Post: types.EndpointAction{Handler: tokensPost, AccessHandler: access.AllowAuthenticated},
	Get:  types.EndpointAction{Handler: tokensGet, AccessHandler: access.AllowAuthenticated},
}

var tokenCmd = types.Endpoint{
	Path: "tokens/{name}",

	Delete: types.EndpointAction{Handler: tokenDelete, AccessHandler: access.AllowAuthenticated},
}

func tokensPost(state types.State, r *http.Request) types.Response {
	req := types.TokenRequest{}

	// Parse the request.
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return types.BadRequest(err)
	}

	err = utils.ValidateFQDN(req.Name)
	if err != nil {
		return types.SmartError(fmt.Errorf("Token name %q is not a valid FQDN: %w", req.Name, err))
	}

	// Check cluster membership consistency before allowing token creation
	// This ensures core_cluster_members, truststore, and dqlite are all in sync
	intState, err := internalState.ToInternal(state)
	if err != nil {
		return types.SmartError(err)
	}

	err = intState.CheckMembershipConsistency(r.Context())
	if err != nil {
		return types.SmartError(err)
	}

	// Generate join token for new member. This will be stored alongside the join
	// address and cluster certificate to simplify setup.
	tokenKey, err := shared.RandomCryptoString()
	if err != nil {
		return types.InternalError(err)
	}

	clusterCert, err := state.ClusterCert().PublicKeyX509()
	if err != nil {
		return types.InternalError(err)
	}

	joinAddresses := []types.AddrPort{}
	for _, addr := range state.Truststore().RemoteAddresses() {
		joinAddresses = append(joinAddresses, addr)
	}

	logger, err := log.LoggerFromContext(r.Context())
	if err != nil {
		return types.InternalError(err)
	}

	if len(joinAddresses) == 0 {
		logger.Warn(fmt.Sprintf("Failed to check trust store for eligible join addresses. Issuing token with join address %q", state.Address().Host))
		joinAddresses, err = types.ParseAddrPorts([]string{state.Address().Host})
		if err != nil {
			return types.SmartError(err)
		}
	}

	expiryDate := sql.NullTime{
		Valid: req.ExpireAfter != 0,
	}

	if expiryDate.Valid {
		expiryDate.Time = time.Now().Add(req.ExpireAfter)
	}

	token := types.Token{
		Secret:        tokenKey,
		Fingerprint:   shared.CertFingerprint(clusterCert),
		JoinAddresses: joinAddresses,
	}

	tokenString, err := token.String()
	if err != nil {
		return types.InternalError(err)
	}

	err = state.Database().Transaction(r.Context(), func(ctx context.Context, tx *sql.Tx) error {
		err = cluster.DeleteExpiredCoreTokenRecords(ctx, tx)
		if err != nil {
			return err
		}

		_, err = cluster.CreateCoreTokenRecord(ctx, tx, cluster.CoreTokenRecord{
			Name:       req.Name,
			Secret:     tokenKey,
			ExpiryDate: expiryDate,
		})
		return err
	})
	if err != nil {
		return types.SmartError(err)
	}

	return types.SyncResponse(true, tokenString)
}

func tokensGet(state types.State, r *http.Request) types.Response {
	clusterCert, err := state.ClusterCert().PublicKeyX509()
	if err != nil {
		return types.InternalError(err)
	}

	joinAddresses := []types.AddrPort{}
	for _, addr := range state.Truststore().RemoteAddresses() {
		joinAddresses = append(joinAddresses, addr)
	}

	var records []types.TokenRecord
	err = state.Database().Transaction(r.Context(), func(ctx context.Context, tx *sql.Tx) error {
		var err error
		tokens, err := cluster.GetCoreTokenRecords(ctx, tx)
		if err != nil {
			return err
		}

		records = make([]types.TokenRecord, 0, len(tokens))
		for _, token := range tokens {
			if token.Expired() {
				continue
			}

			apiToken, err := token.ToAPI(clusterCert, joinAddresses)
			if err != nil {
				return err
			}

			records = append(records, *apiToken)
		}

		return nil
	})
	if err != nil {
		return types.SmartError(err)
	}

	return types.SyncResponse(true, records)
}

func tokenDelete(state types.State, r *http.Request) types.Response {
	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return types.SmartError(err)
	}

	err = state.Database().Transaction(r.Context(), func(ctx context.Context, tx *sql.Tx) error {
		return cluster.DeleteCoreTokenRecord(ctx, tx, name)
	})
	if err != nil {
		return types.SmartError(err)
	}

	return types.EmptySyncResponse
}
