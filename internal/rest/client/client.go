package client

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/tcp"
	"github.com/gorilla/websocket"

	"github.com/canonical/microcluster/v4/microcluster/types"
)

// Client is a rest client for the daemon.
type Client struct {
	*http.Client
	url *url.URL
}

// CtxKey is the type used for all fields stored in the request context by Microcluster.
type CtxKey string

const (
	// CtxAccess is the access field in request context.
	CtxAccess CtxKey = "access"
)

// New returns a new client configured with the given url and certificates.
func New(url *url.URL, clientCert *shared.CertInfo, remoteCert *x509.Certificate, forwarding bool) (*Client, error) {
	var err error
	var httpClient *http.Client

	// If the url is an absolute path to the control.socket, return a client to the local unix socket.
	if strings.HasSuffix(url.String(), "control.socket") && path.IsAbs(url.Hostname()) {
		httpClient, err = unixHTTPClient(shared.HostPath(url.Hostname()))
		url.Host = filepath.Base(url.Hostname())
	} else {
		proxy := shared.ProxyFromEnvironment
		if forwarding {
			proxy = forwardingProxy
		}

		httpClient, err = tlsHTTPClient(clientCert, remoteCert, proxy)
	}

	if err != nil {
		return nil, err
	}

	return &Client{
		Client: httpClient,
		url:    url,
	}, nil
}

func unixHTTPClient(path string) (*http.Client, error) {
	// Setup a Unix socket dialer
	unixDial := func(ctx context.Context, network string, addr string) (net.Conn, error) {
		raddr, err := net.ResolveUnixAddr("unix", path)
		if err != nil {
			return nil, err
		}

		var d net.Dialer
		return d.DialContext(ctx, "unix", raddr.String())
	}

	// Define the http transport
	transport := &http.Transport{
		DialContext:       unixDial,
		DisableKeepAlives: true,
	}

	// Define the http client
	client := &http.Client{Transport: transport}

	// Setup redirect policy
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		// Replicate the headers
		req.Header = via[len(via)-1].Header

		return nil
	}

	return client, nil
}

func tlsHTTPClient(clientCert *shared.CertInfo, remoteCert *x509.Certificate, proxy func(req *http.Request) (*url.URL, error)) (*http.Client, error) {
	var tlsConfig *tls.Config
	if remoteCert != nil {
		var err error
		tlsConfig, err = TLSClientConfig(clientCert, remoteCert)
		if err != nil {
			return nil, fmt.Errorf("Failed to parse TLS config: %w", err)
		}
	}

	tlsDialContext := func(t *http.Transport) func(context.Context, string, string) (net.Conn, error) {
		return func(ctx context.Context, network string, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}

			addrs, err := net.LookupHost(host)
			if err != nil {
				return nil, err
			}

			var lastErr error
			for _, a := range addrs {
				dialer := tls.Dialer{NetDialer: &net.Dialer{}, Config: t.TLSClientConfig}
				conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(a, port))
				if err != nil {
					lastErr = err
					continue
				}

				tcpConn, err := tcp.ExtractConn(conn)
				if err != nil {
					return nil, err
				}

				err = tcp.SetTimeouts(tcpConn, 0)
				if err != nil {
					return nil, err
				}

				return conn, nil
			}

			return nil, fmt.Errorf("Unable to connect to %q: %w", addr, lastErr)
		}
	}

	transport := &http.Transport{
		TLSClientConfig:   tlsConfig,
		DisableKeepAlives: true,
		Proxy:             proxy,
	}

	// Define the http client
	client := &http.Client{Transport: transport}
	transport.DialTLSContext = tlsDialContext(transport)

	// Setup redirect policy
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		// Replicate the headers
		req.Header = via[len(via)-1].Header

		return nil
	}

	return client, nil
}

// HTTP returns the underlying HTTP client to allow direct modification.
func (c *Client) HTTP() *http.Client {
	return c.Client
}

