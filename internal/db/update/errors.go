package update

import "fmt"

// ErrGracefulAbort is a special error that indicates a non successful schema check.
var ErrGracefulAbort = fmt.Errorf("schema check gracefully aborted")
