package access

import (
	"context"
	"net/http"

	"github.com/canonical/lxd/lxd/request"
)

// TrustedRequest holds data pertaining to what level of trust we have for the request.
type TrustedRequest struct {
	Trusted bool
}

// SetRequestAuthentication sets the trusted status for the request. A trusted request will be treated as having come from a trusted system.
func SetRequestAuthentication(r *http.Request, trusted bool) *http.Request {
	r = r.WithContext(context.WithValue(r.Context(), any(request.CtxAccess), TrustedRequest{Trusted: trusted}))

	return r
}
