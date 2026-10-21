package main

import (
	"crypto/tls"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/thomas-illiet/jupyter-kubeopencode/internal/mtlsproxy"
)

// The production image build overlays these files from BuildKit secrets. The
// private key is therefore present only inside the resulting static binary.
//
//go:embed certs/*
var embeddedCredentials embed.FS

func main() {
	if err := run(); err != nil {
		slog.Error("model mTLS proxy stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	listen := flag.String("listen", "127.0.0.1:9443", "loopback listen address")
	upstream := flag.String("upstream", "", "fixed HTTPS model gateway URL")
	flag.Parse()
	if *upstream == "" {
		return errors.New("--upstream is required")
	}
	clientPEM, err := fs.ReadFile(embeddedCredentials, "certs/client.crt")
	if err != nil {
		return fmt.Errorf("read embedded client certificate: %w", err)
	}
	keyPEM, err := fs.ReadFile(embeddedCredentials, "certs/client.key")
	if err != nil {
		return fmt.Errorf("read embedded client key: %w", err)
	}
	caPEM, err := fs.ReadFile(embeddedCredentials, "certs/gateway-ca.crt")
	if err != nil {
		return fmt.Errorf("read embedded gateway CA: %w", err)
	}
	certificate, err := tls.X509KeyPair(clientPEM, keyPEM)
	if err != nil {
		return fmt.Errorf("parse embedded client identity: %w", err)
	}
	handler, err := mtlsproxy.NewHandler(*upstream, certificate, caPEM)
	if err != nil {
		return err
	}
	listener, err := mtlsproxy.ListenLoopback(*listen)
	if err != nil {
		return err
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 1 << 20}
	slog.Info("model mTLS proxy listening", "address", listener.Addr().String())
	return server.Serve(listener)
}
