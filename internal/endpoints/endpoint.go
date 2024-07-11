package endpoints

// Endpoint represents the common methods of an Endpoint.
type Endpoint interface {
	Listen() error
	Serve()
	Close() error
	Type() EndpointType
}

// EndpointType enumerates the supported endpoints.
type EndpointType int

const (
	// EndpointControl represents the control endpoint accessible via unix socket.
	EndpointControl = iota

	// EndpointNetwork represents the user endpoint accessible over https (on a different port to the user endpoint).
	EndpointNetwork
)

const (
	// EndpointsUnix represents the name of the Unix endpoints.
	EndpointsUnix string = "unix"

	// EndpointsCore represents the name of the core API endpoints.
	EndpointsCore string = "core"
)

// String labels EndpointTypes for logging purposes.
func (et EndpointType) String() string {
	switch et {
	case EndpointControl:
		return "control socket"
	case EndpointNetwork:
		return "https socket"
	default:
		return ""
	}
}
