package resources

import (
	"context"
	"crypto/x509"
	"database/sql"
	"net/http"
	"testing"
	"time"

	"github.com/canonical/lxd/shared/api"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/suite"

	"github.com/canonical/microcluster/v2/cluster"
	"github.com/canonical/microcluster/v2/rest/types"
)

type clusterSuite struct {
	suite.Suite
}

func TestClusterSuite(t *testing.T) {
	suite.Run(t, new(clusterSuite))
}

// newTestDB creates an in-memory sqlite database with the post-migration core
// table schemas and prepares the cluster statements against it.
func (s *clusterSuite) newTestDB() *sql.DB {
	db, err := sql.Open("sqlite3", ":memory:")
	s.Require().NoError(err)

	_, err = db.Exec(`
CREATE TABLE core_cluster_members (
    id              INTEGER   PRIMARY KEY AUTOINCREMENT NOT NULL,
    name            TEXT      NOT NULL,
    address         TEXT      NOT NULL,
    certificate     TEXT      NOT NULL,
    schema_internal INTEGER   NOT NULL,
    schema_external INTEGER   NOT NULL,
    heartbeat       DATETIME  NOT NULL,
    role            TEXT      NOT NULL,
    api_extensions  TEXT      NOT NULL DEFAULT '[]',
    UNIQUE(name),
    UNIQUE(certificate)
);

CREATE TABLE core_token_records (
    id          INTEGER   PRIMARY KEY AUTOINCREMENT NOT NULL,
    name        TEXT      NOT NULL,
    secret      TEXT      NOT NULL,
    expiry_date DATETIME,
    UNIQUE(name),
    UNIQUE(secret)
);
`)
	s.Require().NoError(err)

	err = cluster.PrepareStmts(db, "", false)
	s.Require().NoError(err)

	return db
}

// newRequest builds a ClusterMember join request with a certificate carrying
// the supplied DNS names and a fixed raw body (so that distinct raw bytes yield
// distinct certificate strings).
func (s *clusterSuite) newRequest(name, address, secret string, rawCert []byte, dnsNames ...string) types.ClusterMember {
	addrPort, err := types.ParseAddrPort(address)
	s.Require().NoError(err)

	return types.ClusterMember{
		ClusterMemberLocal: types.ClusterMemberLocal{
			Name:        name,
			Address:     addrPort,
			Certificate: types.X509Certificate{Certificate: &x509.Certificate{DNSNames: dnsNames, Raw: rawCert}},
		},
		Secret: secret,
	}
}

// memberFromRequest returns the pending CoreClusterMember that a successful
// admission of req would create.
func (s *clusterSuite) memberFromRequest(req types.ClusterMember) cluster.CoreClusterMember {
	return cluster.CoreClusterMember{
		Name:           req.Name,
		Address:        req.Address.String(),
		Certificate:    req.Certificate.String(),
		SchemaInternal: req.SchemaInternalVersion,
		SchemaExternal: req.SchemaExternalVersion,
		APIExtensions:  req.Extensions,
		Role:           cluster.Pending,
	}
}

// Test_admitClusterMember_createsMemberAndKeepsToken ensures that a valid first
// admission creates the pending member and, crucially, does not consume the
// join token.
func (s *clusterSuite) Test_admitClusterMember_createsMemberAndKeepsToken() {
	db := s.newTestDB()
	defer db.Close()

	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	s.Require().NoError(err)

	expiresAt := sql.NullTime{}
	_, err = cluster.CreateCoreTokenRecord(ctx, tx, cluster.CoreTokenRecord{Name: "node-1", Secret: "secret-1", ExpiryDate: expiresAt})
	s.Require().NoError(err)

	req := s.newRequest("node-1", "10.0.0.1:8443", "secret-1", []byte("cert-1"), "node-1")
	err = admitClusterMember(ctx, tx, req)
	s.Require().NoError(err)

	// The member must have been created and left pending.
	member, err := cluster.GetCoreClusterMember(ctx, tx, "node-1")
	s.Require().NoError(err)
	s.Equal(cluster.Pending, member.Role)
	s.Equal("10.0.0.1:8443", member.Address)
	s.Equal(req.Certificate.String(), member.Certificate)

	// The token must still be present for a later retry.
	record, err := cluster.GetCoreTokenRecord(ctx, tx, "secret-1")
	s.Require().NoError(err)
	s.Equal("node-1", record.Name)

	s.Require().NoError(tx.Commit())
}

// Test_admitClusterMember_retryAfterRollback ensures that a node which failed
// to join after admission (and was rolled back, removing its member record) can
// retry with the same token and be re-admitted.
func (s *clusterSuite) Test_admitClusterMember_retryAfterRollback() {
	db := s.newTestDB()
	defer db.Close()

	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	s.Require().NoError(err)

	_, err = cluster.CreateCoreTokenRecord(ctx, tx, cluster.CoreTokenRecord{Name: "node-1", Secret: "secret-1"})
	s.Require().NoError(err)

	req := s.newRequest("node-1", "10.0.0.1:8443", "secret-1", []byte("cert-1"), "node-1")

	// First admission creates the pending member and keeps the token.
	err = admitClusterMember(ctx, tx, req)
	s.Require().NoError(err)

	// The join then fails and the member is rolled back. Removal must not
	// consume the token, so that the member can retry.
	err = cluster.DeleteCoreClusterMember(ctx, tx, req.Address.String())
	s.Require().NoError(err)

	_, err = cluster.GetCoreTokenRecord(ctx, tx, "secret-1")
	s.Require().NoError(err)

	// The retry is admitted again.
	err = admitClusterMember(ctx, tx, req)
	s.Require().NoError(err)

	member, err := cluster.GetCoreClusterMember(ctx, tx, "node-1")
	s.Require().NoError(err)
	s.Equal(cluster.Pending, member.Role)

	s.Require().NoError(tx.Commit())
}

