package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	dqliteClient "github.com/canonical/go-dqlite/v3/client"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	internalClient "github.com/canonical/microcluster/v4/internal/rest/client"
)

type cmdDescribe struct {
	common *CmdControl

	flagTimeout int
}

func (c *cmdDescribe) command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "describe <address>",
		Short: "Describe dqlite node metadata",
		RunE:  c.run,
	}

	cmd.Flags().IntVarP(&c.flagTimeout, "timeout", "t", 5, "Number of seconds before timing out")
	return cmd
}

func (c *cmdDescribe) run(cmd *cobra.Command, args []string) error {
	if len(args) != 1 {
		return cmd.Help()
	}

	ctx := cmd.Context()
	var cancel context.CancelFunc = func() {}
	if c.flagTimeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, time.Duration(c.flagTimeout)*time.Second)
	}

	defer cancel()

	meta, err := describeDqliteNodeMetadata(ctx, c.common.FlagStateDir, args[0])
	if err != nil {
		return err
	}

	out, err := yaml.Marshal(struct {
		FailureDomain uint64 `yaml:"failure-domain"`
		Weight        uint64 `yaml:"weight"`
	}{
		FailureDomain: meta.FailureDomain,
		Weight:        meta.Weight,
	})
	if err != nil {
		return fmt.Errorf("Failed marshalling dqlite node metadata: %w", err)
	}

	fmt.Print(string(out))
	return nil
}

// describeDqliteNodeMetadata connects to the given dqlite address and returns node metadata.
// This helper exists to support developer/test verification workflows and is not intended as a
// stable API surface.
func describeDqliteNodeMetadata(ctx context.Context, stateDir string, address string) (*dqliteClient.NodeMetadata, error) {
	serverCert, err := tls.LoadX509KeyPair(filepath.Join(stateDir, "server.crt"), filepath.Join(stateDir, "server.key"))
	if err != nil {
		return nil, fmt.Errorf("Failed loading server keypair: %w", err)
	}

	clusterPEM, err := os.ReadFile(filepath.Join(stateDir, "cluster.crt"))
	if err != nil {
		return nil, fmt.Errorf("Failed reading cluster certificate: %w", err)
	}

	block, _ := pem.Decode(clusterPEM)
	if block == nil {
		return nil, fmt.Errorf("Failed decoding cluster certificate")
	}

	clusterX509, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("Failed parsing cluster certificate: %w", err)
	}

	pool := x509.NewCertPool()
	ok := pool.AppendCertsFromPEM(clusterPEM)
	if !ok {
		return nil, fmt.Errorf("Failed appending cluster certificates to pool")
	}

	dial := func(ctx context.Context, address string) (net.Conn, error) {
		config := &tls.Config{
			Certificates: []tls.Certificate{serverCert},
			RootCAs:      pool,
			MinVersion:   tls.VersionTLS12,
		}

		if len(clusterX509.DNSNames) > 0 {
			config.ServerName = clusterX509.DNSNames[0]
		}

		return internalClient.DialDqlite(ctx, address, config)
	}

	client, err := dqliteClient.New(ctx, address, dqliteClient.WithDialFunc(dial))
	if err != nil {
		return nil, fmt.Errorf("Failed connecting to dqlite: %w", err)
	}

	defer client.Close()

	meta, err := client.Describe(ctx)
	if err != nil {
		return nil, fmt.Errorf("Failed describing dqlite node metadata: %w", err)
	}

	return meta, nil
}