// SetClusterNotification sets the client's proxy to apply the forwarding headers to a request.
func (c *Client) SetClusterNotification() {
	c.Transport.(*http.Transport).Proxy = forwardingProxy
}

func forwardingProxy(r *http.Request) (*url.URL, error) {
	r.Header.Set("User-Agent", types.UserAgentNotifier)

	return shared.ProxyFromEnvironment(r)
}

// IsForwardedRequest determines if this request has been forwarded from another cluster member.
func IsForwardedRequest(r *http.Request) bool {
	return r.Header.Get("User-Agent") == types.UserAgentNotifier
}

func (c *Client) rawQuery(ctx context.Context, method string, url *url.URL, data any) (*http.Response, error) {
	var req *http.Request
	var err error

	// Assign a context timeout if we don't already have one.
	_, ok := ctx.Deadline()
	if !ok {
		timeoutCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		ctx = timeoutCtx
		defer cancel()
	}

	// Get a new HTTP request setup
	if data != nil {
		switch data := data.(type) {
		case io.Reader:
			// Some data to be sent along with the request
			req, err = http.NewRequestWithContext(ctx, method, url.String(), data)
			if err != nil {
				return nil, err
			}

			// Set the encoding accordingly
			req.Header.Set("Content-Type", "application/octet-stream")
		default:
			// Encode the provided data
			buf := bytes.Buffer{}
			err := json.NewEncoder(&buf).Encode(data)
			if err != nil {
				return nil, err
			}

			// Some data to be sent along with the request
			// Use a reader since the request body needs to be seekable
			req, err = http.NewRequestWithContext(ctx, method, url.String(), bytes.NewReader(buf.Bytes()))
			if err != nil {
				return nil, err
			}

			// Set the encoding accordingly
			req.Header.Set("Content-Type", "application/json")
		}
	} else {
		// No data to be sent along with the request
		req, err = http.NewRequestWithContext(ctx, method, url.String(), nil)
		if err != nil {
			return nil, err
		}
	}

	// Send the request
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}

	return resp, nil
}

// MakeRequest performs a request and parses the response into an api.Response.
// This function can be used instead of the QueryStruct if custom settings have
// to be applied to the request struct.
func (c *Client) MakeRequest(r *http.Request) (*api.Response, error) {
	// Send the request
	resp, err := c.Do(r)
	if err != nil {
		return nil, err
	}

	parsedResponse, err := types.ParseResponse(resp)
	if err != nil {
		return nil, err
	}

	return parsedResponse, nil
}

func (c *Client) mergeURL(endpointType types.EndpointPrefix, endpoint *url.URL) *url.URL {
	localURL := &url.URL{}
	if endpoint != nil {
		// Get a new local struct to avoid modifying the provided one.
		newURL := *endpoint
		localURL = &newURL
	}

	localURL.Host = c.url.Host
	localURL.Scheme = c.url.Scheme
	localURL.Path = filepath.Join("/", string(endpointType), localURL.Path)
	localURL.RawPath = filepath.Join("/", string(endpointType), localURL.RawPath)

	localQuery := localURL.Query()
	clientQuery := c.url.Query()
	for k := range localQuery {
		clientQuery.Set(k, localQuery.Get(k))
	}

	localURL.RawQuery = clientQuery.Encode()
	return localURL
}

// Query sends a request of the specified method to the provided endpoint (optional) on the API matching the endpointType.
// The response gets unpacked into the target struct. POST requests can optionally provide raw data to be sent through.
//
// The final URL is that provided as the endpoint combined with the applicable prefix for the endpointType and the scheme and host from the client.
func (c *Client) Query(ctx context.Context, method string, endpointType types.EndpointPrefix, endpoint *url.URL, data any, target any) error {
	resp, err := c.QueryStructRaw(ctx, method, endpointType, endpoint, data)
	if err != nil {
		return err
	}

	response, err := types.ParseResponse(resp)
	if err != nil {
		return err
	}

	// Unpack into the target struct.
	err = response.MetadataAsStruct(&target)
	if err != nil {
		return err
	}

	return nil
}

