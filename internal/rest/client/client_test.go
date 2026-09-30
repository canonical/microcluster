package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/canonical/lxd/shared/api"
	"github.com/stretchr/testify/require"

	"github.com/canonical/microcluster/v4/microcluster/types"
)

type testMember struct {
	Name string `json:"name"`
}

// newDelayedBodyServer returns a server that sends the response headers first
// and the response body only after a short delay.
func newDelayedBodyServer(t *testing.T) (*httptest.Server, []byte) {
	t.Helper()

	metadata, err := json.Marshal([]testMember{{Name: "m1"}, {Name: "m2"}})
	require.NoError(t, err)

	body, err := json.Marshal(api.Response{
		Type:       api.SyncResponse,
		Status:     api.Success.String(),
		StatusCode: int(api.Success),
		Metadata:   metadata,
	})
	require.NoError(t, err)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		flusher, ok := w.(http.Flusher)
		if ok {
			flusher.Flush()
		}

		time.Sleep(100 * time.Millisecond)
		_, _ = w.Write(body)
	}))

	t.Cleanup(server.Close)

	return server, body
}

func newTestClient(t *testing.T, server *httptest.Server) *Client {
	t.Helper()

	serverURL, err := url.Parse(server.URL)
	require.NoError(t, err)

	c, err := New(serverURL, nil, nil, false)
	require.NoError(t, err)

	return c
}

// callerContexts returns the contexts a caller may pass to a query:
// one without a deadline, which makes rawQuery add its own timeout, and one with a deadline.
func callerContexts() []struct {
	name   string
	getCtx func(t *testing.T) context.Context
} {
	return []struct {
		name   string
		getCtx func(t *testing.T) context.Context
	}{
		{
			name: "No deadline",
			getCtx: func(t *testing.T) context.Context {
				return context.Background()
			},
		},
		{
			name: "Caller deadline",
			getCtx: func(t *testing.T) context.Context {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				t.Cleanup(cancel)

				return ctx
			},
		},
	}
}

// TestQueryReadsDelayedBody checks that Query can read a response body that
// arrives some time after the response headers.
//
// Without a caller deadline, rawQuery adds its own timeout. That timeout must
// stay alive until Query has read the body, or the read fails with
// "context canceled". The caller deadline case never took that path and serves
// as a control.
func TestQueryReadsDelayedBody(t *testing.T) {
	t.Parallel()

	for _, tt := range callerContexts() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server, _ := newDelayedBodyServer(t)
			c := newTestClient(t, server)

			var members []testMember
			err := c.Query(tt.getCtx(t), "GET", types.PublicEndpoint, &api.NewURL().Path("cluster").URL, nil, &members)
			require.NoError(t, err)
			require.Equal(t, []testMember{{Name: "m1"}, {Name: "m2"}}, members)
		})
	}
}

// TestQueryRawReadsDelayedBody checks that the body of a response returned by
// QueryRaw can still be read after QueryRaw has returned, with and without a
// caller deadline. The body arrives some time after the headers, so reading it
// fails if the request context was already cancelled when QueryRaw returned.
func TestQueryRawReadsDelayedBody(t *testing.T) {
	t.Parallel()

	for _, tt := range callerContexts() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server, body := newDelayedBodyServer(t)
			c := newTestClient(t, server)

			resp, err := c.QueryRaw(tt.getCtx(t), "GET", types.PublicEndpoint, &api.NewURL().Path("cluster").URL, nil)
			require.NoError(t, err)
			defer resp.Body.Close()

			got, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.JSONEq(t, string(body), string(got))
		})
	}
}

// TestCancelOnCloseBody checks that closing the wrapped response body cancels
// the request context, so the timeout added by rawQuery is released once the
// caller is done with the response.
func TestCancelOnCloseBody(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	body := &cancelOnCloseBody{ReadCloser: io.NopCloser(nil), cancel: cancel}

	require.NoError(t, ctx.Err())
	require.NoError(t, body.Close())
	require.ErrorIs(t, ctx.Err(), context.Canceled)
}
