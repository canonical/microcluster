package api

// These are the extensions that are present when the daemon starts.
var extensions = []string{
	"custom_extension_a_0",
	"custom_extension_a_1",
}

// Extensions returns the list of MicroOVN extensions.
func Extensions() []string {
	return extensions
}
