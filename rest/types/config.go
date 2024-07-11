package types

// DaemonConfig is the in memory version of the local daemon.yaml file.
type DaemonConfig struct {
	Name    string                  `json:"name" yaml:"name"`
	Address AddrPort                `json:"address" yaml:"address"`
	Servers map[string]ServerConfig `json:"servers" yaml:"servers"`
}
