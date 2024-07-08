package types

// ServerConfig represents the mutable fields of an additional network listener.
type ServerConfig struct {
	// Address is the server listen address.
	// Example: 127.0.0.1:9000
	Address AddrPort `json:"address" yaml:"address"`
}
