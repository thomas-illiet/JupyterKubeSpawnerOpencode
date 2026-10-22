package main

import (
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/thomas-illiet/jupyter-kubeopencode/internal/credentialbundle"
	"github.com/thomas-illiet/jupyter-kubeopencode/internal/mtlsproxy"
)

// The production image injects an authenticated encrypted bundle through
// linker variables. This prevents PEM recovery with a text editor or strings;
// it is obfuscation, not a substitute for gateway-side workload authorization.
var (
	embeddedCredentialKey    string
	embeddedCredentialBundle string
)

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
	clientPEM, keyPEM, caPEM, err := credentialbundle.Open(embeddedCredentialKey, embeddedCredentialBundle)
	if err != nil {
		return fmt.Errorf("open embedded mTLS credentials: %w", err)
	}
	defer credentialbundle.Zero(clientPEM)
	defer credentialbundle.Zero(keyPEM)
	defer credentialbundle.Zero(caPEM)
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
