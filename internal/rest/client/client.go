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

	clusterRequest "github.com/canonical/lxd/lxd/cluster/request"
	"github.com/canonical/lxd/lxd/request"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/tcp"
	"github.com/gorilla/websocket"

	"github.com/canonical/microcluster/v3/rest/response"
	"github.com/canonical/microcluster/v3/rest/types"
)

// Client is a rest client for the daemon.
type Client struct {
	*http.Client
	url api.URL
}

// New returns a new client configured with the given url and certificates.
func New(url api.URL, clientCert *shared.CertInfo, remoteCert *x509.Certificate, forwarding bool) (*Client, error) {
	var err error
	var httpClient *http.Client

	// If the url is an absolute path to the control.socket, return a client to the local unix socket.
	if strings.HasSuffix(url.String(), "control.socket") && path.IsAbs(url.Hostname()) {
		httpClient, err = unixHTTPClient(shared.HostPath(url.Hostname()))
		url.Host(filepath.Base(url.Hostname()))
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

// SetClusterNotification sets the client's proxy to apply the forwarding headers to a request.
func (c *Client) SetClusterNotification() {
	c.Transport.(*http.Transport).Proxy = forwardingProxy
}

func forwardingProxy(r *http.Request) (*url.URL, error) {
	r.Header.Set("User-Agent", clusterRequest.UserAgentNotifier)

	ctx := r.Context()

	val, ok := ctx.Value(request.CtxUsername).(string)
	if ok {
		r.Header.Add(request.HeaderForwardedUsername, val)
	}
	val, ok = ctx.Value(request.CtxProtocol).(string)
	if ok {
		r.Header.Add(request.HeaderForwardedProtocol, val)
	}

	r.Header.Add(request.HeaderForwardedAddress, r.RemoteAddr)

	return shared.ProxyFromEnvironment(r)
}

// IsForwardedRequest determines if this request has been forwarded from another cluster member.
func IsForwardedRequest(r *http.Request) bool {
	return r.Header.Get("User-Agent") == clusterRequest.UserAgentNotifier
}

func (c *Client) rawQuery(ctx context.Context, method string, url *api.URL, data any) (*http.Response, error) {
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

			// Log the data
			logger.Debugf("%v", data)
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
func (c *Client) MakeRequest(r *http.Request) (*api.Response, error) {
	// Send the request
	resp, err := c.Do(r)
	if err != nil {
		return nil, err
	}

	parsedResponse, err := response.ParseResponse(resp)
	if err != nil {
		return nil, err
	}

	return parsedResponse, nil
}

func (c *Client) mergeURL(endpointType types.EndpointPrefix, endpoint *api.URL) *api.URL {
	localURL := api.NewURL()
	if endpoint != nil {
		// Get a new local struct to avoid modifying the provided one.
		newURL := *endpoint
		localURL = &newURL
	}

	localURL.URL.Host = c.url.URL.Host
	localURL.URL.Scheme = c.url.URL.Scheme
	localURL.URL.Path = filepath.Join("/", string(endpointType), localURL.URL.Path)
	localURL.URL.RawPath = filepath.Join("/", string(endpointType), localURL.URL.RawPath)

	localQuery := localURL.URL.Query()
	clientQuery := c.url.URL.Query()
	for k := range localQuery {
		clientQuery.Set(k, localQuery.Get(k))
	}

	localURL.URL.RawQuery = clientQuery.Encode()
	return localURL
}

// QueryStruct sends a request of the specified method to the provided endpoint (optional) on the API matching the endpointType.
// The response gets unpacked into the target struct. POST requests can optionally provide raw data to be sent through.
//
// The final URL is that provided as the endpoint combined with the applicable prefix for the endpointType and the scheme and host from the client.
func (c *Client) QueryStruct(ctx context.Context, method string, endpointType types.EndpointPrefix, endpoint *api.URL, data any, target any) error {
	resp, err := c.QueryStructRaw(ctx, method, endpointType, endpoint, data)
	if err != nil {
		return err
	}

	response, err := response.ParseResponse(resp)
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

// QueryStructRaw sends a request of the specified method to the provided endpoint (optional) on the API matching the endpointType.
// The raw response is returned. POST requests can optionally provide raw data to be sent through.
//
// The final URL is that provided as the endpoint combined with the applicable prefix for the endpointType and the scheme and host from the client.
func (c *Client) QueryStructRaw(ctx context.Context, method string, endpointType types.EndpointPrefix, endpoint *api.URL, data any) (*http.Response, error) {
	// Merge the provided URL with the one we have for the client.
	localURL := c.mergeURL(endpointType, endpoint)

	// Send the actual query through.
	resp, err := c.rawQuery(ctx, method, localURL, data)
	if err != nil {
		return nil, err
	}

	// Log the data.
	// TODO: log pretty.
	logger.Debug("Got raw response struct from microcluster daemon", logger.Ctx{"endpoint": localURL.String(), "method": method})
	return resp, nil
}

// RawWebsocket dials the provided endpoint and tries to upgrade the connection.
//
// The final URL is that provided as the endpoint combined with the applicable prefix for the endpointType and the scheme and host from the client.
func (c *Client) RawWebsocket(ctx context.Context, endpointType types.EndpointPrefix, endpoint *api.URL) (*websocket.Conn, error) {
	// Merge the provided URL with the one we have for the client.
	localURL := c.mergeURL(endpointType, endpoint)

	// Pick the right scheme based on the client configuration.
	if c.url.URL.Scheme == "http" {
		localURL.URL.Scheme = "ws"
	} else {
		localURL.URL.Scheme = "wss"
	}

	// Get the transport configuration from the HTTP client.
	tr, ok := c.Client.Transport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("Invalid underlying client transport, expected %T, got %T", &http.Transport{}, c.Client.Transport)
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
			_, err := response.ParseResponse(resp)
			if err != nil {
				return nil, fmt.Errorf("Failed websocket upgrade request: %w", err)
			}
		}

		return nil, fmt.Errorf("Failed to establish websocket connection: %w", err)
	}

	logger.Debug("Established websocket connection", logger.Ctx{"endpoint": localURL.String()})

	return conn, nil
}

// URL returns the address used for the client.
func (c *Client) URL() api.URL {
	return c.url
}

// UseTarget returns a new client with the query "?target=name" set.
func (c *Client) UseTarget(name string) *Client {
	localURL := api.NewURL()
	localURL.URL.Host = c.url.URL.Host
	localURL.URL.Scheme = c.url.URL.Scheme
	localURL.URL.Path = c.url.URL.Path
	localURL.URL.RawQuery = c.url.URL.RawQuery
	localURL = localURL.WithQuery("target", name)

	return &Client{
		Client: c.Client,
		url:    *localURL,
	}
}