// Test_admitClusterMember_consumedTokenWithExistingMember ensures that a join
// carrying a token that has already been consumed (a completed join) is
// rejected, even when a member record with matching credentials still exists.
func (s *clusterSuite) Test_admitClusterMember_consumedTokenWithExistingMember() {
	db := s.newTestDB()
	defer db.Close()

	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	s.Require().NoError(err)

	req := s.newRequest("node-1", "10.0.0.1:8443", "secret-1", []byte("cert-1"), "node-1")

	// A member record exists (e.g. a previous join reached admission), but the
	// token has been consumed by confirmation.
	member := s.memberFromRequest(req)
	_, err = cluster.CreateCoreClusterMember(ctx, tx, member)
	s.Require().NoError(err)

	err = admitClusterMember(ctx, tx, req)
	s.Require().Error(err)
	s.True(api.StatusErrorCheck(err, http.StatusNotFound))

	// The existing member must be left untouched.
	members, err := cluster.GetCoreClusterMembers(ctx, tx)
	s.Require().NoError(err)
	s.Len(members, 1)

	s.Require().NoError(tx.Rollback())
}

// Test_admitClusterMember_consumedTokenWithoutMember ensures that a join
// carrying a token we do not have, and for which no member record exists, is
// rejected with a not-found error.
func (s *clusterSuite) Test_admitClusterMember_consumedTokenWithoutMember() {
	db := s.newTestDB()
	defer db.Close()

	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	s.Require().NoError(err)

	req := s.newRequest("node-1", "10.0.0.1:8443", "secret-1", []byte("cert-1"), "node-1")
	err = admitClusterMember(ctx, tx, req)
	s.Require().Error(err)
	s.True(api.StatusErrorCheck(err, http.StatusNotFound))

	s.Require().NoError(tx.Commit())
}

// Test_admitClusterMember_existingMemberMismatch ensures that a retry or
// concurrent admission is rejected when the existing member record does not
// describe the same node.
func (s *clusterSuite) Test_admitClusterMember_existingMemberMismatch() {
	db := s.newTestDB()
	defer db.Close()

	ctx := context.Background()
	req := s.newRequest("node-1", "10.0.0.1:8443", "secret-1", []byte("cert-1"), "node-1")

	mismatchedAddress := s.memberFromRequest(req)
	mismatchedAddress.Address = "10.0.0.99:8443"
	mismatchedCertificate := s.memberFromRequest(req)
	mismatchedCertificate.Certificate = "pre-existing-different-cert"

	tests := []struct {
		name        string
		existing    cluster.CoreClusterMember
		expectError string
	}{
		{
			name:        "token present, mismatched certificate",
			existing:    mismatchedCertificate,
			expectError: "mismatched credentials",
		},
		{
			name:        "token present, mismatched address",
			existing:    mismatchedAddress,
			expectError: "mismatched credentials",
		},
	}

	for i := range tests {
		tc := tests[i]
		s.T().Run(tc.name, func(t *testing.T) {
			tx, err := db.BeginTx(ctx, nil)
			s.Require().NoError(err)

			_, err = cluster.CreateCoreTokenRecord(ctx, tx, cluster.CoreTokenRecord{Name: "node-1", Secret: "secret-1"})
			s.Require().NoError(err)

			_, err = cluster.CreateCoreClusterMember(ctx, tx, tc.existing)
			s.Require().NoError(err)

			err = admitClusterMember(ctx, tx, req)
			s.Require().Error(err)
			s.Contains(err.Error(), tc.expectError)

			s.Require().NoError(tx.Rollback())
		})
	}
}

// Test_admitClusterMember_expiredToken ensures that an expired join token is
// rejected.
func (s *clusterSuite) Test_admitClusterMember_expiredToken() {
	db := s.newTestDB()
	defer db.Close()

	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	s.Require().NoError(err)

	_, err = cluster.CreateCoreTokenRecord(ctx, tx, cluster.CoreTokenRecord{
		Name:       "node-1",
		Secret:     "secret-1",
		ExpiryDate: sql.NullTime{Valid: true, Time: time.Now().Add(-time.Hour)},
	})
	s.Require().NoError(err)

	req := s.newRequest("node-1", "10.0.0.1:8443", "secret-1", []byte("cert-1"), "node-1")
	err = admitClusterMember(ctx, tx, req)
	s.Require().Error(err)
	s.Contains(err.Error(), "Token expired")

	s.Require().NoError(tx.Commit())
}

// Test_admitClusterMember_sanMismatch ensures that a join whose certificate SAN
// does not contain the token's member name is rejected.
func (s *clusterSuite) Test_admitClusterMember_sanMismatch() {
	db := s.newTestDB()
	defer db.Close()

	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	s.Require().NoError(err)

	_, err = cluster.CreateCoreTokenRecord(ctx, tx, cluster.CoreTokenRecord{Name: "node-1", Secret: "secret-1"})
	s.Require().NoError(err)

	req := s.newRequest("node-1", "10.0.0.1:8443", "secret-1", []byte("cert-1"), "some-other-name")
	err = admitClusterMember(ctx, tx, req)
	s.Require().Error(err)
	s.Contains(err.Error(), "SAN does not contain join token name")

	s.Require().NoError(tx.Commit())
}
