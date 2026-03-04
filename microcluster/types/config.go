package types

// DaemonConfig is the in memory version of the local daemon.yaml file.
type DaemonConfig struct {
	Name          string                  `json:"name" yaml:"name"`
	Address       AddrPort                `json:"address" yaml:"address"`
	Servers       map[string]ServerConfig `json:"servers" yaml:"servers"`
	FailureDomain uint64                  `json:"failure-domain" yaml:"failure-domain"`
}

// DaemonConfigPatch is the request body for PATCH /core/1.0/daemon/config.
// Optional fields preserve existing values when omitted.
type DaemonConfigPatch struct {
	Servers       *map[string]ServerConfig `json:"servers,omitempty" yaml:"servers,omitempty"`
	FailureDomain *uint64                  `json:"failure-domain,omitempty" yaml:"failure-domain,omitempty"`
}
