package cluster

//go:generate -command mapper lxd-generate db mapper -t certificates.mapper.go
//go:generate mapper reset
//
//go:generate mapper stmt -e internal_certificate objects table=internal_certificates version=2
//go:generate mapper stmt -e internal_certificate objects-by-Fingerprint table=internal_certificates version=2
//go:generate mapper stmt -e internal_certificate id table=internal_certificates version=2
//go:generate mapper stmt -e internal_certificate create table=internal_certificates version=2
//go:generate mapper stmt -e internal_certificate delete-by-Fingerprint table=internal_certificates version=2
//go:generate mapper stmt -e internal_certificate delete-by-Name-and-Type table=internal_certificates version=2
//go:generate mapper stmt -e internal_certificate update table=internal_certificates version=2
//
//go:generate mapper method -i -e internal_certificate GetMany version=2
//go:generate mapper method -i -e internal_certificate GetOne version=2
//go:generate mapper method -i -e internal_certificate ID version=2
//go:generate mapper method -i -e internal_certificate Exists version=2
//go:generate mapper method -i -e internal_certificate Create version=2
//go:generate mapper method -i -e internal_certificate DeleteOne-by-Fingerprint version=2
//go:generate mapper method -i -e internal_certificate DeleteMany-by-Name-and-Type version=2
//go:generate mapper method -i -e internal_certificate Update version=2

// InternalCertificate is here to pass the certificates content from the database around.
type InternalCertificate struct {
	ID          int
	Fingerprint string `db:"primary=yes"`
	Type        CertificateType
	Name        string
	Certificate string
}

// InternalCertificateFilter specifies potential query parameter fields.
type InternalCertificateFilter struct {
	Fingerprint *string
	Name        *string
	Type        *CertificateType
}

// CertificateType indicates the type of the certificate.
type CertificateType int

// CertificateTypeClient indicates a client certificate type.
const CertificateTypeClient = CertificateType(1)

// CertificateTypeServer indicates a server certificate type.
const CertificateTypeServer = CertificateType(2)
