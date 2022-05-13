package cluster

import "time"

// CreateSchema is the default schema applied when bootstrapping the database.
const CreateSchema = `
CREATE TABLE internal_schemas (
  id          INTEGER    PRIMARY  KEY    AUTOINCREMENT  NOT  NULL,
  version     INTEGER    NOT      NULL,
  updated_at  DATETIME   NOT      NULL,
  UNIQUE      (version)
);

CREATE TABLE internal_certificates (
  id           INTEGER        PRIMARY  KEY    AUTOINCREMENT  NOT  NULL,
  fingerprint  TEXT           NOT      NULL,
  type         INTEGER        NOT      NULL,
  name         TEXT           NOT      NULL,
  certificate  text           NOT      NULL,
  UNIQUE       (fingerprint)
);

CREATE TABLE internal_token_records (
  id           INTEGER         PRIMARY  KEY    AUTOINCREMENT  NOT  NULL,
  joiner_cert  TEXT            NOT      NULL,
  token        TEXT            NOT      NULL,
  UNIQUE       (joiner_cert),
  UNIQUE       (token)
);

CREATE TABLE internal_cluster (
  id                   INTEGER  PRIMARY  KEY    AUTOINCREMENT  NOT  NULL,
  schema               INTEGER  NOT      NULL,
  state                INTEGER  NOT      NULL   DEFAULT        0,
  name                 TEXT     NOT      NULL,
  certificate          TEXT     NOT      NULL,
  UNIQUE(name),
  UNIQUE(certificate)
);
`

//go:generate -command mapper lxd-generate db mapper -t schema.mapper.go
//go:generate mapper reset
//
//go:generate mapper stmt -e internal_schema objects table=internal_schemas version=2
//
//go:generate mapper method -e internal_schema GetMany version=2

// InternalSchema represents the database schema table.
type InternalSchema struct {
	ID        int
	Version   string `db:"primary=yes"`
	UpdatedAt time.Time
}

// InternalSchemaFilter represents the database schema table.
type InternalSchemaFilter struct {
	ID        *int
	Version   *string
	UpdatedAt *time.Time
}
