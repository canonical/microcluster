package cluster

// Code generation directives.
//
//go:generate -command mapper lxd-generate db mapper -t token_records.mapper.go
//go:generate mapper reset
//
//go:generate mapper stmt -e internal_token_record objects table=internal_token_records version=2
//go:generate mapper stmt -e internal_token_record objects-by-JoinerCert table=internal_token_records version=2
//go:generate mapper stmt -e internal_token_record id table=internal_token_records version=2
//go:generate mapper stmt -e internal_token_record create table=internal_token_records version=2
//go:generate mapper stmt -e internal_token_record delete-by-JoinerCert table=internal_token_records version=2
//
//go:generate mapper method -e internal_token_record ID version=2
//go:generate mapper method -e internal_token_record Exists version=2
//go:generate mapper method -e internal_token_record GetOne version=2
//go:generate mapper method -e internal_token_record GetMany version=2
//go:generate mapper method -e internal_token_record Create version=2
//go:generate mapper method -e internal_token_record DeleteOne-by-JoinerCert version=2

// InternalTokenRecord is the database representation of a join token record.
type InternalTokenRecord struct {
	ID         int
	Token      string
	JoinerCert string `db:"primary=yes"`
}

// InternalTokenRecordFilter is the filter struct for filtering results from generated methods.
type InternalTokenRecordFilter struct {
	ID         *int
	Token      *string
	JoinerCert *string
}
