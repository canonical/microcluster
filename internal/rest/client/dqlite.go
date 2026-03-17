package client

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"

	"github.com/canonical/lxd/shared/revert"

	"github.com/canonical/microcluster/v4/microcluster/types"
)

// DialDqlite dials a remote member's internal database endpoint and upgrades the
// connection to the dqlite protocol.
func DialDqlite(ctx context.Context, addr string, config *tls.Config) (net.Conn, error) {
	if config == nil {
		return nil, fmt.Errorf("Invalid TLS config")
	}

	addrPort, err := types.ParseAddrPort(addr)
	if err != nil {
		return nil, fmt.Errorf("Failed to parse the address: %w", err)
	}

	request := &http.Request{
		Method:     "POST",
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     make(http.Header),
		Host:       addrPort.String(),
	}

	request.URL = &url.URL{
		Scheme: "https",
		Host:   addrPort.String(),
		Path:   fmt.Sprintf("/%s/%s", types.InternalEndpoint, "database"),
	}

	request.Header.Set("Upgrade", "dqlite")
	request.Header.Set("X-Dqlite-Version", "1")
	request = request.WithContext(ctx)

	reverter := revert.New()
	defer reverter.Fail()

	tlsDialer := tls.Dialer{Config: config}
	conn, err := tlsDialer.DialContext(ctx, "tcp", addrPort.String())
	if err != nil {
		return nil, fmt.Errorf("Failed connecting to HTTP endpoint %q: %w", addrPort.String(), err)
	}

	reverter.Add(func() {
		_ = conn.Close()
	})

	err = request.Write(conn)
	if err != nil {
		return nil, fmt.Errorf("Failed sending HTTP request to %q: %w", request.URL, err)
	}

	response, err := http.ReadResponse(bufio.NewReader(conn), request)
	if err != nil {
		return nil, fmt.Errorf("Failed to read response: %w", err)
	}

	reverter.Add(func() {
		_ = response.Body.Close()
	})

	_, err = io.Copy(io.Discard, response.Body)
	if err != nil {
		return nil, fmt.Errorf("Failed to read dqlite response body: %w", err)
	}

	err = response.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("Failed to close dqlite response body: %w", err)
	}

	if response.StatusCode == http.StatusUpgradeRequired {
		return nil, fmt.Errorf("Upgrade needed")
	}

	if response.StatusCode != http.StatusSwitchingProtocols {
		return nil, fmt.Errorf("Dialing failed: expected status code 101 got %d", response.StatusCode)
	}

	if response.Header.Get("Upgrade") != "dqlite" {
		return nil, fmt.Errorf("Missing or unexpected Upgrade header in response")
	}

	reverter.Success()
	return conn, nil
}
