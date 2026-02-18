package types

// Status represents server status information.
type Status struct {
	Name       string     `json:"name"    yaml:"name"`
	Address    AddrPort   `json:"address" yaml:"address"`
	Version    string     `json:"version" yaml:"version"`
	Ready      bool       `json:"ready"   yaml:"ready"`
	Extensions Extensions `json:"extensions" yaml:"extensions"`
}