// QueryRaw is a helper for initiating a request on any endpoints defined external to microcluster.
// Unlike Query it returns the raw HTTP response.
func (c *Client) QueryRaw(ctx context.Context, method string, prefix types.EndpointPrefix, path *url.URL, in any) (*http.Response, error) {
	queryCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	return c.QueryStructRaw(queryCtx, method, prefix, path, in)
}

// Websocket is a helper for upgrading a request to websocket on any endpoints defined external to microcluster.
// This function should be used for all client methods defined externally from microcluster.
func (c *Client) Websocket(ctx context.Context, prefix types.EndpointPrefix, path *url.URL) (*websocket.Conn, error) {
	websocketCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	return c.RawWebsocket(websocketCtx, prefix, path)
}

// QueryStructRaw sends a request of the specified method to the provided endpoint (optional) on the API matching the endpointType.
// The raw response is returned. POST requests can optionally provide raw data to be sent through.
//
// The final URL is that provided as the endpoint combined with the applicable prefix for the endpointType and the scheme and host from the client.
func (c *Client) QueryStructRaw(ctx context.Context, method string, endpointType types.EndpointPrefix, endpoint *url.URL, data any) (*http.Response, error) {
	// Merge the provided URL with the one we have for the client.
	localURL := c.mergeURL(endpointType, endpoint)

	// Send the actual query through.
	resp, err := c.rawQuery(ctx, method, localURL, data)
	if err != nil {
		return nil, err
	}

	return resp, nil
}

// RawWebsocket dials the provided endpoint and tries to upgrade the connection.
//
// The final URL is that provided as the endpoint combined with the applicable prefix for the endpointType and the scheme and host from the client.
func (c *Client) RawWebsocket(ctx context.Context, endpointType types.EndpointPrefix, endpoint *url.URL) (*websocket.Conn, error) {
	// Merge the provided URL with the one we have for the client.
	localURL := c.mergeURL(endpointType, endpoint)

	// Pick the right scheme based on the client configuration.
	if c.url.Scheme == "http" {
		localURL.Scheme = "ws"
	} else {
		localURL.Scheme = "wss"
	}

	// Get the transport configuration from the HTTP client.
	tr, ok := c.Transport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("Invalid underlying client transport, expected %T, got %T", &http.Transport{}, c.Transport)
	}

	// Setup a new websocket dialer using the already existing HTTP client.
	// As the client might be either local or remote always copy the relevant TLS config too.
	dialer := websocket.Dialer{
		NetDialContext:    tr.DialContext,
		NetDialTLSContext: tr.DialTLSContext,
		TLSClientConfig:   tr.TLSClientConfig,
		Proxy:             tr.Proxy,
	}

	// Assign a context timeout if we don't already have one.
	_, ok = ctx.Deadline()
	if !ok {
		timeoutCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		ctx = timeoutCtx
		defer cancel()
	}

	// Establish the connection
	conn, resp, err := dialer.DialContext(ctx, localURL.String(), nil)
	if err != nil {
		if resp != nil {
			_, err := types.ParseResponse(resp)
			if err != nil {
				return nil, fmt.Errorf("Failed websocket upgrade request: %w", err)
			}
		}

		return nil, fmt.Errorf("Failed to establish websocket connection: %w", err)
	}

	return conn, nil
}

// URL returns the address used for the client.
func (c *Client) URL() *url.URL {
	return c.url
}

// UseTarget returns a new client with the query "?target=name" set.
func (c *Client) UseTarget(name string) types.Client {
	localURL := api.NewURL()
	localURL.URL.Host = c.url.Host
	localURL.URL.Scheme = c.url.Scheme
	localURL.URL.Path = c.url.Path
	localURL.RawQuery = c.url.RawQuery
	localURL = localURL.WithQuery("target", name)

	return &Client{
		Client: c.Client,
		url:    &localURL.URL,
	}
}
