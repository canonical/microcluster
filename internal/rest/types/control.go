package types

// Control represents the arguments that can be used to initialize/shutdown the daemon.
type Control struct {
	Bootstrap bool   `json:"bootstrap" yaml:"bootstrap"`
	JoinToken string `json:"join_token" yaml:"join_token"`
}
