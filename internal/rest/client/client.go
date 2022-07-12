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

	clusterRequest "github.com/lxc/lxd/lxd/cluster/request"
	"github.com/lxc/lxd/lxd/request"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/tcp"
)

// EndpointType is a type specifying the endpoint on with the resource exists.
type EndpointType string

const (
	// ExtendedEndpoint - All endpoints added managed by external usage of MicroCluster.
	ExtendedEndpoint EndpointType = "1.0"

	// PublicEndpoint - Internally managed APIs available without authentication.
	PublicEndpoint EndpointType = "cluster/1.0"

	// InternalEndpoint - all endpoints restricted to trusted servers.
	InternalEndpoint EndpointType = "cluster/internal"

	// ControlEndpoint - all endpoints available on the local unix socket.
	ControlEndpoint EndpointType = "cluster/control"
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

	tlsDialContext := func(ctx context.Context, network string, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}

		addrs, err := net.LookupHost(host)
		if err != nil {
			return nil, err
		}

		for _, a := range addrs {
			dialer := tls.Dialer{NetDialer: &net.Dialer{}, Config: tlsConfig}
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(a, port))
			if err != nil {
				continue
			}

			tcpConn, err := tcp.ExtractConn(conn)
			if err != nil {
				return nil, err
			}

			err = tcp.SetTimeouts(tcpConn)
			if err != nil {
				return nil, err
			}

			return conn, nil
		}

		return nil, fmt.Errorf("Unable to connect to: %q", addr)
	}

	transport := &http.Transport{
		DisableKeepAlives: true,
		DialTLSContext:    tlsDialContext,
		Proxy:             proxy,
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

func (c *Client) rawQuery(ctx context.Context, method string, url *api.URL, data any) (*api.Response, error) {
	var req *http.Request
	var err error

	// Assign a context timeout if we don't already have one.
	_, ok := ctx.Deadline()
	if !ok {
		timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
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

	parsedResponse, err := parseResponse(resp)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()
	_, err = io.Copy(io.Discard, resp.Body)
	if err != nil {
		logger.Error("Failed to read response body", logger.Ctx{"error": err})
	}

	return parsedResponse, nil
}

// QueryStruct sends a request of the specified method to the provided endpoint (optional) on the API matching the endpointType.
// The response gets unpacked into the target struct. POST requests can optionally provide raw data to be sent through.
//
// The final URL is that provided as the endpoint combined with the applicable prefix for the endpointType and the scheme and host from the client.
func (c *Client) QueryStruct(ctx context.Context, method string, endpointType EndpointType, endpoint *api.URL, data any, target any) error {
	// Merge the provided URL with the one we have for the client.
	localURL := api.NewURL()
	if endpoint != nil {
		// Get a new local struct to avoid modifying the provided one.
		newURL := *endpoint
		localURL = &newURL
	}

	localURL.URL.Host = c.url.URL.Host
	localURL.URL.Scheme = c.url.URL.Scheme
	localURL.URL.Path = "/" + string(endpointType) + localURL.URL.Path

	// Send the actual query through.
	resp, err := c.rawQuery(ctx, method, localURL, data)
	if err != nil {
		return err
	}

	// Unpack into the target struct.
	err = resp.MetadataAsStruct(&target)
	if err != nil {
		return err
	}

	// Log the data.
	logger.Debugf("Got response struct from microcluster daemon")
	// TODO: Log.pretty.
	return nil
}

// URL returns the address used for the client.
func (c *Client) URL() api.URL {
	return c.url
}
