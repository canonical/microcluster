package access

import (
	"context"
	"net/http"

	"github.com/canonical/microcluster/v2/internal/rest/client"
)

// TrustedRequest holds data pertaining to what level of trust we have for the request.
type TrustedRequest struct {
	Trusted bool
}

// SetRequestAuthentication sets the trusted status for the request. A trusted request will be treated as having come from a trusted system.
func SetRequestAuthentication(r *http.Request, trusted bool) *http.Request {
	r = r.WithContext(context.WithValue(r.Context(), any(client.CtxAccess), TrustedRequest{Trusted: trusted}))

	return r
}
