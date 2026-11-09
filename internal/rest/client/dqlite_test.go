package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Ensures DialDqlite gives up on the HTTP upgrade once the context is done, if the remote member
// never answers it.
func TestDialDqliteUpgradeTimeout(t *testing.T) {
	released := make(chan struct{})
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-released
	}))

	server.StartTLS()
	defer server.Close()
	defer close(released)

	transport, ok := server.Client().Transport.(*http.Transport)
	require.True(t, ok)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	conn, err := DialDqlite(ctx, server.Listener.Addr().String(), transport.TLSClientConfig.Clone())
	require.Error(t, err)
	require.Nil(t, conn)
	require.Less(t, time.Since(start), 5*time.Second)
}
